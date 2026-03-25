package thefeed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sartoopjj/thefeed/internal/client"
	"github.com/sartoopjj/thefeed/internal/protocol"
	"github.com/sartoopjj/thefeed/internal/server"
	"github.com/sartoopjj/thefeed/internal/web"
)

func findFreePort(t *testing.T, network string) int {
	t.Helper()
	switch network {
	case "udp":
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("find free udp port: %v", err)
		}
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).Port
	default:
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("find free tcp port: %v", err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
}

func startDNSServer(t *testing.T, domain, passphrase string, channels []string, messages map[int][]protocol.Message) (string, context.CancelFunc) {
	t.Helper()

	qk, rk, err := protocol.DeriveKeys(passphrase)
	if err != nil {
		t.Fatalf("derive keys: %v", err)
	}

	feed := server.NewFeed(channels)
	for ch, msgs := range messages {
		feed.UpdateChannel(ch, msgs)
	}

	port := findFreePort(t, "udp")
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	dnsServer := server.NewDNSServer(addr, domain, feed, qk, rk, protocol.DefaultMaxPadding)

	ctx, cancel := context.WithCancel(context.Background())

	ready := make(chan struct{})
	go func() {
		close(ready)
		if err := dnsServer.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("dns server error: %v", err)
		}
	}()
	<-ready
	time.Sleep(100 * time.Millisecond)

	return addr, cancel
}

// --- Server E2E Tests ---

