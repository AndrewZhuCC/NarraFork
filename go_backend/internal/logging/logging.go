// Package logging provides the structured logger constructor for NarraFork.
package logging

import (
	"io"
	"log/slog"
)

// NewLogger creates a structured logger writing to w at the given level.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
