package server

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// Config holds server configuration.
type Config struct {
	ListenAddr   string
	Domain       string
	Passphrase   string
	ChannelsFile string
	MaxPadding   int
	MsgLimit     int  // max messages per channel (0 = default 15)
	NoTelegram   bool // if true, fetch public channels without Telegram login
	AllowManage  bool // if true, remote channel management and sending via DNS is allowed
	EnableMedia  bool // if true, media tokens/download/preview are enabled
	Telegram     TelegramConfig
}

// Server orchestrates the DNS server and Telegram reader.
type Server struct {
	cfg    Config
	feed   *Feed
	reader *TelegramReader // nil when --no-telegram
}

// New creates a new Server.
func New(cfg Config) (*Server, error) {
	channels, err := loadChannels(cfg.ChannelsFile)
	if err != nil {
		return nil, fmt.Errorf("load channels: %w", err)
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("no channels configured in %s", cfg.ChannelsFile)
	}

	log.Printf("[server] loaded %d channels: %v", len(channels), channels)

	feed := NewFeed(channels)
	return &Server{cfg: cfg, feed: feed}, nil
}

// Run starts both the DNS server and the Telegram reader.
func (s *Server) Run(ctx context.Context) error {
	queryKey, responseKey, err := protocol.DeriveKeys(s.cfg.Passphrase)
	if err != nil {
		return fmt.Errorf("derive keys: %w", err)
	}

	// Handle login-only mode
	if s.cfg.Telegram.LoginOnly {
		reader := NewTelegramReader(s.cfg.Telegram, s.feed.ChannelNames(), s.feed, 15, s.cfg.EnableMedia)
		return reader.Run(ctx)
	}

	// Start Telegram reader in background, or public web fetcher in no-login mode.
	if !s.cfg.NoTelegram {
		msgLimit := s.cfg.MsgLimit
		if msgLimit <= 0 {
			msgLimit = 15
		}
		reader := NewTelegramReader(s.cfg.Telegram, s.feed.ChannelNames(), s.feed, msgLimit, s.cfg.EnableMedia)
		s.reader = reader
		go func() {
			if err := reader.Run(ctx); err != nil {
				log.Printf("[telegram] error: %v", err)
			}
		}()
	} else {
		msgLimit := s.cfg.MsgLimit
		if msgLimit <= 0 {
			msgLimit = 15
		}
		publicReader := NewPublicReader(s.feed.ChannelNames(), s.feed, msgLimit)
		go func() {
			if err := publicReader.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("[public] error: %v", err)
			}
		}()
		log.Println("[server] running without Telegram login; fetching public channels via t.me")
	}

	// Start DNS server (blocking, respects ctx cancellation)
	maxPad := s.cfg.MaxPadding
	if maxPad == 0 {
		maxPad = protocol.DefaultMaxPadding
	}
	dnsServer := NewDNSServer(s.cfg.ListenAddr, s.cfg.Domain, s.feed, queryKey, responseKey, maxPad, s.reader, s.cfg.AllowManage, s.cfg.ChannelsFile, s.cfg.EnableMedia)
	return dnsServer.ListenAndServe(ctx)
}

func loadChannels(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var channels []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip @ prefix
		name := strings.TrimPrefix(line, "@")
		channels = append(channels, name)
	}
	return channels, scanner.Err()
}
