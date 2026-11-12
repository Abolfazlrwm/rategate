// Package observability
//
// logger.go — structured logging via the standard library slog package
// (Go 1.21+). The logger writes JSON by default for production parity
// with the rest of the observability stack.
package observability

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a slog.Logger configured at the given level and format.
//
// level is one of: debug, info, warn, error (case-insensitive). Unknown
// levels default to info.
//
// format is "json" or "text"; anything else defaults to json.
func NewLogger(level, format string) *slog.Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{
		Level:     lvl,
		AddSource: true,
	}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
