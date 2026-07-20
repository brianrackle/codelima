package codelima

// Daemon process lifecycle management: shutdown gating on the daemon lock,
// identity-verified termination escalation, and the update-only
// compatibility handshake. This is orchestration logic, deliberately kept out
// of the CLI dispatch layer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

func isUnsupportedLegacyHandoffTransport(err error) bool {
	if err == nil {
		return false
	}
	// Daemons that tag the condition explicitly are authoritative either way.
	var rpcErr *daemon.RPCError
	if errors.As(err, &rpcErr) {
		if unsupported, ok := rpcErr.Fields["handoff_transport_unsupported"].(bool); ok {
			return unsupported
		}
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		if unsupported, ok := appErr.Fields["handoff_transport_unsupported"].(bool); ok {
			return unsupported
		}
	}
	// Legacy fallback: daemons from before the typed field (protocol <= 3 at
	// the time this shipped) surface the Darwin unixpacket failure only as
	// free text, so match the exact phrasing those binaries emit.
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unixpacket") && strings.Contains(message, "protocol not supported")
}

func restartAfterUnsupportedLegacyHandoff(ctx context.Context, service *Service, client *daemonclient.Client) (map[string]any, error) {
	identity, _ := readDaemonIdentity(service.cfg.MetadataRoot)
	var status daemon.Status
	_ = client.Call(ctx, "daemon.status", nil, &status)
	if identity.PID <= 0 && status.PID > 0 {
		identity = daemon.Identity{
			Token: status.Identity, Version: status.Version, Protocol: status.Protocol, PID: status.PID, StartedAt: status.StartedAt,
		}
	}
	var stopped map[string]bool
	if err := client.Call(ctx, "daemon.stop", nil, &stopped); err != nil {
		return nil, fromDaemonError(err)
	}
	_ = client.Close()
	if err := finishDaemonShutdown(ctx, service.cfg.MetadataRoot, identity, daemonShutdownTimeout(status.TerminalCount)); err != nil {
		return nil, dependencyUnavailable("legacy daemon did not stop after unsupported live-update transport", err, nil)
	}
	status, err := startDaemon(ctx, service)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"updated":      true,
		"live_handoff": false,
		"fallback":     "restart",
		"pid":          status.PID,
	}, nil
}

var errDaemonShutdownTimeout = errors.New("daemon shutdown timed out")

const (
	daemonShutdownBaseTimeout  = 15 * time.Second
	daemonShutdownPerTerminal  = 3 * time.Second
	daemonShutdownMaximum      = 2 * time.Minute
	daemonTerminationGrace     = 2 * time.Second
	daemonStartRecoveryTimeout = 15 * time.Second
)

func daemonShutdownTimeout(terminalCount int) time.Duration {
	if terminalCount < 0 {
		terminalCount = 0
	}
	timeout := daemonShutdownBaseTimeout + time.Duration(terminalCount)*daemonShutdownPerTerminal
	if timeout > daemonShutdownMaximum {
		return daemonShutdownMaximum
	}
	return timeout
}

func finishDaemonShutdown(ctx context.Context, home string, identity daemon.Identity, timeout time.Duration) error {
	err := waitForDaemonShutdown(ctx, home, timeout)
	if err == nil || !errors.Is(err, errDaemonShutdownTimeout) {
		return err
	}
	if !recoverableDaemonIdentity(identity) {
		return err
	}
	if terminateErr := terminateMatchingDaemon(home, identity); terminateErr != nil {
		return errors.Join(err, terminateErr)
	}
	return waitForDaemonShutdown(ctx, home, daemonTerminationGrace)
}

