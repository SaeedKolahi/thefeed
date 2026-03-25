package protocol

import (
	"bytes"
	"testing"
)

func TestSerializeParseMetadata(t *testing.T) {
	original := &Metadata{
		Marker:    [3]byte{0xAA, 0xBB, 0xCC},
		Timestamp: 1700000000,
		Channels: []ChannelInfo{
			{Name: "VahidOnline", Blocks: 5, LastMsgID: 1234},
			{Name: "test", Blocks: 3, LastMsgID: 5678},
		},
	}
	data := SerializeMetadata(original)
	parsed, err := ParseMetadata(data)
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	if parsed.Marker != original.Marker {
		t.Errorf("marker: got %v, want %v", parsed.Marker, original.Marker)
	}
	if parsed.Timestamp != original.Timestamp {
		t.Errorf("timestamp: got %d, want %d", parsed.Timestamp, original.Timestamp)
	}
	if len(parsed.Channels) != len(original.Channels) {
		t.Fatalf("channels: got %d, want %d", len(parsed.Channels), len(original.Channels))
	}
	for i := range original.Channels {
		if parsed.Channels[i] != original.Channels[i] {
			t.Errorf("channel %d mismatch", i)
		}
	}
}

func TestSerializeParseMessages(t *testing.T) {
	original := []Message{
		{ID: 100, Timestamp: 1700000000, Text: "Hello world"},
		{ID: 101, Timestamp: 1700000060, Text: "Test farsi"},
		{ID: 102, Timestamp: 1700000120, Text: "[IMAGE] Caption"},
	}
	data := SerializeMessages(original)
	parsed, err := ParseMessages(data)
	if err != nil {
		t.Fatalf("ParseMessages: %v", err)
	}
	if len(parsed) != len(original) {
		t.Fatalf("messages: got %d, want %d", len(parsed), len(original))
	}
	for i := range original {
		if parsed[i] != original[i] {
			t.Errorf("message %d mismatch", i)
		}
	}
}

func TestSplitIntoBlocks(t *testing.T) {
	data := bytes.Repeat([]byte("A"), DefaultBlockPayload*3+50)
	blocks := SplitIntoBlocks(data)
	if len(blocks) != 4 {
		t.Fatalf("blocks: got %d, want 4", len(blocks))
	}
	for i, b := range blocks {
		if i < 3 && len(b) != DefaultBlockPayload {
			t.Errorf("block %d: size %d, want %d", i, len(b), DefaultBlockPayload)
		}
	}
	if len(blocks[3]) != 50 {
		t.Errorf("last block: size %d, want 50", len(blocks[3]))
	}
	var reassembled []byte
	for _, b := range blocks {
		reassembled = append(reassembled, b...)
	}
	if !bytes.Equal(reassembled, data) {
		t.Error("reassembled data does not match original")
	}
}

func TestSplitIntoBlocksEmpty(t *testing.T) {
	blocks := SplitIntoBlocks(nil)
	if len(blocks) != 1 {
		t.Fatalf("empty should produce 1 block, got %d", len(blocks))
	}
}

func TestMessageRoundtripThroughBlocks(t *testing.T) {
	msgs := []Message{
		{ID: 1, Timestamp: 1700000000, Text: "Short"},
		{ID: 2, Timestamp: 1700000001, Text: string(bytes.Repeat([]byte("X"), 300))},
		{ID: 3, Timestamp: 1700000002, Text: "End"},
	}
	data := SerializeMessages(msgs)
	blocks := SplitIntoBlocks(data)
	var reassembled []byte
	for _, b := range blocks {
		reassembled = append(reassembled, b...)
	}
	parsed, err := ParseMessages(reassembled)
	if err != nil {
		t.Fatalf("ParseMessages: %v", err)
	}
	if len(parsed) != len(msgs) {
		t.Fatalf("got %d messages, want %d", len(parsed), len(msgs))
	}
	for i := range msgs {
		if parsed[i] != msgs[i] {
			t.Errorf("message %d mismatch", i)
		}
	}
}

func TestParseMetadataTooShort(t *testing.T) {
	_, err := ParseMetadata([]byte{0x01, 0x02})
	if err == nil {
		t.Error("expected error for short metadata")
	}
}
