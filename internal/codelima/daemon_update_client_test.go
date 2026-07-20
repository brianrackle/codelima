package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
)

func TestDaemonUpdateDefaultsToCallerBinaryAndBridgesPreviousProtocol(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	home, err := os.MkdirTemp("../../tmp", "du-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	service.cfg.MetadataRoot = home
	paths := daemon.HomePaths(service.cfg.MetadataRoot)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	identity := daemon.Identity{
		Token:    "legacy-daemon",
		Version:  "legacy-dev",
		Protocol: daemon.ProtocolVersion - 1,
		PID:      os.Getpid(),
	}
	identityData, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Identity, identityData, 0o600); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	type observedUpdate struct {
		hello   daemon.HelloParams
		methods []string
		path    string
	}
	observed := make(chan observedUpdate, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		decoder, encoder := json.NewDecoder(connection), json.NewEncoder(connection)
		var helloRequest daemon.Request
		if decoder.Decode(&helloRequest) != nil {
			return
		}
		var hello daemon.HelloParams
		if json.Unmarshal(helloRequest.Params, &hello) != nil {
			return
		}
		helloResult, _ := json.Marshal(daemon.HelloResult{
			Version: identity.Version, Protocol: identity.Protocol, ClientID: "update-client", InputOwner: false,
		})
		if encoder.Encode(daemon.Response{ID: helloRequest.ID, Result: helloResult}) != nil {
			return
		}

		var request daemon.Request
		if decoder.Decode(&request) != nil {
			return
		}
		methods := []string{request.Method}
		if request.Method == "input.takeover" {
			takeoverResult, _ := json.Marshal(map[string]bool{"input_owner": true})
			if encoder.Encode(daemon.Response{ID: request.ID, Result: takeoverResult}) != nil {
				return
			}
			if decoder.Decode(&request) != nil {
				return
			}
			methods = append(methods, request.Method)
		}
		var params map[string]string
		if json.Unmarshal(request.Params, &params) != nil {
			return
		}
		observed <- observedUpdate{hello: hello, methods: methods, path: params["path"]}
		result, _ := json.Marshal(map[string]bool{"updated": true})
		_ = encoder.Encode(daemon.Response{ID: request.ID, Result: result})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := dispatchDaemon(ctx, service, []string{"update"}); err != nil {
		t.Fatalf("dispatchDaemon(update) error = %v", err)
	}

	var got observedUpdate
	select {
	case got = <-observed:
	case <-ctx.Done():
		t.Fatal("timed out waiting for daemon update request")
	}
	if got.hello.Version != identity.Version || got.hello.Protocol != identity.Protocol {
		t.Fatalf("update hello = version %q protocol %d, want legacy version %q protocol %d", got.hello.Version, got.hello.Protocol, identity.Version, identity.Protocol)
	}
	wantMethods := []string{"input.takeover", "daemon.update"}
	if !slices.Equal(got.methods, wantMethods) {
		t.Fatalf("update methods = %v, want %v", got.methods, wantMethods)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantPath := resolveCodelimaExecutablePath(executable)
	if got.path != wantPath {
		t.Fatalf("daemon update path = %q, want caller executable %q", got.path, wantPath)
	}
	if !filepath.IsAbs(got.path) {
		t.Fatalf("daemon update path = %q, want absolute path", got.path)
	}
}

func TestDaemonHostUpdateRejectsMissingCallerBinaryPath(t *testing.T) {
	t.Parallel()

	_, err := (&daemonHost{}).update("")
	var rpcErr *daemon.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("update without path error = %T %v, want daemon RPC error", err, err)
	}
	if rpcErr.Category != "InvalidArgument" || !strings.Contains(rpcErr.Message, "caller binary path") {
		t.Fatalf("update without path error = %#v", rpcErr)
	}
}

func TestWaitForDaemonShutdownUsesLockInsteadOfStaleEndpointFiles(t *testing.T) {
	t.Parallel()

	home, err := os.MkdirTemp("../../tmp", "daemon-shutdown-stale-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	paths := daemon.HomePaths(home)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Socket, paths.Client, paths.PID, paths.Identity} {
		if err := os.WriteFile(path, []byte("stale\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := waitForDaemonShutdown(context.Background(), home, 100*time.Millisecond); err != nil {
		t.Fatalf("waitForDaemonShutdown() rejected an available lock because endpoint files remained: %v", err)
	}
}

func TestWaitForDaemonShutdownWaitsForLockRelease(t *testing.T) {
	t.Parallel()

	home, err := os.MkdirTemp("../../tmp", "daemon-shutdown-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	paths := daemon.HomePaths(home)
	if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(75 * time.Millisecond)
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		close(released)
	}()

	started := time.Now()
	if err := waitForDaemonShutdown(context.Background(), home, time.Second); err != nil {
		t.Fatalf("waitForDaemonShutdown() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("waitForDaemonShutdown() returned before lock release after %v", elapsed)
	}
	<-released
}

func TestFinishDaemonShutdownTerminatesMatchingStuckDaemon(t *testing.T) {
	home, err := os.MkdirTemp("../../tmp", "daemon-shutdown-stuck-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	command, paths := startDaemonShutdownHelper(t, home)
	identity := daemon.Identity{Token: "stuck-daemon", Version: "legacy", Protocol: 2, PID: command.Process.Pid}
	data, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Identity, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := finishDaemonShutdown(context.Background(), home, identity, 25*time.Millisecond); err != nil {
		t.Fatalf("finishDaemonShutdown() error = %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("stuck daemon helper exited successfully instead of by signal")
	}
}

func TestFinishDaemonShutdownDoesNotSignalChangedIdentity(t *testing.T) {
	home, err := os.MkdirTemp("../../tmp", "daemon-shutdown-changed-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	command, paths := startDaemonShutdownHelper(t, home)
	expected := daemon.Identity{Token: "expected-daemon", Version: "legacy", Protocol: 2, PID: command.Process.Pid}
	current := expected
	current.Token = "different-daemon"
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Identity, data, 0o600); err != nil {
		t.Fatal(err)
	}

	err = finishDaemonShutdown(context.Background(), home, expected, 25*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("finishDaemonShutdown() error = %v, want identity-change refusal", err)
	}
	if err := syscall.Kill(command.Process.Pid, 0); err != nil {
		t.Fatalf("identity-mismatched process was signaled: %v", err)
	}
}

func startDaemonShutdownHelper(t *testing.T, home string) (*exec.Cmd, daemon.Paths) {
	t.Helper()
	paths := daemon.HomePaths(home)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(home, "ready")
	command := exec.Command(os.Args[0], "-test.run=^TestDaemonShutdownProcessHelper$")
	command.Env = append(os.Environ(),
		"CODELIMA_TEST_DAEMON_LOCK="+paths.Lock,
		"CODELIMA_TEST_DAEMON_READY="+ready,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for lock-holder helper")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return command, paths
}

func TestDaemonShutdownProcessHelper(t *testing.T) {
	lockPath := os.Getenv("CODELIMA_TEST_DAEMON_LOCK")
	readyPath := os.Getenv("CODELIMA_TEST_DAEMON_READY")
	if lockPath == "" || readyPath == "" {
		return
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {}
}
