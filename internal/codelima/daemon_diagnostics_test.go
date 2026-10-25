package codelima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonDiagnosticsCLIWorksWithBrokenSettings(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "_config", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	broken := []byte("daemon: [broken")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--home", home, "--json", "daemon", "diagnose", "--sample-seconds", "0"}, nil, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "control-plane probes failed") {
		t.Fatalf("capture failed: %d %s %s", code, &stdout, &stderr)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, broken) {
		t.Fatal("capture changed settings", err)
	}
	if _, err := LoadConfig(home); err == nil {
		t.Fatal("ordinary config load ignored broken settings")
	}
}

func TestDaemonDiagnosticsCaptureContinuesAfterFailedReadWithoutRecovery(t *testing.T) {
	root := t.TempDir()
	home, output := filepath.Join(root, "home"), filepath.Join(root, "bundle")
	if err := os.MkdirAll(filepath.Join(home, "_daemon"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"daemon.identity": fmt.Sprintf(`{"pid":%d,"version":"test"}`, os.Getpid()),
		"session.json":    `{"version":2,"terminals":[]}`,
		"daemon.log":      strings.Repeat("old log record\n", 30000) + "last evidence\n",
	} {
		if err := os.WriteFile(filepath.Join(home, "_daemon", name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := newDaemonDiagnosticCapture(home, daemonDiagnosticOptions{Output: output, TerminalID: "one"})
	var methods []string
	c.call = func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("probe has no deadline")
		}
		methods = append(methods, method)
		switch method {
		case "daemon.status":
			return json.RawMessage(`{"running":true}`), nil
		case "terminal.list":
			return json.RawMessage(`[{"terminal_id":"one"}]`), nil
		case "daemon.snapshot":
			return json.RawMessage(fmt.Sprintf(`{"terminal_runtimes":{"one":{"renderer_pid":%d}}}`, os.Getpid())), nil
		case "terminal.read":
			return nil, errors.New("cached read failed")
		default:
			t.Fatalf("unexpected mutation/probe: %s", method)
			return nil, nil
		}
	}
	c.run = func(context.Context, string, ...string) diagnosticCommandResult {
		return diagnosticCommandResult{stdout: []byte("observed process\n")}
	}
	result, err := c.capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(methods, []string{"daemon.status", "terminal.list", "daemon.snapshot", "terminal.read"}) {
		t.Fatal(methods)
	}
	if result.FailedProbes == 0 || !strings.Contains(result.Summary, "control plane responded but the selected cached terminal read failed") {
		t.Fatalf("missing failed-probe distinction: %+v", result)
	}
	for _, name := range []string{"capture.txt", "daemon-status.json", "terminal-list.json", "daemon-snapshot.json", "terminal-read.err", "terminal-read.exit", "daemon.identity", "session.json", "daemon.log.tail", "renderer-pids.txt", "renderers-ps.txt", "probed-renderer-pid.txt", "summary.md", "probes.json"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil {
			t.Fatal(name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("bundle file %s is not private: %o", name, info.Mode().Perm())
		}
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("bundle directory is not private", err)
	}
	log, err := os.ReadFile(filepath.Join(output, "daemon.log.tail"))
	if err != nil || len(log) > diagnosticLogLimit || !strings.Contains(string(log), "last evidence") {
		t.Fatal("bounded log tail lost evidence", err)
	}
	before, err := os.ReadFile(filepath.Join(output, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newDaemonDiagnosticCapture(home, daemonDiagnosticOptions{Output: output}).capture(context.Background()); err == nil {
		t.Fatal("existing capture overwritten")
	}
	after, err := os.ReadFile(filepath.Join(output, "summary.md"))
	if err != nil || string(before) != string(after) {
		t.Fatal("existing evidence changed", err)
	}
}

func TestDaemonDiagnosticsOfflineCaptureDoesNotSeedOrStartRuntime(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	result, err := newDaemonDiagnosticCapture(home, daemonDiagnosticOptions{}).capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Summary, "control-plane probes failed") {
		t.Fatal(result.Summary)
	}
	for _, name := range []string{"nodes", "configurations", "environments", "_daemon/daemon.pid", "_config/settings.yaml"} {
		if _, err := os.Stat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("capture initialized runtime metadata %s: %v", name, err)
		}
	}
	if !strings.HasPrefix(result.Directory, filepath.Join(home, "_diagnostics")+string(filepath.Separator)) {
		t.Fatal(result.Directory)
	}
	foreign := filepath.Join(home, "unrelated-file")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err == nil {
		t.Fatal("diagnostics caused an unrelated file to be adopted")
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatal("offline capture prevented first run:", err)
	}
	if data, err := os.ReadFile(filepath.Join(result.Directory, "summary.md")); err != nil || string(data) != result.Summary {
		t.Fatal("first run changed evidence", err)
	}
}

func TestDaemonDiagnosticsRejectsSpecialFilesAndBoundsCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, _, err := readDiagnosticFile(path, 1024); err == nil {
		t.Fatal("FIFO accepted as evidence file")
	}
	if time.Since(started) > time.Second {
		t.Fatal("FIFO read blocked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	got := runDiagnosticCommand(ctx, "/bin/sh", "-c", "printf ready; sleep 30")
	if got.err == nil || string(got.stdout) != "ready" || time.Since(started) > 2*time.Second {
		t.Fatalf("command did not preserve partial evidence and cancel: %+v", got)
	}
	var buffer diagnosticBuffer
	data := []byte(strings.Repeat("x", diagnosticCommandLimit+100))
	n, err := buffer.Write(data)
	if err != nil || n != len(data) || !buffer.truncated || buffer.Len() != diagnosticCommandLimit {
		t.Fatal("command output is not bounded")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	large := runDiagnosticCommand(ctx2, "/bin/sh", "-c", "head -c 2097152 /dev/zero")
	if large.err != nil || !large.truncated || len(large.stdout) != diagnosticCommandLimit {
		t.Fatalf("subprocess copy bypassed the evidence limit: bytes=%d, truncated=%v, error=%v", len(large.stdout), large.truncated, large.err)
	}
}

func TestDaemonDiagnosticsCancellationKeepsPartialBundle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newDaemonDiagnosticCapture(filepath.Join(t.TempDir(), "home"), daemonDiagnosticOptions{})
	c.run = func(context.Context, string, ...string) diagnosticCommandResult { return diagnosticCommandResult{} }
	c.call = func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == "daemon.status" {
			cancel()
		}
		return nil, ctx.Err()
	}
	result, err := c.capture(ctx)
	if !errors.Is(err, context.Canceled) || result.Directory == "" || result.FailedProbes == 0 {
		t.Fatalf("lost cancellation or evidence location: %+v / %v", result, err)
	}
	for _, file := range []string{"summary.md", "probes.json", "daemon-status.err", "terminal-list.err", "daemon-snapshot.err"} {
		if _, err := os.Stat(filepath.Join(result.Directory, file)); err != nil {
			t.Fatal(err)
		}
	}
}
