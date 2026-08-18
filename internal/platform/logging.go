package platform

import (
	"os"

	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/config"
)

// NewLogger builds the process-wide logger. Production default is JSON to
// stdout; the Seq sink is wired in only when LOG_FORMAT=seq.
func NewLogger(cfg config.Config) zerolog.Logger {
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}

	var writer zerolog.LevelWriter = zerolog.MultiLevelWriter(os.Stdout)
	if cfg.LogFormat == "seq" && cfg.SeqURL != "" {
		writer = zerolog.MultiLevelWriter(os.Stdout, newSeqWriter(cfg.SeqURL))
	}

	return zerolog.New(writer).Level(level).With().Timestamp().Logger()
}
