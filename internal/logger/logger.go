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
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

var (
	mu      sync.RWMutex
	global  *slog.Logger
	logFile io.Closer
)

const (
	// DefaultMaxBytes is the maximum size of the active log before it is
	// rotated. It is intentionally small enough to bound disk use while still
	// retaining useful diagnostics for a long interactive session.
	DefaultMaxBytes int64 = 10 * 1024 * 1024
	// DefaultMaxBackups is the number of rotated generations retained.
	DefaultMaxBackups = 5
)

// Options controls file rotation for a logger.
type Options struct {
	MaxBytes   int64
	MaxBackups int
}

// Init opens (or creates) the log file at path and installs a JSON slog handler
// as the package-global logger. It must be called once before any call to L().
// Calling Init again replaces the logger and closes the previous log file.
func Init(path string) error {
	return InitWithOptions(path, Options{})
}

// InitWithOptions installs a JSON logger that rotates path before a write
// would exceed MaxBytes. Zero-valued limits use the package defaults.
func InitWithOptions(path string, options Options) error {
	writer, err := newRotatingWriter(path, options)
	if err != nil {
		return fmt.Errorf("logger.Init: could not open log file %q: %w", path, err)
	}

	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
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
	logFile = writer
	global = logger
	return nil
}

type rotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

func newRotatingWriter(path string, options Options) (*rotatingWriter, error) {
	if options.MaxBytes <= 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	if options.MaxBackups <= 0 {
		options.MaxBackups = DefaultMaxBackups
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	w := &rotatingWriter{path: path, maxBytes: options.MaxBytes, maxBackups: options.MaxBackups, file: f, size: info.Size()}
	if w.size >= w.maxBytes {
		if err := w.rotate(); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, fmt.Errorf("write closed log")
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close active log: %w", err)
	}
	for generation := w.maxBackups; generation >= 1; generation-- {
		source := backupPath(w.path, generation)
		if generation == w.maxBackups {
			if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove oldest log backup %q: %w", source, err)
			}
			continue
		}
		target := backupPath(w.path, generation+1)
		if err := os.Rename(source, target); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate log backup %q: %w", source, err)
		}
	}
	if err := os.Rename(w.path, backupPath(w.path, 1)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate active log: %w", err)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open new active log: %w", err)
	}
	w.file = f
	w.size = 0
	return nil
}

func backupPath(path string, generation int) string {
	return filepath.Clean(fmt.Sprintf("%s.%d", path, generation))
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
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

func (nopHandler) Enabled(_ context.Context, _ slog.Level) bool  { return false }
func (nopHandler) Handle(_ context.Context, _ slog.Record) error { return nil }
func (nopHandler) WithAttrs(_ []slog.Attr) slog.Handler          { return nopHandler{} }
func (nopHandler) WithGroup(_ string) slog.Handler               { return nopHandler{} }
