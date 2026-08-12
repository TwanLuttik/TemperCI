// Package logging provides structured logging helpers shared by control plane
// and agent binaries.
package logging

import (
	"log/slog"
	"os"
)

// New returns a JSON slog logger writing to stderr at Info level.
func New() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