func terminateMatchingDaemon(home string, expected daemon.Identity) error {
	current, err := readDaemonIdentity(home)
	if err != nil {
		return fmt.Errorf("verify daemon identity before termination: %w", err)
	}
	if current.Token != expected.Token || current.PID != expected.PID {
		return fmt.Errorf("daemon identity changed before termination")
	}
	available, err := daemonLockAvailable(daemon.HomePaths(home).Lock)
	if err != nil {
		return err
	}
	if available {
		return nil
	}
	if err := syscall.Kill(expected.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate stuck daemon pid %d: %w", expected.PID, err)
	}
	terminationCtx, cancel := context.WithTimeout(context.Background(), daemonTerminationGrace)
	defer cancel()
	if err := waitForDaemonShutdown(terminationCtx, home, daemonTerminationGrace); err == nil {
		return nil
	}
	current, err = readDaemonIdentity(home)
	if err != nil || current.Token != expected.Token || current.PID != expected.PID {
		return fmt.Errorf("daemon identity changed while waiting for termination")
	}
	if err := syscall.Kill(expected.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill stuck daemon pid %d: %w", expected.PID, err)
	}
	return nil
}

func readDaemonIdentity(home string) (daemon.Identity, error) {
	data, err := os.ReadFile(daemon.HomePaths(home).Identity)
	if err != nil {
		return daemon.Identity{}, err
	}
	var identity daemon.Identity
	if err := json.Unmarshal(data, &identity); err != nil {
		return daemon.Identity{}, err
	}
	return identity, nil
}

func recoverableDaemonIdentity(identity daemon.Identity) bool {
	return identity.Token != "" && identity.Version != "" && identity.PID > 0 &&
		identity.Protocol >= daemon.ProtocolCompatFloor && identity.Protocol <= daemon.ProtocolVersion
}

func waitForDaemonShutdown(ctx context.Context, home string, timeout time.Duration) error {
	paths := daemon.HomePaths(home)
	deadline := time.Now().Add(timeout)
	for {
		available, err := daemonLockAvailable(paths.Lock)
		if err != nil {
			return err
		}
		if available {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w waiting for daemon lock release", errDaemonShutdownTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func daemonLockAvailable(path string) (bool, error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if os.IsNotExist(err) {
		// A missing lock directory means no prepared daemon can own the lock.
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("open daemon lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return false, fmt.Errorf("probe daemon lock: %w", err)
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return true, nil
}

func daemonUpdateBinaryPath(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return resolveCodelimaExecutablePath(absolute), nil
}

// dialDaemonUpdateClient is the update half of the daemon lifecycle
// compatibility boundary. It authenticates using the running daemon's
// persisted identity so daemon.update can ask that process to exec the caller's
// new binary. Startup recovery may use the same identity only to stop the old
// owner; ordinary commands retain the exact current-version handshake.
func dialDaemonUpdateClient(ctx context.Context, service *Service) (*daemonclient.Client, error) {
	options := daemonclient.Options{
		Home: service.cfg.MetadataRoot, Version: Version, WantInput: true, Timeout: 45 * time.Second,
	}
	usingPersistedIdentity := false
	identityPath := daemon.HomePaths(service.cfg.MetadataRoot).Identity
	if data, err := os.ReadFile(identityPath); err == nil {
		var identity daemon.Identity
		if json.Unmarshal(data, &identity) == nil &&
			identity.Version != "" &&
			identity.Protocol >= daemon.ProtocolCompatFloor &&
			identity.Protocol <= daemon.ProtocolVersion {
			options.Version = identity.Version
			options.Protocol = identity.Protocol
			usingPersistedIdentity = identity.Version != Version || identity.Protocol != daemon.ProtocolVersion
		}
	}
	client, err := daemonclient.Dial(ctx, options)
	if err != nil && usingPersistedIdentity {
		client, err = daemonclient.Dial(ctx, daemonclient.Options{
			Home: service.cfg.MetadataRoot, Version: Version, WantInput: true, Timeout: 45 * time.Second,
		})
	}
	if err != nil {
		return nil, err
	}
	if !client.Hello.InputOwner {
		if err := takeTUIDaemonInput(ctx, client); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	return client, nil
}
