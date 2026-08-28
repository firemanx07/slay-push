package main

import (
	"fmt"

	"github.com/rs/zerolog"
)

// asynqZerologAdapter routes asynq's internal logging through our zerolog
// logger.
type asynqZerologAdapter struct {
	logger zerolog.Logger
}

// Debug logs a debug message via zerolog.
func (a asynqZerologAdapter) Debug(args ...interface{}) { a.logger.Debug().Msg(fmt.Sprint(args...)) }

// Info logs an info message via zerolog.
func (a asynqZerologAdapter) Info(args ...interface{}) { a.logger.Info().Msg(fmt.Sprint(args...)) }

// Warn logs a warning message via zerolog.
func (a asynqZerologAdapter) Warn(args ...interface{}) { a.logger.Warn().Msg(fmt.Sprint(args...)) }

// Error logs an error message via zerolog.
func (a asynqZerologAdapter) Error(args ...interface{}) { a.logger.Error().Msg(fmt.Sprint(args...)) }

// Fatal logs a fatal message via zerolog.
func (a asynqZerologAdapter) Fatal(args ...interface{}) { a.logger.Fatal().Msg(fmt.Sprint(args...)) }