func TestE2E_FetchMetadataThroughDNS(t *testing.T) {
	domain := "feed.example.com"
	passphrase := "test-secret-key-123"
	channels := []string{"news", "tech"}

	msgs := map[int][]protocol.Message{
		1: {
			{ID: 100, Timestamp: 1700000000, Text: "Hello from news"},
			{ID: 101, Timestamp: 1700000001, Text: "Second news"},
		},
		2: {
			{ID: 200, Timestamp: 1700000010, Text: "Tech update"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}

	meta, err := fetcher.FetchMetadata()
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	if len(meta.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(meta.Channels))
	}
	if meta.Channels[0].Name != "news" {
		t.Errorf("channel 0 name = %q, want %q", meta.Channels[0].Name, "news")
	}
	if meta.Channels[1].Name != "tech" {
		t.Errorf("channel 1 name = %q, want %q", meta.Channels[1].Name, "tech")
	}
	if meta.Channels[0].LastMsgID != 100 {
		t.Errorf("channel 0 lastMsgID = %d, want 100", meta.Channels[0].LastMsgID)
	}
}

func TestE2E_FetchChannelMessages(t *testing.T) {
	domain := "feed.example.com"
	passphrase := "e2e-pass-456"
	channels := []string{"updates"}

	msgs := map[int][]protocol.Message{
		1: {
			{ID: 1, Timestamp: 1700000000, Text: "First message"},
			{ID: 2, Timestamp: 1700000001, Text: "Second message"},
			{ID: 3, Timestamp: 1700000002, Text: "Third message"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}

	meta, err := fetcher.FetchMetadata()
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	blockCount := int(meta.Channels[0].Blocks)
	if blockCount <= 0 {
		t.Fatal("expected blocks > 0")
	}

	fetchedMsgs, err := fetcher.FetchChannel(1, blockCount)
	if err != nil {
		t.Fatalf("fetch channel: %v", err)
	}

	if len(fetchedMsgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(fetchedMsgs))
	}

	for i, want := range msgs[1] {
		got := fetchedMsgs[i]
		if got.ID != want.ID || got.Text != want.Text {
			t.Errorf("message %d: got {ID:%d Text:%q}, want {ID:%d Text:%q}",
				i, got.ID, got.Text, want.ID, want.Text)
		}
	}
}

func TestE2E_FetchWithDoubleLabel(t *testing.T) {
	domain := "feed.example.com"
	passphrase := "double-label-test"
	channels := []string{"channel1"}

	msgs := map[int][]protocol.Message{
		1: {{ID: 10, Timestamp: 1700000000, Text: "Double label message"}},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetQueryMode(protocol.QueryDoubleLabel)

	meta, err := fetcher.FetchMetadata()
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	fetchedMsgs, err := fetcher.FetchChannel(1, int(meta.Channels[0].Blocks))
	if err != nil {
		t.Fatalf("fetch channel: %v", err)
	}

	if len(fetchedMsgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(fetchedMsgs))
	}
	if fetchedMsgs[0].Text != "Double label message" {
		t.Errorf("message text = %q, want %q", fetchedMsgs[0].Text, "Double label message")
	}
}

func TestE2E_WrongPassphrase(t *testing.T) {
	domain := "feed.example.com"
	channels := []string{"ch1"}

	msgs := map[int][]protocol.Message{
		1: {{ID: 1, Timestamp: 1700000000, Text: "secret"}},
	}

	resolver, cancel := startDNSServer(t, domain, "server-key", channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, "wrong-key", []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}

	_, err = fetcher.FetchMetadata()
	if err == nil {
		t.Fatal("expected error with wrong passphrase, got nil")
	}
}

func TestE2E_LargeMessages(t *testing.T) {
	domain := "feed.example.com"
	passphrase := "large-msg-test"
	channels := []string{"big"}

	longText := strings.Repeat("A", 500)
	msgs := map[int][]protocol.Message{
		1: {
			{ID: 1, Timestamp: 1700000000, Text: longText},
			{ID: 2, Timestamp: 1700000001, Text: "Short"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}

	meta, err := fetcher.FetchMetadata()
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	fetchedMsgs, err := fetcher.FetchChannel(1, int(meta.Channels[0].Blocks))
	if err != nil {
		t.Fatalf("fetch channel: %v", err)
	}

	if len(fetchedMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(fetchedMsgs))
	}
	if fetchedMsgs[0].Text != longText {
		t.Errorf("long message length = %d, want %d", len(fetchedMsgs[0].Text), len(longText))
	}
}

// --- Web UI E2E Tests ---

func TestE2E_WebAPI_ConfigAndStatus(t *testing.T) {
	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Status should show not configured
	resp, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()

	var status map[string]any
	json.NewDecoder(resp.Body).Decode(&status)
	if status["configured"] != false {
		t.Errorf("expected configured=false, got %v", status["configured"])
	}

	// GET config when not configured
	resp2, err := http.Get(base + "/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer resp2.Body.Close()
	var cfgResp map[string]any
	json.NewDecoder(resp2.Body).Decode(&cfgResp)
	if cfgResp["configured"] != false {
		t.Errorf("expected configured=false on GET config, got %v", cfgResp["configured"])
	}

	// POST config
	cfg := `{"domain":"test.example.com","key":"testpass","resolvers":["127.0.0.1:9999"],"queryMode":"single","rateLimit":10}`
	resp3, err := http.Post(base+"/api/config", "application/json", strings.NewReader(cfg))
	if err != nil {
		t.Fatalf("POST /api/config: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		body, _ := io.ReadAll(resp3.Body)
		t.Fatalf("POST /api/config status=%d body=%s", resp3.StatusCode, body)
	}

	// Status should now show configured
	resp4, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status after config: %v", err)
	}
	defer resp4.Body.Close()
	var status2 map[string]any
	json.NewDecoder(resp4.Body).Decode(&status2)
	if status2["configured"] != true {
		t.Errorf("expected configured=true, got %v", status2["configured"])
	}
	if status2["domain"] != "test.example.com" {
		t.Errorf("domain = %v, want test.example.com", status2["domain"])
	}
}

func TestE2E_WebAPI_InvalidConfig(t *testing.T) {
	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Missing required fields
	resp, err := http.Post(base+"/api/config", "application/json", strings.NewReader(`{"domain":"x"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}

	// Invalid JSON
	resp2, err := http.Post(base+"/api/config", "application/json", strings.NewReader(`not json`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Errorf("expected 400 for invalid json, got %d", resp2.StatusCode)
	}
}

func TestE2E_WebAPI_Channels(t *testing.T) {
	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	resp, err := http.Get(base + "/api/channels")
	if err != nil {
		t.Fatalf("GET /api/channels: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "null\n" && string(body) != "[]\n" {
		t.Logf("channels response: %q (acceptable)", string(body))
	}
}

func TestE2E_WebAPI_Messages(t *testing.T) {
	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	resp, err := http.Get(base + "/api/messages/1")
	if err != nil {
		t.Fatalf("GET /api/messages/1: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Invalid channel number
	resp2, err := http.Get(base + "/api/messages/abc")
	if err != nil {
		t.Fatalf("GET /api/messages/abc: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Errorf("expected 400 for invalid channel, got %d", resp2.StatusCode)
	}
}

func TestE2E_WebAPI_IndexPage(t *testing.T) {
	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestE2E_WebAPI_NotFound(t *testing.T) {
	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	resp, err := http.Get(base + "/nonexistent")
	if err != nil {
		t.Fatalf("GET /nonexistent: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestE2E_WebAPI_MethodNotAllowed(t *testing.T) {
	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	req, _ := http.NewRequest(http.MethodPut, base+"/api/config", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}

	resp2, err := http.Get(base + "/api/refresh")
	if err != nil {
		t.Fatalf("GET /api/refresh: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 405 {
		t.Errorf("expected 405 for GET /api/refresh, got %d", resp2.StatusCode)
	}
}

func TestE2E_WebAPI_ConfigPersistence(t *testing.T) {
	dataDir := t.TempDir()

	port1 := findFreePort(t, "tcp")
	srv1, err := web.New(dataDir, port1)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv1.Run()
	time.Sleep(200 * time.Millisecond)

	base1 := fmt.Sprintf("http://127.0.0.1:%d", port1)
	cfg := `{"domain":"persist.example.com","key":"persistkey","resolvers":["1.1.1.1"]}`
	resp, err := http.Post(base1+"/api/config", "application/json", strings.NewReader(cfg))
	if err != nil {
		t.Fatalf("POST config: %v", err)
	}
	resp.Body.Close()

	configPath := dataDir + "/config.json"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config.json was not persisted to disk")
	}

	port2 := findFreePort(t, "tcp")
	srv2, err := web.New(dataDir, port2)
	if err != nil {
		t.Fatalf("create second web server: %v", err)
	}
	go srv2.Run()
	time.Sleep(200 * time.Millisecond)

	base2 := fmt.Sprintf("http://127.0.0.1:%d", port2)
	resp2, err := http.Get(base2 + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status on second instance: %v", err)
	}
	defer resp2.Body.Close()

	var status map[string]any
	json.NewDecoder(resp2.Body).Decode(&status)
	if status["configured"] != true {
		t.Error("second instance should have loaded config, got configured=false")
	}
	if status["domain"] != "persist.example.com" {
		t.Errorf("domain = %v, want persist.example.com", status["domain"])
	}
}

// TestE2E_FullRoundTrip tests DNS server -> client fetcher -> web API end to end.
func TestE2E_FullRoundTrip(t *testing.T) {
	domain := "roundtrip.example.com"
	passphrase := "full-roundtrip-key"
	channels := []string{"general", "alerts"}

	msgs := map[int][]protocol.Message{
		1: {
			{ID: 1, Timestamp: 1700000000, Text: "General message 1"},
			{ID: 2, Timestamp: 1700000001, Text: "General message 2"},
		},
		2: {
			{ID: 10, Timestamp: 1700000010, Text: "Alert!"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	dataDir := t.TempDir()
	port := findFreePort(t, "tcp")
	srv, err := web.New(dataDir, port)
	if err != nil {
		t.Fatalf("create web server: %v", err)
	}
	go srv.Run()
	time.Sleep(200 * time.Millisecond)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	cfgJSON := fmt.Sprintf(`{"domain":"%s","key":"%s","resolvers":["%s"],"queryMode":"single","rateLimit":0}`,
		domain, passphrase, resolver)
	resp, err := http.Post(base+"/api/config", "application/json", strings.NewReader(cfgJSON))
	if err != nil {
		t.Fatalf("POST /api/config: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("config POST status=%d", resp.StatusCode)
	}

	// Wait for auto-refresh to fetch data
	time.Sleep(3 * time.Second)

	// Channels should be populated
	resp2, err := http.Get(base + "/api/channels")
	if err != nil {
		t.Fatalf("GET /api/channels: %v", err)
	}
	defer resp2.Body.Close()

	var chList []protocol.ChannelInfo
	json.NewDecoder(resp2.Body).Decode(&chList)
	if len(chList) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(chList))
	}
	if chList[0].Name != "general" || chList[1].Name != "alerts" {
		t.Errorf("channels = %v, want [general, alerts]", chList)
	}

	// Messages for channel 1
	resp3, err := http.Get(base + "/api/messages/1")
	if err != nil {
		t.Fatalf("GET /api/messages/1: %v", err)
	}
	defer resp3.Body.Close()

	var msgList []protocol.Message
	json.NewDecoder(resp3.Body).Decode(&msgList)
	if len(msgList) != 2 {
		t.Fatalf("expected 2 messages for channel 1, got %d", len(msgList))
	}
	if msgList[0].Text != "General message 1" {
		t.Errorf("msg[0].Text = %q, want %q", msgList[0].Text, "General message 1")
	}

	// Messages for channel 2
	resp4, err := http.Get(base + "/api/messages/2")
	if err != nil {
		t.Fatalf("GET /api/messages/2: %v", err)
	}
	defer resp4.Body.Close()

	var msgList2 []protocol.Message
	json.NewDecoder(resp4.Body).Decode(&msgList2)
	if len(msgList2) != 1 {
		t.Fatalf("expected 1 message for channel 2, got %d", len(msgList2))
	}
	if msgList2[0].Text != "Alert!" {
		t.Errorf("msg[0].Text = %q, want %q", msgList2[0].Text, "Alert!")
	}
}
