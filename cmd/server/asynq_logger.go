package main

import (
	"fmt"

	"github.com/rs/zerolog"
)

// asynqZerologAdapter routes asynq's own internal logging through our
// zerolog logger, so worker logs are structured/consistent with the rest of
// the process instead of asynq's default plain-text logger.
type asynqZerologAdapter struct {
	logger zerolog.Logger
}

func (a asynqZerologAdapter) Debug(args ...interface{}) { a.logger.Debug().Msg(fmt.Sprint(args...)) }
func (a asynqZerologAdapter) Info(args ...interface{})  { a.logger.Info().Msg(fmt.Sprint(args...)) }
func (a asynqZerologAdapter) Warn(args ...interface{})  { a.logger.Warn().Msg(fmt.Sprint(args...)) }
func (a asynqZerologAdapter) Error(args ...interface{}) { a.logger.Error().Msg(fmt.Sprint(args...)) }
func (a asynqZerologAdapter) Fatal(args ...interface{}) { a.logger.Fatal().Msg(fmt.Sprint(args...)) }
