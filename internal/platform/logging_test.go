package platform

import (
	"testing"

	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/config"
)

func TestNewLogger_InvalidLevelFallsBackToInfo(t *testing.T) {
	logger := NewLogger(config.Config{LogLevel: "not-a-real-level", LogFormat: "json"})
	if logger.GetLevel() != zerolog.InfoLevel {
		t.Errorf("level = %v, want %v", logger.GetLevel(), zerolog.InfoLevel)
	}
}

func TestNewLogger_ValidLevel(t *testing.T) {
	logger := NewLogger(config.Config{LogLevel: "debug", LogFormat: "json"})
	if logger.GetLevel() != zerolog.DebugLevel {
		t.Errorf("level = %v, want %v", logger.GetLevel(), zerolog.DebugLevel)
	}
}

func TestNewLogger_JSONFormatDoesNotWireSeq(t *testing.T) {
	// LogFormat != "seq" — must not attempt to reach SeqURL at all, even
	// though one is set, and must return usable logger.
	logger := NewLogger(config.Config{LogLevel: "info", LogFormat: "json", SeqURL: "http://unused:1"})
	logger.Info().Msg("should not panic or block")
}

func TestNewLogger_SeqFormatWithoutURLDoesNotWireSeq(t *testing.T) {
	// LogFormat == "seq" but SeqURL is empty — same as above, must not wire
	// the Seq writer (config.Validate() would normally reject this
	// combination before NewLogger is ever called, but NewLogger itself
	// must degrade safely rather than panic).
	logger := NewLogger(config.Config{LogLevel: "info", LogFormat: "seq", SeqURL: ""})
	logger.Info().Msg("should not panic or block")
}

func TestNewLogger_SeqFormatWiresSeqWriter(t *testing.T) {
	// A real SeqURL with LogFormat=seq wires the batching seqWriter — this
	// must not block or panic on construction (the writer flushes
	// asynchronously in the background).
	logger := NewLogger(config.Config{LogLevel: "info", LogFormat: "seq", SeqURL: "http://127.0.0.1:1"})
	logger.Info().Msg("queued for async flush, never actually sent in this test")
}
