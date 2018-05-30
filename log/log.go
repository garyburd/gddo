package log

import (
	"context"
	"os"

	"github.com/inconshreveable/log15"
)

// Type key is used as a key for context.Context values
type key int

const (
	_ key = iota
	loggerKey
)

func init() {
	// Set the log level via the GODOC_LOG_LEVEL environment variable.
	// Valid values can be found in the code for log15.LvlFromString,
	// but include the standard "debug", "info", "warn", "error", and
	// "crit" (critical).
	//
	// This only sets the Root logger on the log15 package. If the context
	// contains a logger, then this environment variable will not affect
	// log level.
	//
	// If the GODOC_LOG_LEVEL env var is blank, then we do nothing.
	if logLevelStr := os.Getenv("GODOC_LOG_LEVEL"); logLevelStr != "" {
		logLevel, _ := log15.LvlFromString(logLevelStr)
		log15.Root().SetHandler(
			log15.LvlFilterHandler(logLevel, log15.StdoutHandler),
		)
	}
}

// FromContext always returns a logger. If there is no logger in the context, it
// returns the root logger. It is not recommended for use and may be removed in
// the future.
func FromContext(ctx context.Context) log15.Logger {
	if logger, ok := ctx.Value(loggerKey).(log15.Logger); ok {
		return logger
	}

	return log15.Root()
}

// NewContext creates a new context containing the given logger. It is not
// recommended for use and may be removed in the future.
func NewContext(ctx context.Context, l log15.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// Debug is a convenient alias for FromContext(ctx).Debug
func Debug(ctx context.Context, msg string, logCtx ...interface{}) {
	FromContext(ctx).Debug(msg, logCtx...)
}

// Info is a convenient alias for FromContext(ctx).Info
func Info(ctx context.Context, msg string, logCtx ...interface{}) {
	FromContext(ctx).Info(msg, logCtx...)
}

// Warn is a convenient alias for FromContext(ctx).Warn
func Warn(ctx context.Context, msg string, logCtx ...interface{}) {
	FromContext(ctx).Warn(msg, logCtx...)
}

// Error is a convenient alias for FromContext(ctx).Error
func Error(ctx context.Context, msg string, logCtx ...interface{}) {
	FromContext(ctx).Error(msg, logCtx...)
}

// Crit is a convenient alias for FromContext(ctx).Crit
func Crit(ctx context.Context, msg string, logCtx ...interface{}) {
	FromContext(ctx).Crit(msg, logCtx...)
}

// Fatal is equivalent to Crit() followed by a call to os.Exit(1).
func Fatal(ctx context.Context, msg string, logCtx ...interface{}) {
	FromContext(ctx).Crit(msg, logCtx...)
	os.Exit(1)
}
