package codelima

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"bogus":   slog.LevelInfo,
	}
	for input, want := range cases {
		if got := parseLogLevel(input); got != want {
			t.Fatalf("parseLogLevel(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestLogLevelFlagControlsVerbosity is the plan's logger-plumbing red test: the
// same debug-level record must appear when the handler is built at debug and be
// suppressed when built at warn.
func TestLogLevelFlagControlsVerbosity(t *testing.T) {
	t.Parallel()

	var debugBuf bytes.Buffer
	debugLogger := newTextLogger(&debugBuf, parseLogLevel("debug"))
	debugLogger.Debug("seam ping", "op", "node.start")
	if !strings.Contains(debugBuf.String(), "seam ping") {
		t.Fatalf("debug handler dropped a debug record: %q", debugBuf.String())
	}

	var warnBuf bytes.Buffer
	warnLogger := newTextLogger(&warnBuf, parseLogLevel("warn"))
	warnLogger.Debug("seam ping", "op", "node.start")
	if strings.TrimSpace(warnBuf.String()) != "" {
		t.Fatalf("warn handler should suppress debug records, got %q", warnBuf.String())
	}
	// A warning still reaches the warn handler so operators are not blind.
	warnLogger.Warn("seam warn")
	if !strings.Contains(warnBuf.String(), "seam warn") {
		t.Fatalf("warn handler dropped a warn record: %q", warnBuf.String())
	}
}

func TestRotatingLogWriterRotatesAtThreshold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "codelima.log")
	writer, err := newRotatingLogWriter(path, 32)
	if err != nil {
		t.Fatalf("newRotatingLogWriter() error = %v", err)
	}
	defer func() { _ = writer.Close() }()

	// Two writes under the cap stay in the primary file.
	if _, err := writer.Write([]byte("0123456789\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := writer.Write([]byte("abcdefghij\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatalf("did not expect a rotated generation yet")
	}

	// The write that crosses the cap rotates the current file to .1 and starts
	// a fresh primary file holding only the new line.
	if _, err := writer.Write([]byte("ROTATED-LINE\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("expected rotated file: %v", err)
	}
	if !strings.Contains(string(rotated), "abcdefghij") {
		t.Fatalf("rotated file missing prior content: %q", string(rotated))
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(current) error = %v", err)
	}
	if got := strings.TrimSpace(string(current)); got != "ROTATED-LINE" {
		t.Fatalf("primary file after rotation = %q, want only the new line", got)
	}

	// Only one generation is kept: a second rotation overwrites .1.
	if _, err := writer.Write([]byte(strings.Repeat("x", 40) + "\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	rotated, err = os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("expected rotated file after second rotation: %v", err)
	}
	if !strings.Contains(string(rotated), "ROTATED-LINE") {
		t.Fatalf("second rotation should carry the previous primary content: %q", string(rotated))
	}
}

func TestNewTUIFileLoggerWritesUnderLogsDir(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	logger, closeLog, err := newTUIFileLogger(home, slog.LevelDebug)
	if err != nil {
		t.Fatalf("newTUIFileLogger() error = %v", err)
	}
	logger.Info("tui log line", "source", "test")
	if err := closeLog(); err != nil {
		t.Fatalf("close log error = %v", err)
	}

	logPath := filepath.Join(home, "_logs", "codelima.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", logPath, err)
	}
	if !strings.Contains(string(data), "tui log line") {
		t.Fatalf("log file missing record: %q", string(data))
	}
}

// TestNewDaemonFileLoggerRotatesInPlace covers the daemon half of ADR 59's
// rotation policy. The daemon runs for days on a file nothing else truncates,
// so its structured sink has to rotate the same way the TUI's does.
func TestNewDaemonFileLoggerRotatesInPlace(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "_daemon", "daemon.log")
	logger, closeLog, err := newDaemonFileLogger(path, slog.LevelDebug)
	if err != nil {
		t.Fatalf("newDaemonFileLogger() error = %v", err)
	}
	defer func() { _ = closeLog() }()

	logger.Info("daemon log line", "source", "test")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !strings.Contains(string(data), "daemon log line") {
		t.Fatalf("daemon log missing record: %q", string(data))
	}
	if daemonLogMaxBytes <= 0 {
		t.Fatalf("daemonLogMaxBytes = %d, want a positive rotation cap", daemonLogMaxBytes)
	}
}

// TestEnableDaemonFileLoggingRedirectsServiceAndStoreSinks pins the wiring:
// runDaemon's structured records and the Store warnings that ride the same
// logger both land in the rotating daemon.log rather than the inherited
// descriptor.
func TestEnableDaemonFileLoggingRedirectsServiceAndStoreSinks(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	closeLog, err := enableDaemonFileLogging(service)
	if err != nil {
		t.Fatalf("enableDaemonFileLogging() error = %v", err)
	}
	service.log().Error("daemon seam record")
	service.store.log().Warn("store seam record")
	if err := closeLog(); err != nil {
		t.Fatalf("close daemon log error = %v", err)
	}

	path := filepath.Join(service.cfg.MetadataRoot, "_daemon", "daemon.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	for _, want := range []string{"daemon seam record", "store seam record"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("daemon log missing %q: %q", want, string(data))
		}
	}
}

func TestEnableFileLoggingContainsSuccessfulLimaDiagnostics(t *testing.T) {
	// Not parallel: enableFileLogging swaps the process-global package logger.
	original := packageLog()
	t.Cleanup(func() { setPackageLogger(original) })

	home := t.TempDir()
	binary := filepath.Join(home, "limactl")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
printf '%s\n' 'time="2026-07-21T09:12:59-07:00" level=warning msg="instance mrwing has errors"' >&2
printf '%s\n' '{"name":"mrwing","status":"Stopped"}'
`), 0o700); err != nil {
		t.Fatal(err)
	}

	var terminalStderr bytes.Buffer
	service := NewService(DefaultConfig(home), nil, nil, io.Discard, &terminalStderr)
	client, ok := service.sandbox.(*LimaClient)
	if !ok {
		t.Fatalf("sandbox = %T, want *LimaClient", service.sandbox)
	}
	client.Binary = binary

	closeLog, err := service.enableFileLogging()
	if err != nil {
		t.Fatalf("enableFileLogging() error = %v", err)
	}
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if err := closeLog(); err != nil {
		t.Fatalf("close log error = %v", err)
	}

	if terminalStderr.Len() != 0 {
		t.Fatalf("Lima diagnostic leaked to TUI stderr: %q", terminalStderr.String())
	}
	data, err := os.ReadFile(filepath.Join(home, "_logs", "codelima.log"))
	if err != nil {
		t.Fatalf("read TUI log: %v", err)
	}
	logText := string(data)
	if !strings.Contains(logText, "instance mrwing has errors") || !strings.Contains(logText, "source=limactl") {
		t.Fatalf("TUI log does not contain the Lima diagnostic: %q", logText)
	}
}

func TestPackageLoggerDefaultsToDiscardAndIsSwappable(t *testing.T) {
	// Not parallel: mutates process-global package logger.
	original := packageLog()
	t.Cleanup(func() { setPackageLogger(original) })

	// Default must be non-nil so libghostty capture never panics.
	if packageLog() == nil {
		t.Fatal("packageLog() returned nil")
	}

	var buf bytes.Buffer
	setPackageLogger(newTextLogger(&buf, slog.LevelDebug))
	packageLog().Debug("libghostty warning(osc): boom", "source", "libghostty")
	if !strings.Contains(buf.String(), "libghostty") {
		t.Fatalf("swapped package logger did not receive record: %q", buf.String())
	}

	setPackageLogger(nil)
	if packageLog() == nil {
		t.Fatal("setPackageLogger(nil) must fall back to a non-nil discard logger")
	}
}
