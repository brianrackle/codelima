package codelima

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// tuiLogMaxBytes is the size at which the TUI file sink rotates. One generation
// (.1) is kept; there is deliberately no dependency on an external rotation
// library (ADR 59).
const tuiLogMaxBytes int64 = 5 * 1024 * 1024

// parseLogLevel maps a --log-level value to an slog.Level, defaulting to info
// for empty or unrecognized input so a typo never silences logging entirely.
func parseLogLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
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

// discardLogger is the sane default installed on every Service before any sink
// is wired, so seam log calls never panic on a nil logger.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTextLogger builds the CLI-mode sink: structured text on the given writer
// (stderr in production) filtered at level.
func newTextLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// rotatingLogWriter is an io.Writer that appends to a file and, when a write
// would push it past maxBytes, renames the current file to "<path>.1" (keeping a
// single generation) and continues in a fresh file. It is safe for concurrent
// use so the Service logger and the libghostty capture logger can share one.
type rotatingLogWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

func newRotatingLogWriter(path string, maxBytes int64) (*rotatingLogWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	w := &rotatingLogWriter{path: path, maxBytes: maxBytes}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingLogWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.maxBytes > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		// Best-effort: if rotation fails we keep writing to the current file
		// rather than dropping the log line.
		_ = w.rotate()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingLogWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		w.file = nil
		return err
	}
	w.file = nil
	_ = os.Remove(w.path + ".1")
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		// Reopen the original file so writes continue after a failed rename.
		_ = w.open()
		return err
	}
	return w.open()
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// newTUIFileLogger builds the TUI-mode sink: an append-only, size-rotated file
// under CODELIMA_HOME/_logs/codelima.log. It returns the logger and a closer
// the caller must invoke on TUI shutdown.
func newTUIFileLogger(home string, level slog.Level) (*slog.Logger, func() error, error) {
	path := filepath.Join(home, "_logs", "codelima.log")
	writer, err := newRotatingLogWriter(path, tuiLogMaxBytes)
	if err != nil {
		return nil, nil, err
	}
	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level}))
	return logger, writer.Close, nil
}

// packageLogger is a process-global sink reachable from spots where plumbing a
// Service logger is disproportionate — specifically the libghostty stderr
// capture in tui_terminal_ghostty_cgo.go, whose withGhosttyStderrSuppressed is
// a generic free function with no Service in scope (ADR 59). It defaults to
// discard and is pointed at the active TUI file sink when the TUI starts.
var (
	packageLoggerMu sync.RWMutex
	packageLogger   = discardLogger()
)

func setPackageLogger(logger *slog.Logger) {
	if logger == nil {
		logger = discardLogger()
	}
	packageLoggerMu.Lock()
	packageLogger = logger
	packageLoggerMu.Unlock()
}

func packageLog() *slog.Logger {
	packageLoggerMu.RLock()
	defer packageLoggerMu.RUnlock()
	return packageLogger
}
