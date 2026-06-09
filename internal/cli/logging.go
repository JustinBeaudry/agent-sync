package cli

import (
	"io"
	"log/slog"
	"strings"
)

// newLogger builds the process logger per AGENTS.md: structured, to
// stderr only, JSON handler in non-interactive mode, text handler on a
// TTY. Data goes to stdout; logs never do. The writer is always stderr
// (passed in so tests can capture it).
func newLogger(stderr io.Writer, access Access, level string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var h slog.Handler
	if access.IsTTY && !access.NonInteractive {
		h = slog.NewTextHandler(stderr, opts)
	} else {
		h = slog.NewJSONHandler(stderr, opts)
	}
	return slog.New(h)
}

// parseLevel maps a --log-level string to an slog level. Unknown values
// fall back to info.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
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
