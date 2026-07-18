package codelima

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
)

func TestDaemonRestoreSessionQuarantinesUnsupportedVersion(t *testing.T) {
	host, sessionPath, logs := newDaemonSessionTestHost(t, "respawn")
	original := []byte("{\"version\":1,\"terminals\":[]}\n")
	if err := os.WriteFile(sessionPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := host.restoreSession(context.Background()); err != nil {
		t.Fatalf("restoreSession() error = %v", err)
	}

	assertFreshDaemonSession(t, sessionPath)
	backups, err := filepath.Glob(sessionPath + ".unsupported-v1-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("unsupported session backups = %v, want exactly one", backups)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("quarantined session = %q, want %q", backup, original)
	}
	if text := logs.String(); !strings.Contains(text, "quarantined incompatible daemon session") || !strings.Contains(text, "version=1") {
		t.Fatalf("recovery log = %q", text)
	}
}

func TestDaemonRestoreSessionQuarantinesMalformedJSON(t *testing.T) {
	host, sessionPath, logs := newDaemonSessionTestHost(t, "respawn")
	original := []byte("not-json\n")
	if err := os.WriteFile(sessionPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := host.restoreSession(context.Background()); err != nil {
		t.Fatalf("restoreSession() error = %v", err)
	}

	assertFreshDaemonSession(t, sessionPath)
	backups, err := filepath.Glob(sessionPath + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("corrupt session backups = %v, want exactly one", backups)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("quarantined session = %q, want %q", backup, original)
	}
	if text := logs.String(); !strings.Contains(text, "quarantined corrupt daemon session") {
		t.Fatalf("recovery log = %q", text)
	}
}

func TestDaemonRestoreForgetReplacesSessionWithoutParsing(t *testing.T) {
	host, sessionPath, logs := newDaemonSessionTestHost(t, "forget")
	if err := os.WriteFile(sessionPath, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := host.restoreSession(context.Background()); err != nil {
		t.Fatalf("restoreSession() error = %v", err)
	}

	assertFreshDaemonSession(t, sessionPath)
	backups, err := filepath.Glob(sessionPath + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("restore=forget created backups: %v", backups)
	}
	if logs.Len() != 0 {
		t.Fatalf("restore=forget logged unexpected recovery: %q", logs.String())
	}
}

func newDaemonSessionTestHost(t *testing.T, restore string) (*daemonHost, string, *bytes.Buffer) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cwd, "..", "..", "tmp", "daemon-session-"+newID()[:8])
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	logs := &bytes.Buffer{}
	service := &Service{
		now:    func() time.Time { return time.Date(2026, time.July, 17, 20, 0, 0, 0, time.UTC) },
		logger: newTextLogger(logs, parseLogLevel("warn")),
	}
	sessionPath := filepath.Join(root, "session.json")
	host := &daemonHost{
		service:   service,
		restore:   restore,
		session:   sessionPath,
		terminals: map[string]*daemonTerminalEntry{},
		broadcast: func(string, any) {},
	}
	return host, sessionPath, logs
}

func assertFreshDaemonSession(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var session daemon.Session
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatalf("decode fresh session: %v", err)
	}
	if session.Version != daemon.SessionVersion || len(session.Terminals) != 0 {
		t.Fatalf("fresh session = %#v, want version %d with no terminals", session, daemon.SessionVersion)
	}
}
