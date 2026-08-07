package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The observer cache synthesizes entries from `limactl watch` events, which
// carry no SSH config path. Failing there would leave a plainly running node
// unroutable until the next full cache resync, which only happens when the
// watcher itself dies.
func TestForwardingSSHConfigFallsBackToDirectListWhenCacheLacksSSHConfig(t *testing.T) {
	t.Parallel()
	home, configPath, identity := newForwardingSSHConfigFixture(t)
	client := NewLimaClient(t.TempDir())
	client.LimaHome = home
	client.Binary = writeLimaListStub(t, limaListRecordJSON("demo", "Running", configPath, home))
	seedLimaObservationCache(t, client, RuntimeObservation{Name: "demo", Exists: true, Status: ObservationRunning})

	got, err := client.ForwardingSSHConfig(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ForwardingSSHConfig() error = %v", err)
	}
	if got.User != "lima" || got.Host != "127.0.0.1" || got.Port != 60022 || got.IdentityFile != identity {
		t.Fatalf("ForwardingSSHConfig() = %#v", got)
	}
}

// A failed initial list leaves the cache empty but authoritative. Every running
// node would otherwise be treated as absent.
func TestForwardingSSHConfigFallsBackWhenCacheIsEmptyButAuthoritative(t *testing.T) {
	t.Parallel()
	home, configPath, identity := newForwardingSSHConfigFixture(t)
	client := NewLimaClient(t.TempDir())
	client.LimaHome = home
	client.Binary = writeLimaListStub(t, limaListRecordJSON("demo", "Running", configPath, home))
	seedLimaObservationCache(t, client)

	got, err := client.ForwardingSSHConfig(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ForwardingSSHConfig() error = %v", err)
	}
	if got.IdentityFile != identity {
		t.Fatalf("ForwardingSSHConfig() = %#v", got)
	}
}

// The direct list is also what makes "not running" trustworthy: only limactl
// itself, never a cache entry, may report that.
func TestForwardingSSHConfigReportsNotRunningOnlyAfterDirectConfirmation(t *testing.T) {
	t.Parallel()
	home, configPath, _ := newForwardingSSHConfigFixture(t)
	client := NewLimaClient(t.TempDir())
	client.LimaHome = home
	client.Binary = writeLimaListStub(t, limaListRecordJSON("demo", "Stopped", configPath, home))
	seedLimaObservationCache(t, client, RuntimeObservation{Name: "demo", Exists: true, Status: ObservationStopped})

	_, err := client.ForwardingSSHConfig(context.Background(), "demo")
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Category != CategoryPreconditionFailed {
		t.Fatalf("ForwardingSSHConfig() error = %v, want a precondition failure", err)
	}
}

func newForwardingSSHConfigFixture(t *testing.T) (home, configPath, identity string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPath, identity, _ = writeTestLimaSSHConfig(t, home, "demo")
	return home, configPath, identity
}

func limaListRecordJSON(name, status, configPath, limaHome string) string {
	return fmt.Sprintf(`{"name":%q,"status":%q,"dir":%q,"sshConfigFile":%q,"LimaHome":%q,"limaVersion":"2.1.0"}`,
		name, status, filepath.Join(limaHome, name), configPath, limaHome)
}

// writeLimaListStub installs a limactl stand-in that answers `list --json` with
// one fixed record, so the direct-list fallback can be exercised without Lima.
func writeLimaListStub(t *testing.T, record string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "limactl-list-stub")
	script := "#!/bin/sh\nset -eu\nif [ \"${1:-}\" = list ]; then\n  cat <<'RECORD'\n" + record + "\nRECORD\nfi\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
