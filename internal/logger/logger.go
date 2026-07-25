// Package logger provides a file-based structured logger for Triad.
//
// Because bubbletea owns the terminal (stdout/stderr), ALL debug/diagnostic
// output from the agent client and tool executors must go to a log file —
// never to stdout or stderr. This package initialises a single slog.Logger
// backed by a JSON file handler and exposes it via the package-level L() accessor.
//
// Usage:
//
//	// In main.go, before starting the TUI:
//	if err := logger.Init("triad.log"); err != nil { ... }
//
//	// Anywhere else:
//	logger.L().Debug("sending request", "url", url, "model", model)
//	logger.L().Warn("retry", "attempt", 2, "delay", "4s")
package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

var (
	mu      sync.RWMutex
	global  *slog.Logger
	logFile *os.File
)

// Init opens (or creates) the log file at path and installs a JSON slog handler
// as the package-global logger. It must be called once before any call to L().
// Calling Init again replaces the logger and closes the previous log file.
func Init(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("logger.Init: could not open log file %q: %w", path, err)
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	})
	logger := slog.New(handler)

	mu.Lock()
	defer mu.Unlock()

	// Close the previous log file if Init is called more than once.
	if logFile != nil {
		_ = logFile.Close()
	}
	logFile = f
	global = logger
	return nil
}

// L returns the package-global slog.Logger. If Init has not been called yet
// (e.g. in tests), L returns a no-op logger so callers never need to nil-check.
func L() *slog.Logger {
	mu.RLock()
	l := global
	mu.RUnlock()

	if l == nil {
		return slog.New(nopHandler{})
	}
	return l
}

// Close flushes and closes the underlying log file. Call this at program exit
// after the TUI has shut down (not required for correctness — the OS will close
// the fd — but good practice to flush the last buffered writes).
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
		global = nil
	}
}

// ---------------------------------------------------------------------------
// nopHandler — satisfies slog.Handler with all operations as no-ops.
// Used as a safe default before Init is called (e.g. in unit tests).
// ---------------------------------------------------------------------------

type nopHandler struct{}

func (nopHandler) Enabled(_ context.Context, _ slog.Level) bool       { return false }
func (nopHandler) Handle(_ context.Context, _ slog.Record) error       { return nil }
func (nopHandler) WithAttrs(_ []slog.Attr) slog.Handler               { return nopHandler{} }
func (nopHandler) WithGroup(_ string) slog.Handler                     { return nopHandler{} }
