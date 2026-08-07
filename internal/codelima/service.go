package codelima

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type Service struct {
	cfg            Config
	store          *Store
	sandbox        SandboxClient
	tui            TUIRunner
	stdin          io.Reader
	stdout         io.Writer
	stderr         io.Writer
	now            func() time.Time
	ready          *serviceReadiness
	logger         *slog.Logger
	logLevel       slog.Level
	daemonClient   *daemonclient.Client
	localTerminals bool
}

// serviceReadiness caches the once-per-instance directory bootstrap for read
// paths. It sits behind a pointer so withIO's shallow clone shares it and so
// Service stays copyable without embedding a sync.Once by value.
type serviceReadiness struct {
	directoriesOnce sync.Once
	directoriesErr  error
}

type NodeCreateInput struct {
	Configuration string
	Directory     string
	Slug          string
	Runtime       string
	Provider      string
	WorkspaceMode string
	Ports         []string
}

type NodeCloneInput struct {
	SourceNode string
	NodeSlug   string
}

func NewService(cfg Config, sandbox SandboxClient, stdin io.Reader, stdout, stderr io.Writer) *Service {
	if sandbox == nil {
		sandbox = NewLimaClient(cfg.MetadataRoot)
	}
	if lima, ok := sandbox.(*LimaClient); ok {
		lima.RuntimeCommands = cfg.RuntimeCommands.ApplyDefaults(defaultRuntimeCommandTemplates())
		lima.Stdout = stdout
		lima.Stderr = stderr
	}

	return &Service{
		cfg:     cfg,
		store:   NewStore(cfg),
		sandbox: sandbox,
		tui:     newTUIRunner(),
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		now: func() time.Time {
			return time.Now().UTC()
		},
		ready:    &serviceReadiness{},
		logger:   discardLogger(),
		logLevel: slog.LevelInfo,
	}
}

// SetLogger installs the process logger and records its level. Run wires the
// CLI-mode text sink here; enableFileLogging later swaps in the TUI file sink at
// the same level. The field is a plain pointer, so withIO's shallow clone shares
// it (mirroring the ready pointer-field pattern from work item 0.3).
func (s *Service) SetLogger(logger *slog.Logger, level slog.Level) {
	if s == nil {
		return
	}
	if logger == nil {
		logger = discardLogger()
	}
	s.logger = logger
	s.logLevel = level
	// Store warns about records its read paths skip; without this it would fall
	// back to the package sink, which is discard outside the TUI.
	s.store.SetLogger(logger)
}

// log returns the Service logger, falling back to a discard logger so seam log
// calls are always safe even on a Service built without SetLogger (tests).
func (s *Service) log() *slog.Logger {
	if s == nil || s.logger == nil {
		return discardLogger()
	}
	return s.logger
}

// enableFileLogging switches the Service logger, process-global libghostty
// capture logger, and Lima subprocess diagnostics to the TUI file sink at the
// configured level. The TUI runner calls it once at startup and defers the
// returned closer.
func (s *Service) enableFileLogging() (func() error, error) {
	logger, closeLog, err := newTUIFileLogger(s.cfg.MetadataRoot, s.logLevel)
	if err != nil {
		return nil, err
	}
	s.logger = logger
	s.store.SetLogger(logger)
	setPackageLogger(logger)
	if lima, ok := s.sandbox.(*LimaClient); ok {
		lima.Stderr = newDiagnosticLogWriter(logger, "limactl")
	}
	return closeLog, nil
}

// logOperation is the deferred half of service-operation seam logging: it emits
// a start record at call time (via logOperationStart) and a finish record here,
// carrying the operation name, duration, and terminal error. Errors log at error
// level; clean finishes at debug so default (info) CLI runs stay quiet.
func (s *Service) logOperation(op string, start time.Time, errp *error) {
	logger := s.log()
	duration := s.now().Sub(start)
	var err error
	if errp != nil {
		err = *errp
	}
	if err != nil {
		logger.Error("operation failed", "op", op, "duration", duration.String(), "error", err.Error())
		return
	}
	logger.Debug("operation finished", "op", op, "duration", duration.String())
}

// logRuntime records a runtime (sandbox) invocation at a service call site: the verb
// only, never the full argv (argv can carry host paths). Debug level keeps it out
// of default CLI output.
func (s *Service) logRuntime(verb, nodeID string) {
	s.log().Debug("sandbox invocation", "verb", verb, "node", nodeID)
}

// recordNodeStartRollback persists the failed-start rollback state and logs any
// error from those writes at error level instead of discarding it (absorbed work
// item 0.7.2). Behaviour is otherwise unchanged: the caller still returns the
// original start failure. The write takes the node's metadata lock on a fresh
// context, because the caller's is frequently the reason the start failed; if
// even that cannot be acquired the node stays in its in-flight status, which the
// next operation (or doctor --repair) recovers as a stale claim.
func (s *Service) recordNodeStartRollback(node Node, bootstrap BootstrapState, event Event) {
	if err := s.withRollbackLocks(nil, []string{node.ID}, func() error {
		if saveErr := s.store.SaveNode(node, bootstrap); saveErr != nil {
			s.log().Error("node start rollback save failed", "node", node.ID, "error", saveErr.Error())
		}
		if eventErr := s.store.AppendNodeEvent(node.ID, event); eventErr != nil {
			s.log().Error("node start rollback event append failed", "node", node.ID, "error", eventErr.Error())
		}
		return nil
	}); err != nil {
		s.log().Error("node start rollback lock failed", "node", node.ID, "error", err.Error())
	}
}

// restoreNodeStatus leaves the in-flight window without recording a failure. It
// covers the rejections that happen before the runtime is touched at all (an
// unreadable runtime listing, a host port reserved by another node): those never
// marked a node failed, and leaving it provisioning would strand it.
func (s *Service) restoreNodeStatus(nodeID string, status NodeStatus) {
	if err := s.withRollbackLocks(nil, []string{nodeID}, func() error {
		node, err := s.store.NodeByID(nodeID)
		if err != nil {
			return err
		}
		if !nodeStatusInFlight(node.Status) {
			return nil
		}
		bootstrap, err := s.store.LoadBootstrapState(nodeID)
		if err != nil {
			return err
		}
		node.Status = status
		node.UpdatedAt = s.now()
		return s.store.SaveNode(node, bootstrap)
	}); err != nil {
		s.log().Error("node status restore failed", "node", nodeID, "error", err.Error())
	}
}

func (s *Service) withIO(stdout, stderr io.Writer) *Service {
	if s == nil {
		return nil
	}

	cloned := *s
	cloned.stdout = stdout
	cloned.stderr = stderr

	if lima, ok := s.sandbox.(*LimaClient); ok {
		cloned.sandbox = lima.withIO(stdout, stderr)
	}

	return &cloned
}

func (s *Service) TUI(ctx context.Context, workspaceRoot string) error {
	// A user-initiated app launch is a session start, not a background read:
	// run the one-time locked seed/repair pass here so a fresh home shows the
	// built-in agent profiles and environments in the TUI's pickers.
	// This is idempotent, flock-guarded, and once per process — categorically
	// different from the per-tick unlocked writes work item 0.3 removed (ADR
	// 57). The one-second auto-refresh path stays a pure read, and no runtime
	// dependencies are validated: launching the TUI must work without Lima.
	if err := s.ensureReadyForWrite(ctx); err != nil {
		return err
	}
	if !s.localTerminals {
		client, err := s.connectTUIDaemon(ctx)
		if err != nil {
			return err
		}
		s.daemonClient = client
		defer func() {
			_ = client.Close()
			s.daemonClient = nil
		}()
	}

	if s.tui == nil {
		s.tui = newTUIRunner()
	}

	return s.tui.Run(ctx, s, workspaceRoot)
}

func (s *Service) connectTUIDaemon(ctx context.Context) (*daemonclient.Client, error) {
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: s.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err == nil {
		return claimTUIDaemonInput(ctx, client)
	}
	if !s.cfg.Daemon.Autostart {
		return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
	}
	if _, startErr := startDaemon(ctx, s); startErr != nil {
		return nil, startErr
	}
	client, err = daemonclient.Dial(ctx, daemonclient.Options{Home: s.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err != nil {
		return nil, dependencyUnavailable("daemon did not accept the TUI connection", err, nil)
	}
	return claimTUIDaemonInput(ctx, client)
}

func claimTUIDaemonInput(ctx context.Context, client *daemonclient.Client) (*daemonclient.Client, error) {
	if client.HelloSnapshot().InputOwner {
		return client, nil
	}
	if err := takeTUIDaemonInput(ctx, client); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// takeTUIDaemonInput makes an already-connected interactive TUI authoritative.
// Unlike claimTUIDaemonInput, it always sends the idempotent takeover request:
// Hello describes connection-time ownership and becomes stale when another TUI
// takes over later.
func takeTUIDaemonInput(ctx context.Context, client *daemonclient.Client) error {
	var result map[string]bool
	if err := client.Call(ctx, "input.takeover", nil, &result); err != nil {
		return fromDaemonError(err)
	}
	if !result["input_owner"] {
		return preconditionFailed("daemon did not grant TUI input ownership", map[string]any{"client_id": client.HelloSnapshot().ClientID})
	}
	client.SetInputOwner(true)
	return nil
}

// EnsureReady prepares CODELIMA_HOME for an operation. Read surfaces call it
// with mutating=false: that only creates missing directories (once per Service
// instance) and never writes, seeds, or rewrites files — reads must not write.
// mutating=true additionally seeds and repairs metadata under the
// environments/configurations/nodes locks and validates runtime dependencies.
// Stale metadata (for example an old config.yaml) is therefore upgraded only
// by mutating commands or `codelima doctor --repair`.
func (s *Service) EnsureReady(ctx context.Context, mutating bool) error {
	return s.ensureReady(ctx, mutating)
}

func (s *Service) ensureReady(ctx context.Context, mutating bool) error {
	if !mutating {
		return s.ensureDirectories()
	}

	if err := s.ensureReadyForWrite(ctx); err != nil {
		return err
	}

	return s.validateDependencies(ctx)
}

// ensureReadyForWrite prepares the home for a metadata mutation: directory
// skeleton plus seed/repair under locks, without the runtime-dependency
// validation that EnsureReady(mutating=true) performs. Metadata-only mutations
// (configuration and environment writes) call it directly so they keep
// working without a usable Lima runtime.
func (s *Service) ensureReadyForWrite(ctx context.Context) error {
	if err := s.ensureDirectories(); err != nil {
		return err
	}

	return s.seedAndRepair(ctx, false)
}

func (s *Service) ensureDirectories() error {
	s.ready.directoriesOnce.Do(func() {
		s.ready.directoriesErr = s.store.ensureDirectories()
	})

	return s.ready.directoriesErr
}

// withLocks runs fn while holding the requested global lock domains and
// per-node metadata locks, taken in one call so the documented lock order in
// locks.go is enforced by the lock layer rather than by each call site. fn is
// the metadata read-modify-write phase of an operation and nothing else: the
// runtime work of a lifecycle operation runs between critical sections, not
// inside one.
func (s *Service) withLocks(ctx context.Context, keys []lockKey, nodeIDs []string, fn func() error) error {
	lockSet, err := acquireLockSet(ctx, s.cfg.MetadataRoot, s.log(), keys, nodeIDs)
	if err != nil {
		return err
	}
	defer lockSet.release()

	return fn()
}

// rollbackLockTimeout bounds a rollback's wait for its metadata lock. Rollback
// writes are a handful of small files, so exceeding this means a holder is
// wedged rather than busy, and the durable in-flight status plus stale-claim
// recovery is the safety net that remains.
const rollbackLockTimeout = 30 * time.Second

// withRollbackLocks runs a rollback metadata write on a fresh context. The
// caller's context is frequently the reason we are rolling back (Ctrl+C), and
// an already-cancelled context would fail the acquisition and skip the write
// (same discipline as the runtime rollbacks, which build their own contexts).
func (s *Service) withRollbackLocks(keys []lockKey, nodeIDs []string, fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackLockTimeout)
	defer cancel()

	return s.withLocks(ctx, keys, nodeIDs, fn)
}

// seedAndRepair seeds built-in metadata and repairs stale files. The seed
// version stamp is checked BEFORE any lock is taken: every mutating command and
// every TUI/daemon startup runs this pass, and on an already-seeded home (the
// overwhelmingly common case) it must not queue behind an unrelated operation.
// Only a home that actually needs work takes the environments, configurations,
// and nodes locks, re-checks the stamp under them, and then takes the metadata
// lock of every node whose file the pass may rewrite. Holding the locks is what
// keeps concurrent seeding from duplicating built-in environments (TODO #20).
func (s *Service) seedAndRepair(ctx context.Context, force bool) error {
	if !force && s.store.seedVersion() == seedRevision {
		return nil
	}

	globalLocks, err := acquireLockSet(ctx, s.cfg.MetadataRoot, s.log(), []lockKey{lockEnvironments, lockConfigurations, lockNodes}, nil)
	if err != nil {
		return err
	}
	defer globalLocks.release()

	if !force && s.store.seedVersion() == seedRevision {
		return nil
	}

	// The node set is stable now that the global nodes lock is held, so the
	// enumeration cannot miss a node that appears before the per-node locks are
	// taken. This is the one permitted global-then-per-node nesting.
	nodeIDs, err := s.nodeMetadataIDs()
	if err != nil {
		return err
	}
	nodeLocks, err := acquireLockSet(ctx, s.cfg.MetadataRoot, s.log(), nil, nodeIDs)
	if err != nil {
		return err
	}
	defer nodeLocks.release()

	return s.store.seedAndRepair(s.now(), force)
}

// nodeMetadataIDs lists the node ids that currently own a metadata directory.
// seedAndRepair may rewrite each of their node.yaml files, so it locks them all
// for the pass rather than racing whatever lifecycle operation owns one.
func (s *Service) nodeMetadataIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.MetadataRoot, "nodes"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return ids, nil
}

// nodeStatusInFlight reports whether a persisted status means "a lifecycle
// operation owns this node". These are exactly the statuses a crashed process
// can strand, because they are written before the runtime work starts and
// replaced only after it finishes.
func nodeStatusInFlight(status NodeStatus) bool {
	switch status {
	case NodeStatusProvisioning, NodeStatusRegistering, NodeStatusTerminating:
		return true
	default:
		return false
	}
}

// claimNodeOperation takes the node's lifecycle liveness token or fails fast. A
// node admits one lifecycle operation at a time; now that the runtime work runs
// outside every flock, a second caller must be told so rather than queued
// invisibly behind a VM boot.
func (s *Service) claimNodeOperation(op, nodeID string) (*nodeOperationToken, error) {
	token, ok, err := tryAcquireNodeOperation(s.cfg.MetadataRoot, nodeID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, preconditionFailed("another lifecycle operation is already running on this node", map[string]any{
			"node_id": nodeID, "op": op,
		})
	}
	return token, nil
}

// recoverStaleNodeClaim repairs an in-flight status left behind by a process
// that died mid-operation. The caller already holds the node's liveness token
// and the node's metadata lock, so no live owner can exist: an in-flight status
// here is stale by construction. The crash is recorded as a failure before the
// new operation re-enters the in-flight window, so it stays visible in
// events.jsonl instead of being silently overwritten.
func (s *Service) recoverStaleNodeClaim(op string, node *Node, bootstrap BootstrapState) error {
	if node == nil || !nodeStatusInFlight(node.Status) {
		return nil
	}

	previous := node.Status
	s.log().Warn("recovering stale node lifecycle claim", "node", node.ID, "previous_status", string(previous), "op", op)
	node.Status = NodeStatusFailed
	node.UpdatedAt = s.now()
	if err := s.store.SaveNode(*node, bootstrap); err != nil {
		return err
	}

	return s.store.AppendNodeEvent(node.ID, Event{
		Timestamp: node.UpdatedAt,
		Type:      "node.lifecycle.recovered",
		Fields:    map[string]any{"previous_status": string(previous), "op": op},
	})
}

// staleNodeClaims lists nodes whose persisted status says an operation is in
// flight while no live process holds their token. It never writes, so doctor's
// read-only report can use it.
func (s *Service) staleNodeClaims() ([]Node, error) {
	nodes, err := s.store.ListNodes(true)
	if err != nil {
		return nil, err
	}

	stale := make([]Node, 0)
	for _, node := range nodes {
		if !nodeStatusInFlight(node.Status) {
			continue
		}
		claimed, err := nodeOperationClaimed(s.cfg.MetadataRoot, node.ID)
		if err != nil {
			return nil, err
		}
		if !claimed {
			stale = append(stale, node)
		}
	}
	return stale, nil
}

// repairStaleNodeClaims resets stranded in-flight statuses to failed, the state
// every surface already understands and from which a node can be started again.
// It is the crash-recovery path for a node nobody happens to operate on next.
func (s *Service) repairStaleNodeClaims(ctx context.Context) (int, error) {
	stale, err := s.staleNodeClaims()
	if err != nil {
		return 0, err
	}

	repaired := 0
	for _, node := range stale {
		token, ok, tokenErr := tryAcquireNodeOperation(s.cfg.MetadataRoot, node.ID)
		if tokenErr != nil {
			return repaired, tokenErr
		}
		if !ok {
			// An operation started between the scan and here; it owns the node.
			continue
		}

		repairErr := s.withLocks(ctx, nil, []string{node.ID}, func() error {
			current, loadErr := s.store.NodeByID(node.ID)
			if loadErr != nil {
				return loadErr
			}
			bootstrap, loadErr := s.store.LoadBootstrapState(node.ID)
			if loadErr != nil {
				return loadErr
			}
			if !nodeStatusInFlight(current.Status) {
				return nil
			}
			repaired++
			return s.recoverStaleNodeClaim("doctor.repair", &current, bootstrap)
		})
		token.release()
		if repairErr != nil {
			return repaired, repairErr
		}
	}

	return repaired, nil
}

func (s *Service) validateDependencies(ctx context.Context) error {
	if _, err := exec.LookPath("git"); err != nil {
		return dependencyUnavailable("git is required", err, nil)
	}

	if _, err := s.sandbox.Version(ctx); err != nil {
		return err
	}

	if _, err := s.sandbox.List(ctx); err != nil {
		return err
	}

	return nil
}

// Doctor inspects the home without modifying it. With repair=true it first
// runs the locked seed-and-repair pass (the same one mutating commands run),
// which is the supported way to upgrade stale metadata from a read-only
// workflow.
func (s *Service) Doctor(ctx context.Context, repair bool) (DoctorReport, error) {
	if err := s.ensureDirectories(); err != nil {
		return DoctorReport{}, err
	}

	report := DoctorReport{
		Checks: []DoctorCheck{},
	}

	if repair {
		// Quarantine runs before seeding so the pass that rewrites node files
		// is not asked to reason about records nobody can parse.
		quarantined, err := s.quarantineCorruptNodeRecords(ctx)
		if err != nil {
			return DoctorReport{}, err
		}
		report.Checks = append(report.Checks, DoctorCheck{
			Name:    "corrupt_records",
			Status:  "ok",
			Message: fmt.Sprintf("quarantined %d unreadable node record(s)", len(quarantined)),
		})
		for _, record := range quarantined {
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"unreadable node metadata %s was moved to %s; nothing was deleted",
				record.SourcePath, record.QuarantinePath))
		}

		if err := s.seedAndRepair(ctx, true); err != nil {
			return DoctorReport{}, err
		}
		report.Checks = append(report.Checks, DoctorCheck{Name: "repair", Status: "ok", Message: "seeded built-in metadata and repaired stale files"})

		recovered, err := s.repairStaleNodeClaims(ctx)
		if err != nil {
			return DoctorReport{}, err
		}
		report.Checks = append(report.Checks, DoctorCheck{
			Name:    "node_claims",
			Status:  "ok",
			Message: fmt.Sprintf("recovered %d node(s) stranded in an in-flight status", recovered),
		})
	}

	if err := validateConfig(s.cfg); err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "config", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "config", Status: "ok", Message: "config is valid"})
	}

	if _, err := exec.LookPath("git"); err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "git", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "git", Status: "ok", Message: "git is available"})
	}

	if version, versionErr := s.sandbox.Version(ctx); versionErr != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "lima", Status: "fail", Message: versionErr.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "lima", Status: "ok", Message: fmt.Sprintf("limactl %s is supported", version)})
	}

	observations, err := s.sandbox.List(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "lima_list", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "lima_list", Status: "ok", Message: "Lima instance listing succeeded"})
		orphanWarnings, orphanErr := s.detectOrphans(observations)
		if orphanErr != nil {
			return DoctorReport{}, orphanErr
		}

		report.Warnings = append(report.Warnings, orphanWarnings...)
	}

	if missing, err := s.store.MissingConfigurationIndexes(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, missing...)
	}

	if missing, err := s.store.MissingEnvironmentConfigIndexes(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, missing...)
	}

	if missing, err := s.store.MissingNodeIndexes(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, missing...)
	}

	if incomplete, err := s.store.IncompleteNodeWarnings(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, incomplete...)
	}

	// Every listing skips these rather than failing, so doctor is the surface
	// that tells the user a record exists but is unreadable.
	if corrupt, err := s.store.CorruptNodeWarnings(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, corrupt...)
	}

	// A node left in an in-flight status with nobody holding its lifecycle
	// token is the fingerprint of a process killed mid-operation.
	if stale, err := s.staleNodeClaims(); err != nil {
		return DoctorReport{}, err
	} else {
		for _, node := range stale {
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"node %s is stuck in status %q with no running operation; recover it with `codelima doctor --repair`",
				node.Slug, string(node.Status)))
		}
	}

	if info, err := os.Stat(s.cfg.MetadataRoot); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			report.Warnings = append(report.Warnings, "CODELIMA_HOME permissions are broader than user-private")
		}
	}

	if len(s.cfg.MetadataRoot) > 120 {
		report.Warnings = append(report.Warnings, "CODELIMA_HOME path is long; keep LIMA_HOME short enough for Unix sockets")
	}

	if lima, ok := s.sandbox.(*LimaClient); ok {
		if home, homeErr := lima.resolvedLimaHome(); homeErr != nil {
			report.Checks = append(report.Checks, DoctorCheck{Name: "lima_home", Status: "fail", Message: homeErr.Error()})
		} else {
			report.Checks = append(report.Checks, DoctorCheck{Name: "lima_home", Status: "ok", Message: home})
		}
		goos, _ := lima.platform()
		switch goos {
		case "darwin":
			message := "VZ with VirtioFS; nested virtualization is unavailable"
			if lima.supportsNestedVirtualization() {
				message = "VZ with VirtioFS; nested virtualization is enabled automatically"
			}
			report.Checks = append(report.Checks, DoctorCheck{Name: "lima_driver", Status: "ok", Message: message})
		case "linux":
			kvm, kvmErr := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
			if kvmErr != nil {
				report.Checks = append(report.Checks, DoctorCheck{Name: "lima_driver", Status: "fail", Message: "QEMU requires accessible /dev/kvm: " + kvmErr.Error()})
			} else {
				_ = kvm.Close()
				report.Checks = append(report.Checks, DoctorCheck{Name: "lima_driver", Status: "ok", Message: "QEMU with KVM and 9p"})
			}
		}
	}

	return report, nil
}

// quarantineCorruptNodeRecords is doctor --repair's escape hatch for node
// records nobody can parse. It takes the global nodes lock because the move
// rewrites the by-slug and by-instance indexes, plus the per-node lock of every
// record it touches so the rename cannot race a lifecycle operation writing the
// same directory. The store moves the directory rather than deleting it.
func (s *Service) quarantineCorruptNodeRecords(ctx context.Context) ([]QuarantinedNodeRecord, error) {
	corrupt, err := s.store.CorruptNodeRecords()
	if err != nil {
		return nil, err
	}
	if len(corrupt) == 0 {
		return nil, nil
	}

	nodeIDs := make([]string, 0, len(corrupt))
	for _, record := range corrupt {
		nodeIDs = append(nodeIDs, record.NodeID)
	}

	var quarantined []QuarantinedNodeRecord
	err = s.withLocks(ctx, []lockKey{lockNodes}, nodeIDs, func() error {
		var quarantineErr error
		quarantined, quarantineErr = s.store.QuarantineCorruptNodeRecords(s.now())
		return quarantineErr
	})
	if err != nil {
		return nil, err
	}

	for _, record := range quarantined {
		s.log().Warn("quarantined corrupt node metadata",
			"op", "doctor.repair",
			"node_id", record.NodeID,
			"quarantine", record.QuarantinePath,
			"reason", record.Reason)
	}

	return quarantined, nil
}

func (s *Service) detectOrphans(observations []RuntimeObservation) ([]string, error) {
	warnings := []string{}
	nodes, err := s.store.ListNodes(true)
	if err != nil {
		return nil, err
	}

	nodeByInstance := map[string]Node{}
	for _, node := range nodes {
		nodeByInstance[node.SandboxName] = node
	}

	for _, observation := range observations {
		if _, ok := nodeByInstance[observation.Name]; !ok {
			warnings = append(warnings, "Lima instance without metadata: "+observation.Name)
		}
	}

	for _, node := range nodes {
		if node.Status == NodeStatusTerminated {
			continue
		}

		if _, ok := findObservation(observations, node.SandboxName); !ok {
			warnings = append(warnings, "metadata exists but Lima instance is missing: "+node.SandboxName)
		}
	}

	return warnings, nil
}

func (s *Service) ConfigSummary() map[string]any {
	return s.cfg.Summary()
}

func pathWithinRoot(rootPath, targetPath string) bool {
	rel, err := filepath.Rel(rootPath, filepath.Clean(targetPath))
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (s *Service) NodeCreate(ctx context.Context, input NodeCreateInput) (_ Node, err error) {
	s.log().Debug("operation started", "op", "node.create", "configuration", input.Configuration, "directory", input.Directory)
	defer s.logOperation("node.create", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	// Input parsing and host-directory resolution need no lock at all.
	if strings.TrimSpace(input.Slug) == "" {
		return Node{}, invalidArgument("node slug is required", nil)
	}
	if slugify(input.Slug) != input.Slug {
		return Node{}, invalidArgument("node slug must be a lowercase slug", map[string]any{"slug": input.Slug})
	}
	directoryPath, err := s.resolveNodeDirectoryPath(input.Directory)
	if err != nil {
		return Node{}, err
	}

	runtime := coalesce(input.Runtime, RuntimeVM)
	provider := coalesce(input.Provider, ProviderLima)
	if runtime != RuntimeVM {
		return Node{}, unsupportedFeature("only the vm runtime is supported", map[string]any{"runtime": runtime})
	}

	if provider != ProviderLima {
		return Node{}, unsupportedFeature("only the Lima provider is supported", map[string]any{"provider": provider})
	}

	workspaceMode := normalizeWorkspaceMode(coalesce(input.WorkspaceMode, DefaultWorkspaceMode))
	if workspaceMode == "" {
		return Node{}, invalidArgument("workspace mode must be copy or mounted", map[string]any{"workspace_mode": input.WorkspaceMode})
	}

	ports := resolvePorts(input.Ports, []string{})
	if _, err := validatePorts(ports); err != nil {
		return Node{}, err
	}

	var (
		node      Node
		bootstrap BootstrapState
		token     *nodeOperationToken
	)
	// Naming is the cross-node phase: slug uniqueness and the by-instance name
	// reservation are only meaningful under the global nodes lock. The VM
	// create below deliberately runs outside it.
	if err := s.withLocks(ctx, []lockKey{lockConfigurations, lockNodes}, nil, func() error {
		configurationRef := coalesce(input.Configuration, DefaultConfigurationSlug)
		configuration, err := s.store.ConfigurationByIDOrSlug(configurationRef)
		if err != nil {
			return err
		}

		profileName := configuration.AgentProfileName
		profile, err := s.store.LoadAgentProfile(profileName)
		if err != nil {
			return err
		}

		configurationCommands, err := s.resolveConfigurationBootstrapCommands(configuration)
		if err != nil {
			return err
		}

		nodeSlug := input.Slug
		if err := s.ensureUniqueNodeSlug(nodeSlug); err != nil {
			return err
		}

		sandboxName, err := s.generateSandboxName(nodeSlug)
		if err != nil {
			return err
		}

		bootstrap = BootstrapState{
			AgentProfileName:  profile.Name,
			InstallCommands:   append([]string(nil), profile.InstallCommands...),
			BootstrapCommands: configurationCommands,
			ValidationCommand: profile.ValidationCommand,
			LaunchCommand:     profile.LaunchCommand,
			Environment:       cloneStringMap(profile.Environment),
			Completed:         false,
		}

		workspaceMountPath := ""
		if workspaceMode == WorkspaceModeMounted {
			workspaceMountPath = directoryPath
		}

		node = Node{
			ID:                 newID(),
			Slug:               nodeSlug,
			ConfigurationID:    configuration.ID,
			ConfigurationSlug:  configuration.Slug,
			DirectoryPath:      directoryPath,
			Runtime:            runtime,
			Provider:           provider,
			SandboxName:        sandboxName,
			Image:              configuration.Image,
			VCPUs:              configuration.VCPUs,
			MemoryMiB:          configuration.MemoryMiB,
			DiskMiB:            configuration.DiskMiB,
			Environments:       append([]string(nil), configuration.Environments...),
			Ports:              ports,
			Status:             NodeStatusCreated,
			AgentProfileName:   profileName,
			BootstrapCommands:  bootstrap.CombinedCommands(),
			WorkspaceMode:      workspaceMode,
			GuestWorkspacePath: directoryPath,
			WorkspaceMountPath: workspaceMountPath,
			WorkspaceSeeded:    false,
			BootstrapCompleted: false,
			CreatedAt:          s.now(),
			UpdatedAt:          s.now(),
		}

		// The token is what tells cleanup-incomplete that this reservation
		// belongs to a live create rather than to a crashed one.
		claim, err := s.claimNodeOperation("node.create", node.ID)
		if err != nil {
			return err
		}
		if err := s.store.SaveIncompleteNodeReference(node.ID, node.SandboxName); err != nil {
			claim.release()
			return err
		}
		token = claim
		return nil
	}); err != nil {
		return Node{}, err
	}
	defer token.release()

	cleanupNodeDir := true
	cleanupInstance := false
	defer func() {
		if err == nil {
			return
		}
		if cleanupInstance {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			deleteErr := s.sandbox.Delete(cleanupCtx, node)
			cancel()
			if deleteErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback Lima instance %s: %w", node.SandboxName, deleteErr))
				return
			}
		}
		if cleanupNodeDir {
			removeErr := s.withRollbackLocks([]lockKey{lockNodes}, []string{node.ID}, func() error {
				return s.store.RemoveIncompleteNodeMetadata([]IncompleteNodeMetadata{{
					NodeID: node.ID, DirectoryPath: s.store.nodeDir(node.ID), SandboxName: node.SandboxName,
				}})
			})
			if removeErr != nil {
				err = errors.Join(err, removeErr)
			}
		}
	}()

	// Unlocked: creating the VM is the multi-minute half of this operation.
	s.logRuntime("create", node.ID)
	if createErr := s.sandbox.Create(ctx, node); createErr != nil {
		cleanupInstance = runtimeMutationMayHaveCreated(createErr)
		return Node{}, createErr
	}
	cleanupInstance = true

	if err := s.withLocks(ctx, []lockKey{lockNodes}, []string{node.ID}, func() error {
		if err := s.store.SaveNode(node, bootstrap); err != nil {
			return err
		}
		cleanupNodeDir = false
		cleanupInstance = false
		return s.store.AppendNodeEvent(node.ID, Event{Timestamp: s.now(), Type: "node.created"})
	}); err != nil {
		return Node{}, err
	}

	return node, nil
}

func (s *Service) NodeList(ctx context.Context, includeDeleted bool) ([]Node, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return nil, err
	}

	nodes, err := s.store.ListNodes(includeDeleted)
	if err != nil {
		return nil, err
	}

	nodes, err = s.reconcileNodes(ctx, nodes)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateConfigurationSlugs(nodes); err != nil {
		return nil, err
	}
	sortNodesByDirectory(nodes)
	return nodes, nil
}

func (s *Service) NodeListByDirectoryRoot(ctx context.Context, directoryRoot string, includeDeleted bool) ([]Node, error) {
	nodes, err := s.NodeList(ctx, includeDeleted)
	if err != nil {
		return nil, err
	}
	return filterNodesByDirectoryRoot(nodes, directoryRoot)
}

func filterNodesByDirectoryRoot(nodes []Node, directoryRoot string) ([]Node, error) {
	if strings.TrimSpace(directoryRoot) == "" {
		return nodes, nil
	}
	root, err := canonicalPath(directoryRoot)
	if err != nil {
		return nil, invalidArgument("directory scope must be resolvable", map[string]any{"path": directoryRoot})
	}
	filtered := make([]Node, 0)
	for _, node := range nodes {
		if pathWithinRoot(root, node.DirectoryPath) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func sortNodesByDirectory(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].DirectoryPath == nodes[j].DirectoryPath {
			return nodes[i].Slug < nodes[j].Slug
		}
		return nodes[i].DirectoryPath < nodes[j].DirectoryPath
	})
}

func (s *Service) hydrateConfigurationSlugs(nodes []Node) error {
	cache := make(map[string]string)
	for index := range nodes {
		if nodes[index].ConfigurationID == "" {
			continue
		}
		slug, ok := cache[nodes[index].ConfigurationID]
		if !ok {
			configuration, err := s.store.ConfigurationByID(nodes[index].ConfigurationID)
			if err != nil {
				return err
			}
			slug = configuration.Slug
			cache[configuration.ID] = slug
		}
		nodes[index].ConfigurationSlug = slug
	}
	return nil
}

func (s *Service) hydrateConfigurationSlug(node *Node) error {
	if node == nil || node.ConfigurationID == "" {
		return nil
	}
	configuration, err := s.store.ConfigurationByID(node.ConfigurationID)
	if err != nil {
		return err
	}
	node.ConfigurationSlug = configuration.Slug
	return nil
}

func (s *Service) NodeCleanupIncomplete(ctx context.Context, apply bool) (IncompleteNodeCleanupResult, error) {
	// A dry run only inspects the home, so it stays on the read tier and never
	// requires a runtime backend. Applying can tear down live runtime instances,
	// so it takes the full write-readiness path (seed/repair under locks plus
	// runtime-dependency validation, which requires Lima).
	if apply {
		if err := s.EnsureReady(ctx, true); err != nil {
			return IncompleteNodeCleanupResult{}, err
		}
	} else if err := s.EnsureReady(ctx, false); err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	// Scanning for incomplete directories is a cross-node read, so it runs under
	// the global nodes lock — and only that: the runtime teardown below is the
	// multi-minute part and must not hold it.
	var items []IncompleteNodeMetadata
	if err := s.withLocks(ctx, []lockKey{lockNodes}, nil, func() error {
		var scanErr error
		items, scanErr = s.store.IncompleteNodeMetadata()
		return scanErr
	}); err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	result := IncompleteNodeCleanupResult{DryRun: !apply, Items: items}
	if !apply || len(items) == 0 {
		return result, nil
	}

	// Consult the runtime before deleting any metadata. An incomplete node dir
	// still carries its sandbox-instance.ref, so os.RemoveAll'ing it while the
	// instance is live orphans a running VM and loses the only pointer back to
	// it (TODO #10). Tear the instance down first; only then is removing the
	// metadata that references it safe. Dirs with no matching live instance keep
	// the historical behavior of a straight metadata removal.
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	var teardownFailures []string
	for _, item := range items {
		// An in-flight NodeCreate owns its reservation until it completes. Its
		// liveness token is what distinguishes "being created right now" from
		// "left behind by a crash", now that the create no longer holds a lock
		// across the VM work.
		token, ok, tokenErr := tryAcquireNodeOperation(s.cfg.MetadataRoot, item.NodeID)
		if tokenErr != nil {
			return IncompleteNodeCleanupResult{}, tokenErr
		}
		if !ok {
			s.log().Info("skipping incomplete node with a live create", "node", item.NodeID)
			continue
		}

		if instanceName := strings.TrimSpace(item.SandboxName); instanceName != "" {
			if _, live := findObservation(observations, instanceName); live {
				if delErr := s.sandbox.Delete(ctx, Node{SandboxName: instanceName}); delErr != nil {
					// Leave the dir (and its ref) in place so a retry or a
					// manual limactl deletion can still find the instance.
					teardownFailures = append(teardownFailures, instanceName)
					token.release()
					continue
				}
			}
		}

		removeErr := s.withLocks(ctx, []lockKey{lockNodes}, []string{item.NodeID}, func() error {
			// Re-check under the lock: a create that finished while the runtime
			// teardown ran now owns a complete node directory.
			if s.store.nodeMetadataExists(item.NodeID) {
				return nil
			}
			return s.store.RemoveIncompleteNodeMetadata([]IncompleteNodeMetadata{item})
		})
		token.release()
		if removeErr != nil {
			return IncompleteNodeCleanupResult{}, removeErr
		}
	}

	if len(teardownFailures) > 0 {
		return result, externalCommandFailed(
			"failed to tear down runtime instances for incomplete nodes; their metadata was kept",
			fmt.Errorf("instances still present: %s", strings.Join(teardownFailures, ", ")),
			map[string]any{"instances": teardownFailures},
		)
	}

	return result, nil
}

func (s *Service) NodeShow(ctx context.Context, value string) (Node, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return Node{}, err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	// Showing a node is a read: merge the live observation in memory only.
	node, err = s.reconcileNode(ctx, node)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&node); err != nil {
		return Node{}, err
	}
	return node, nil
}

// nodeTerminalMetadata resolves the durable node fields needed to launch a
// terminal without reconciling live VM status. A host terminal must remain
// available when Lima is stopped or temporarily unavailable, and a
// guest terminal's child command performs its own runtime checks.
func (s *Service) nodeTerminalMetadata(ctx context.Context, value string) (Node, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return Node{}, err
	}
	return s.store.NodeByIDOrSlug(value)
}

func (s *Service) NodeStart(ctx context.Context, value string) (_ Node, err error) {
	s.log().Debug("operation started", "op", "node.start", "node", value)
	defer s.logOperation("node.start", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	resolved, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	token, err := s.claimNodeOperation("node.start", resolved.ID)
	if err != nil {
		return Node{}, err
	}
	defer token.release()

	var (
		node           Node
		bootstrap      BootstrapState
		previousStatus NodeStatus
	)
	// Metadata phase. Publishing the provisioning status here is what makes the
	// unlocked runtime work below safe: a second operation on this node sees an
	// in-flight status (or fails to take the token) instead of interleaving.
	if err := s.withLocks(ctx, nil, []string{resolved.ID}, func() error {
		var phaseErr error
		node, phaseErr = s.store.NodeByID(resolved.ID)
		if phaseErr != nil {
			return phaseErr
		}
		bootstrap, phaseErr = s.store.LoadBootstrapState(node.ID)
		if phaseErr != nil {
			return phaseErr
		}
		if migratedNode, migratedBootstrap, migrated := migrateKnownBuiltInNodeBootstrap(node, bootstrap); migrated {
			node = migratedNode
			bootstrap = migratedBootstrap
			if phaseErr := s.store.SaveNode(node, bootstrap); phaseErr != nil {
				return phaseErr
			}
			if phaseErr := s.store.AppendNodeEvent(node.ID, Event{Timestamp: s.now(), Type: "node.bootstrap.migrated"}); phaseErr != nil {
				return phaseErr
			}
		}
		if phaseErr := s.recoverStaleNodeClaim("node.start", &node, bootstrap); phaseErr != nil {
			return phaseErr
		}

		previousStatus = node.Status
		now := s.now()
		node.Status = NodeStatusProvisioning
		node.UpdatedAt = now
		if phaseErr := s.store.SaveNode(node, bootstrap); phaseErr != nil {
			return phaseErr
		}
		return s.store.AppendNodeEvent(node.ID, Event{Timestamp: now, Type: "node.start.started"})
	}); err != nil {
		return Node{}, err
	}

	// Nothing below holds a flock until the finish phase: VM boot, workspace
	// seeding, every bootstrap command, and agent validation are the
	// multi-minute work that used to hold the global nodes lock.
	//
	// Uncached on purpose: the observation immediately below decides whether to
	// boot the VM at all, and the daemon's cache can be a full reconciliation
	// interval behind — or hold an entry synthesized from a watch event that
	// only ever carried a running/exiting bit.
	node, err = s.reconcileNodeUncached(ctx, node)
	if err != nil {
		s.restoreNodeStatus(resolved.ID, previousStatus)
		return Node{}, err
	}

	if node.LastRuntimeObservation == nil || node.LastRuntimeObservation.Status != ObservationRunning {
		if err := s.ensureNodeHostPortsAvailable(ctx, node); err != nil {
			s.restoreNodeStatus(node.ID, previousStatus)
			return Node{}, err
		}
		s.logRuntime("start", node.ID)
		if err := s.sandbox.Start(ctx, node); err != nil {
			s.restoreNodeStatus(node.ID, previousStatus)
			return Node{}, err
		}
	}

	bootstrapRan := !bootstrap.Completed
	if bootstrapRan {
		if !node.WorkspaceSeeded {
			if err := s.prepareGuestWorkspace(ctx, node); err != nil {
				node.Status = NodeStatusFailed
				node.UpdatedAt = s.now()
				s.recordNodeStartRollback(node, bootstrap, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"directory_path": node.DirectoryPath, "error": err.Error()}})
				return Node{}, err
			}

			node.WorkspaceSeeded = true
			node.UpdatedAt = s.now()
			if err := s.withLocks(ctx, nil, []string{node.ID}, func() error {
				return s.store.SaveNode(node, bootstrap)
			}); err != nil {
				return Node{}, err
			}
		}

		for _, command := range bootstrap.CombinedCommands() {
			if err := s.runBootstrapGuestCommand(ctx, node, command); err != nil {
				node.Status = NodeStatusFailed
				node.UpdatedAt = s.now()
				s.recordNodeStartRollback(node, bootstrap, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"command": command, "error": err.Error()}})
				return Node{}, err
			}
		}

	}

	if err := s.runGuestCommand(ctx, node, bootstrap.ValidationCommand); err != nil {
		node.Status = NodeStatusFailed
		node.UpdatedAt = s.now()
		s.recordNodeStartRollback(node, bootstrap, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"validation_command": bootstrap.ValidationCommand, "error": err.Error()}})
		return Node{}, err
	}
	if bootstrapRan {
		completedAt := s.now()
		bootstrap.Completed = true
		bootstrap.CompletedAt = &completedAt
		node.BootstrapCompleted = true
		node.BootstrapCompletedAt = &completedAt
	}

	node.Status = NodeStatusRunning
	node.UpdatedAt = s.now()
	if err := s.withLocks(ctx, nil, []string{node.ID}, func() error {
		if err := s.store.SaveNode(node, bootstrap); err != nil {
			return err
		}
		return s.store.AppendNodeEvent(node.ID, Event{Timestamp: node.UpdatedAt, Type: "node.started"})
	}); err != nil {
		return Node{}, err
	}

	result, err := s.reconcileNodePersisted(ctx, node)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&result); err != nil {
		return Node{}, err
	}
	return result, nil
}

func (s *Service) ensureNodeHostPortsAvailable(ctx context.Context, node Node) error {
	ports, err := validatePorts(node.Ports)
	if err != nil || len(ports) == 0 {
		return err
	}
	requested := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		requested[strings.SplitN(port, ":", 2)[0]] = struct{}{}
	}
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return err
	}
	running := make(map[string]struct{})
	for _, observation := range observations {
		if observation.Status == ObservationRunning {
			running[observation.Name] = struct{}{}
		}
	}
	nodes, err := s.store.ListNodes(false)
	if err != nil {
		return err
	}
	for _, other := range nodes {
		if other.ID == node.ID {
			continue
		}
		if _, ok := running[other.SandboxName]; !ok {
			continue
		}
		otherPorts, validateErr := validatePorts(other.Ports)
		if validateErr != nil {
			return validateErr
		}
		for _, port := range otherPorts {
			hostPort := strings.SplitN(port, ":", 2)[0]
			if _, conflict := requested[hostPort]; conflict {
				return preconditionFailed("host port is already reserved by a running node", map[string]any{
					"host_port": hostPort, "node": other.Slug, "sandbox_name": other.SandboxName,
				})
			}
		}
	}
	return nil
}

func (s *Service) NodeStop(ctx context.Context, value string) (_ Node, err error) {
	s.log().Debug("operation started", "op", "node.stop", "node", value)
	defer s.logOperation("node.stop", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	resolved, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	token, err := s.claimNodeOperation("node.stop", resolved.ID)
	if err != nil {
		return Node{}, err
	}
	defer token.release()

	var (
		node      Node
		bootstrap BootstrapState
	)
	// A stop leaves no in-flight status of its own: a crash mid-stop is fully
	// recoverable from the next runtime observation. It still recovers a claim
	// stranded by some earlier operation before it touches the node.
	if err := s.withLocks(ctx, nil, []string{resolved.ID}, func() error {
		var phaseErr error
		node, phaseErr = s.store.NodeByID(resolved.ID)
		if phaseErr != nil {
			return phaseErr
		}
		bootstrap, phaseErr = s.store.LoadBootstrapState(node.ID)
		if phaseErr != nil {
			return phaseErr
		}
		return s.recoverStaleNodeClaim("node.stop", &node, bootstrap)
	}); err != nil {
		return Node{}, err
	}

	node, err = s.reconcileNode(ctx, node)
	if err != nil {
		return Node{}, err
	}

	if node.LastRuntimeObservation != nil && node.LastRuntimeObservation.Status != ObservationRunning {
		node.Status = NodeStatusStopped
		node.UpdatedAt = s.now()
		if err := s.withLocks(ctx, nil, []string{node.ID}, func() error {
			return s.store.SaveNode(node, bootstrap)
		}); err != nil {
			return Node{}, err
		}
		if err := s.hydrateConfigurationSlug(&node); err != nil {
			return Node{}, err
		}
		return node, nil
	}

	// Unlocked: stopping a VM is bounded by the runtime, not by us.
	s.logRuntime("stop", node.ID)
	if err := s.sandbox.Stop(ctx, node); err != nil {
		return Node{}, err
	}

	node.Status = NodeStatusStopped
	node.UpdatedAt = s.now()
	if err := s.withLocks(ctx, nil, []string{node.ID}, func() error {
		if err := s.store.SaveNode(node, bootstrap); err != nil {
			return err
		}
		return s.store.AppendNodeEvent(node.ID, Event{Timestamp: node.UpdatedAt, Type: "node.stopped"})
	}); err != nil {
		return Node{}, err
	}

	result, err := s.reconcileNodePersisted(ctx, node)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&result); err != nil {
		return Node{}, err
	}
	return result, nil
}

// nodeCloneSourceRestartTimeout bounds the deferred restart of a clone's source
// VM. `limactl start` is the fifteen-minute class (limaCommandTimeout); the
// extra slack covers the verifying list that Start does and the reconciling
// list that follows it, so the restart is not cut off by its own budget at the
// moment it is about to be recorded.
const nodeCloneSourceRestartTimeout = limaCommandTimeout + 2*time.Minute

func (s *Service) NodeClone(ctx context.Context, input NodeCloneInput) (childNode Node, err error) {
	s.log().Debug("operation started", "op", "node.clone", "source", input.SourceNode)
	defer s.logOperation("node.clone", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	resolvedSource, err := s.store.NodeByIDOrSlug(input.SourceNode)
	if err != nil {
		return Node{}, err
	}

	sourceToken, err := s.claimNodeOperation("node.clone", resolvedSource.ID)
	if err != nil {
		return Node{}, err
	}
	defer sourceToken.release()

	var (
		sourceNode      Node
		sourceBootstrap BootstrapState
	)
	if err := s.withLocks(ctx, nil, []string{resolvedSource.ID}, func() error {
		var phaseErr error
		sourceNode, phaseErr = s.store.NodeByID(resolvedSource.ID)
		if phaseErr != nil {
			return phaseErr
		}
		sourceBootstrap, phaseErr = s.store.LoadBootstrapState(sourceNode.ID)
		if phaseErr != nil {
			return phaseErr
		}
		return s.recoverStaleNodeClaim("node.clone", &sourceNode, sourceBootstrap)
	}); err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&sourceNode); err != nil {
		return Node{}, err
	}

	// Unlocked from here: cycling the source VM and cloning its disk are the
	// multi-minute half of a clone.
	sourceNode, err = s.reconcileNode(ctx, sourceNode)
	if err != nil {
		return Node{}, err
	}

	sourceWasRunning := sourceNode.LastRuntimeObservation != nil && sourceNode.LastRuntimeObservation.Status == ObservationRunning
	if sourceWasRunning {
		if err := s.sandbox.Stop(ctx, sourceNode); err != nil {
			return Node{}, err
		}
	}
	defer func() {
		if !sourceWasRunning {
			return
		}

		// Restart on a fresh context, not the caller's. The clone stopped a VM
		// the user had running, so putting it back is owed regardless of how the
		// clone ended: reusing ctx meant a Ctrl+C during the clone cancelled the
		// restart the instant it started, and the user's source VM stayed
		// stopped by an operation they only interrupted. The sibling rollback
		// below already builds its own context for the same reason.
		restartCtx, cancel := context.WithTimeout(context.Background(), nodeCloneSourceRestartTimeout)
		defer cancel()

		if restartErr := s.sandbox.Start(restartCtx, sourceNode); restartErr != nil {
			err = errors.Join(err, restartErr)
			return
		}

		if _, reconcileErr := s.reconcileNodePersisted(restartCtx, sourceNode); reconcileErr != nil {
			err = errors.Join(err, reconcileErr)
		}
	}()

	childNodeSlug := strings.TrimSpace(input.NodeSlug)
	if childNodeSlug == "" {
		return Node{}, invalidArgument("node clone requires --slug", nil)
	}
	if slugify(childNodeSlug) != childNodeSlug {
		return Node{}, invalidArgument("node slug must be a lowercase slug", map[string]any{"slug": childNodeSlug})
	}

	bootstrap := sourceBootstrap
	bootstrap.InstallCommands = append([]string(nil), sourceBootstrap.InstallCommands...)
	bootstrap.BootstrapCommands = append([]string(nil), sourceBootstrap.BootstrapCommands...)
	bootstrap.Environment = cloneStringMap(sourceBootstrap.Environment)

	var (
		cloneTarget Node
		childToken  *nodeOperationToken
	)
	// Naming the child is the cross-node phase, exactly as in NodeCreate.
	if err := s.withLocks(ctx, []lockKey{lockNodes}, nil, func() error {
		if phaseErr := s.ensureUniqueNodeSlug(childNodeSlug); phaseErr != nil {
			return phaseErr
		}
		sandboxName, phaseErr := s.generateSandboxName(childNodeSlug)
		if phaseErr != nil {
			return phaseErr
		}

		cloneTarget = Node{
			ID:                   newID(),
			Slug:                 childNodeSlug,
			ConfigurationID:      sourceNode.ConfigurationID,
			ConfigurationSlug:    sourceNode.ConfigurationSlug,
			DirectoryPath:        sourceNode.DirectoryPath,
			ParentNodeID:         sourceNode.ID,
			Runtime:              RuntimeVM,
			Provider:             ProviderLima,
			SandboxName:          sandboxName,
			Image:                sourceNode.Image,
			VCPUs:                sourceNode.VCPUs,
			MemoryMiB:            sourceNode.MemoryMiB,
			DiskMiB:              sourceNode.DiskMiB,
			Environments:         append([]string(nil), sourceNode.Environments...),
			Ports:                append([]string(nil), sourceNode.Ports...),
			Status:               NodeStatusCreated,
			AgentProfileName:     sourceNode.AgentProfileName,
			RuntimeCommands:      sourceNode.RuntimeCommands,
			BootstrapCommands:    append([]string(nil), sourceNode.BootstrapCommands...),
			WorkspaceMode:        nodeWorkspaceMode(sourceNode),
			GuestWorkspacePath:   sourceNode.GuestWorkspacePath,
			WorkspaceMountPath:   sourceNode.WorkspaceMountPath,
			WorkspaceSeeded:      sourceNode.WorkspaceSeeded,
			BootstrapCompleted:   bootstrap.Completed,
			BootstrapCompletedAt: bootstrap.CompletedAt,
			CreatedAt:            s.now(),
			UpdatedAt:            s.now(),
		}

		claim, phaseErr := s.claimNodeOperation("node.clone", cloneTarget.ID)
		if phaseErr != nil {
			return phaseErr
		}
		if phaseErr := s.store.SaveIncompleteNodeReference(cloneTarget.ID, cloneTarget.SandboxName); phaseErr != nil {
			claim.release()
			return phaseErr
		}
		childToken = claim
		return nil
	}); err != nil {
		return Node{}, err
	}
	defer childToken.release()

	cleanupChild := true
	cleanupCloneInstance := false
	defer func() {
		if err == nil || !cleanupChild {
			return
		}
		if cleanupCloneInstance {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			deleteErr := s.sandbox.Delete(cleanupCtx, cloneTarget)
			cancel()
			if deleteErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback cloned Lima instance %s: %w", cloneTarget.SandboxName, deleteErr))
				return
			}
		}
		removeErr := s.withRollbackLocks([]lockKey{lockNodes}, []string{cloneTarget.ID}, func() error {
			return s.store.RemoveIncompleteNodeMetadata([]IncompleteNodeMetadata{{
				NodeID: cloneTarget.ID, DirectoryPath: s.store.nodeDir(cloneTarget.ID), SandboxName: cloneTarget.SandboxName,
			}})
		})
		if removeErr != nil {
			err = errors.Join(err, removeErr)
		}
	}()

	s.logRuntime("clone", cloneTarget.ID)
	if cloneErr := s.sandbox.Clone(ctx, sourceNode, cloneTarget); cloneErr != nil {
		cleanupCloneInstance = runtimeMutationMayHaveCreated(cloneErr)
		return Node{}, cloneErr
	}
	cleanupCloneInstance = true

	reconciledChildNode, err := s.reconcileNode(ctx, cloneTarget)
	if err != nil {
		return Node{}, err
	}
	if reconciledChildNode.LastRuntimeObservation != nil && reconciledChildNode.LastRuntimeObservation.Status == ObservationRunning {
		if err := s.sandbox.Stop(ctx, cloneTarget); err != nil {
			return Node{}, err
		}
		reconciledChildNode, err = s.reconcileNode(ctx, cloneTarget)
		if err != nil {
			return Node{}, err
		}
	}
	childNode = reconciledChildNode

	if err := s.withLocks(ctx, []lockKey{lockNodes}, []string{childNode.ID}, func() error {
		if phaseErr := s.store.SaveNode(childNode, bootstrap); phaseErr != nil {
			return phaseErr
		}
		cleanupChild = false
		return s.store.AppendNodeEvent(childNode.ID, Event{Timestamp: s.now(), Type: "node.cloned", Fields: map[string]any{"source_node_id": sourceNode.ID}})
	}); err != nil {
		return Node{}, err
	}

	return childNode, nil
}

func (s *Service) NodeDelete(ctx context.Context, value string) (Node, error) {
	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	resolved, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	token, err := s.claimNodeOperation("node.delete", resolved.ID)
	if err != nil {
		return Node{}, err
	}
	defer token.release()

	var (
		node      Node
		bootstrap BootstrapState
	)
	// The terminating status is the durable half of the guard; the runtime
	// teardown below runs with no lock held. Both phases take the global nodes
	// lock too, because a terminated node releases its by-instance and by-slug
	// index entries, which is cross-node state.
	if err := s.withLocks(ctx, []lockKey{lockNodes}, []string{resolved.ID}, func() error {
		var phaseErr error
		node, phaseErr = s.store.NodeByID(resolved.ID)
		if phaseErr != nil {
			return phaseErr
		}
		bootstrap, phaseErr = s.store.LoadBootstrapState(node.ID)
		if phaseErr != nil {
			return phaseErr
		}
		if phaseErr := s.recoverStaleNodeClaim("node.delete", &node, bootstrap); phaseErr != nil {
			return phaseErr
		}

		node.Status = NodeStatusTerminating
		node.UpdatedAt = s.now()
		return s.store.SaveNode(node, bootstrap)
	}); err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&node); err != nil {
		return Node{}, err
	}

	if err := s.sandbox.Delete(ctx, node); err != nil {
		return Node{}, err
	}

	deletedAt := s.now()
	node.Status = NodeStatusTerminated
	node.UpdatedAt = deletedAt
	node.DeletedAt = &deletedAt
	if err := s.withLocks(ctx, []lockKey{lockNodes}, []string{node.ID}, func() error {
		if phaseErr := s.store.SaveNode(node, bootstrap); phaseErr != nil {
			return phaseErr
		}
		return s.store.AppendNodeEvent(node.ID, Event{Timestamp: deletedAt, Type: "node.deleted"})
	}); err != nil {
		return Node{}, err
	}

	return node, nil
}

func (s *Service) NodeStatus(ctx context.Context, value string) (Node, error) {
	return s.NodeShow(ctx, value)
}

func (s *Service) NodeLogs(ctx context.Context, value string) ([]Event, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return nil, err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return nil, err
	}

	return s.store.NodeEvents(node.ID)
}

func (s *Service) Shell(ctx context.Context, value string, command []string) error {
	if err := s.EnsureReady(ctx, false); err != nil {
		return err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return err
	}

	command = normalizeShellCommand(command)
	workdir := s.nodeGuestWorkspacePath(node)
	interactive := len(command) == 0
	if interactive {
		command = interactiveShellLaunchCommand()
	}
	return s.sandbox.Shell(ctx, node, command, workdir, interactive, ShellStreams{
		Stdin:  s.stdin,
		Stdout: s.stdout,
		Stderr: s.stderr,
	})
}

// LaunchSpec is the single description of how to spawn a managed terminal: the
// argv to exec, the working directory to root it in (empty means inherit), and
// the environment to run it with. It is the one shell-launch contract every
// front end (the TUI today, the daemon and any tmux sidebar tomorrow) asks the
// Service for, then hands to the runtime registry to spawn — no caller builds a
// terminal command itself (IMPROVEMENT_PLAN Part F §2.2).
type LaunchSpec struct {
	Argv []string
	Dir  string
	Env  []string
}

// TerminalLaunchSpec builds the LaunchSpec for a target terminal. It is the
// sole place a managed-terminal command is assembled; front ends spawn only
// what it returns (IMPROVEMENT_PLAN Part F §2.2).
//
//   - NodeShell re-enters the codelima binary as `codelima shell <nodeID>`,
//     where nodeID is target.ID, so the child re-enters the VM through CodeLima
//     rather than a raw runtime shell. The codelima executable is resolved here
//     in the Service (os.Executable + resolveCodelimaExecutablePath), not taken
//     from any caller-cached copy.
//   - NodeHostShell is an interactive login shell rooted at the node's host
//     directory. The path is validated here (non-empty, exists,
//     is a directory); a failure is returned as a typed InvalidArgument error
//     that the caller records against the target.
//
// The caller supplies the already-resolved node directory path (node shells
// pass ""); resolution of the node from the store stays with the caller, which
// keeps this a pure, store-free spec builder.
func (s *Service) TerminalLaunchSpec(target terminal.TargetKey, kind terminal.TerminalKind, workspacePath string) (LaunchSpec, error) {
	switch kind {
	case terminal.NodeShell:
		nodeID := strings.TrimSpace(target.ID)
		if nodeID == "" {
			return LaunchSpec{}, invalidArgument("node id is required to launch a node shell", nil)
		}
		executable, err := resolveCodelimaSelfExecutable()
		if err != nil {
			return LaunchSpec{}, dependencyUnavailable("could not resolve the codelima executable to launch a node shell", err, nil)
		}
		return LaunchSpec{
			Argv: []string{executable, "--home", s.cfg.MetadataRoot, "shell", nodeID},
			Env:  os.Environ(),
		}, nil
	case terminal.NodeHostShell:
		if strings.TrimSpace(workspacePath) == "" {
			return LaunchSpec{}, invalidArgument("node directory path is not configured", map[string]any{"target": target.String()})
		}
		info, err := os.Stat(workspacePath)
		if err != nil {
			return LaunchSpec{}, invalidArgument("node directory path is unavailable", map[string]any{"target": target.String(), "directory_path": workspacePath, "error": err.Error()})
		}
		if !info.IsDir() {
			return LaunchSpec{}, invalidArgument("node directory path is not a directory", map[string]any{"target": target.String(), "directory_path": workspacePath})
		}
		return LaunchSpec{
			Argv: interactiveShellLaunchCommand(),
			Dir:  workspacePath,
			Env:  os.Environ(),
		}, nil
	default:
		return LaunchSpec{}, invalidArgument("unsupported terminal kind", map[string]any{"kind": kind.String()})
	}
}

// resolveCodelimaSelfExecutable resolves the path to the running codelima
// binary (following the platform-dir compatibility symlink), so the Service
// resolves the executable itself rather than depending on any caller's cached
// copy. It was a rebindable package var for a test seam that no longer exists;
// nothing reassigns it, so it is an ordinary function.
func resolveCodelimaSelfExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return resolveCodelimaExecutablePath(executable), nil
}

func (s *Service) ensureUniqueNodeSlug(slug string) error {
	nodes, err := s.store.ListNodes(false)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if node.Slug == slug {
			return preconditionFailed("node slug already exists", map[string]any{"slug": slug})
		}
	}

	return nil
}

func (s *Service) ensureUniqueEnvironmentConfigSlug(slug, currentConfigID string) error {
	configs, err := s.store.ListEnvironmentConfigs(false)
	if err != nil {
		return err
	}

	for _, config := range configs {
		if config.Slug == slug && config.ID != currentConfigID {
			return preconditionFailed("environment slug already exists", map[string]any{"slug": slug})
		}
	}

	return nil
}

func (s *Service) resolveEnvironmentConfigRefs(refs []string) ([]string, error) {
	if refs == nil {
		return nil, nil
	}

	resolved := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return nil, invalidArgument("environment slug is required", nil)
		}

		config, err := s.store.EnvironmentConfigByIDOrSlug(ref)
		if err != nil {
			return nil, err
		}
		if config.DeletedAt != nil {
			return nil, notFound("environment not found", map[string]any{"query": ref})
		}
		if seen[config.Slug] {
			continue
		}

		resolved = append(resolved, config.Slug)
		seen[config.Slug] = true
	}

	return resolved, nil
}

func (s *Service) resolveConfigurationBootstrapCommands(configuration Configuration) ([]string, error) {
	commands := make([]string, 0)
	for _, slug := range configuration.Environments {
		environment, err := s.store.EnvironmentConfigByIDOrSlug(slug)
		if err != nil {
			return nil, err
		}
		if environment.DeletedAt != nil {
			return nil, notFound("environment not found", map[string]any{"query": slug})
		}
		commands = append(commands, environment.BootstrapCommands...)
	}
	commands = append(commands, configuration.BootstrapCommands...)
	return commands, nil
}

func (s *Service) resolveNodeDirectoryPath(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", invalidArgument("current directory is unavailable", nil)
		}
		input = cwd
	}
	directoryPath, err := canonicalPath(input)
	if err != nil {
		return "", invalidArgument("node directory must be resolvable", map[string]any{"path": input})
	}
	info, err := os.Stat(directoryPath)
	if err != nil || !info.IsDir() {
		return "", invalidArgument("node directory must be an existing directory", map[string]any{"path": directoryPath})
	}
	if pathWithinRoot(s.cfg.MetadataRoot, directoryPath) {
		return "", invalidArgument("node directory must not be inside CODELIMA_HOME", map[string]any{"path": directoryPath})
	}
	return directoryPath, nil
}

func ensureNodeDirectoryAvailable(node Node) error {
	info, err := os.Stat(node.DirectoryPath)
	if err != nil {
		return preconditionFailed("node directory no longer exists on the host", map[string]any{"node_id": node.ID, "directory_path": node.DirectoryPath})
	}
	if !info.IsDir() {
		return preconditionFailed("node directory is no longer a directory on the host", map[string]any{"node_id": node.ID, "directory_path": node.DirectoryPath})
	}
	return nil
}

func (s *Service) generateSandboxName(nodeSlug string) (string, error) {
	sandboxName := slugify(nodeSlug)
	if len(sandboxName) > 128 {
		sandboxName = sandboxName[:128]
		sandboxName = strings.Trim(sandboxName, "-")
	}
	if sandboxName == "" {
		sandboxName = "node"
	}
	if err := validateSandboxName(sandboxName); err != nil {
		return "", err
	}
	if exists(s.store.nodeInstanceIndexPath(sandboxName)) {
		return "", preconditionFailed("sandbox name already exists", map[string]any{"sandbox_name": sandboxName})
	}

	nodes, err := s.store.ListNodes(false)
	if err != nil {
		return "", err
	}

	for _, node := range nodes {
		if node.SandboxName == sandboxName && node.Status != NodeStatusTerminated {
			return "", preconditionFailed("sandbox name already exists", map[string]any{"sandbox_name": sandboxName})
		}
	}

	return sandboxName, nil
}

// bootstrapGuestCommandTimeout is the budget for one bootstrap or agent-install
// command in the guest.
//
// These are the only guest commands that legitimately run for tens of minutes:
// a cold VM's first `apt-get update && apt-get install`, a NodeSource install,
// and `npm install -g` for the agent, over whatever the host's uplink happens
// to be. Under the previous blanket ten-minute cap a slow network killed the
// install mid-flight and the node was recorded Failed, which is both wrong and
// unrecoverable without a manual retry. Forty-five minutes is well past any
// plausible honest install and still short enough that a genuinely wedged
// command surfaces the same day.
const bootstrapGuestCommandTimeout = 45 * time.Minute

// runBootstrapGuestCommand runs one bootstrap/agent-install command under the
// long budget. The deadline is set here, at the call site that knows the class
// of work, and the runtime honors it (see guestCommandTimeout).
func (s *Service) runBootstrapGuestCommand(ctx context.Context, node Node, command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}

	bootstrapCtx, cancel := context.WithTimeout(ctx, bootstrapGuestCommandTimeout)
	defer cancel()
	return s.runGuestCommand(bootstrapCtx, node, command)
}

func (s *Service) runGuestCommand(ctx context.Context, node Node, command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}

	workdir := s.nodeGuestWorkspacePath(node)
	script := command
	if workdir != "" {
		// Go's %q escapes for Go, not for sh: it leaves $ and backticks live, so
		// a node directory containing $(...) would execute as root in the guest.
		// shellQuote is the same helper the adjacent seeding path uses.
		script = "cd " + shellQuote(workdir) + " && " + command
	}
	return s.sandbox.Shell(ctx, node, []string{"sh", "-lc", script}, workdir, false, ShellStreams{})
}

func (s *Service) reclaimMountedNodeFilesystemCaches(ctx context.Context) (int, error) {
	nodes, err := s.NodeList(ctx, false)
	if err != nil {
		return 0, err
	}

	reclaimed := 0
	var reclaimErr error
	for _, node := range nodes {
		if nodeWorkspaceMode(node) != WorkspaceModeMounted || node.LastRuntimeObservation == nil || node.LastRuntimeObservation.Status != ObservationRunning {
			continue
		}
		reclaimCtx, cancel := context.WithTimeout(ctx, guestFilesystemCacheReclaimTimeout)
		shellErr := s.sandbox.Shell(
			reclaimCtx,
			node,
			[]string{"sh", "-c", "echo 2 > /proc/sys/vm/drop_caches"},
			"",
			false,
			ShellStreams{},
		)
		cancel()
		if shellErr != nil {
			reclaimErr = errors.Join(reclaimErr, fmt.Errorf("reclaim filesystem metadata cache for mounted node %s: %w", node.Slug, shellErr))
			continue
		}
		reclaimed++
	}

	return reclaimed, reclaimErr
}

func (s *Service) prepareGuestWorkspace(ctx context.Context, node Node) error {
	if err := ensureNodeDirectoryAvailable(node); err != nil {
		return err
	}

	if nodeWorkspaceMode(node) == WorkspaceModeMounted {
		return nil
	}

	return s.seedGuestWorkspace(ctx, node)
}

func (s *Service) seedGuestWorkspace(ctx context.Context, node Node) error {
	targetPath := s.nodeGuestWorkspacePath(node)
	sourcePath := node.DirectoryPath
	prepareScript, err := s.resolveWorkspaceSeedPrepareCommand(node, sourcePath, targetPath)
	if err != nil {
		return err
	}

	if err := s.sandbox.Shell(ctx, node, []string{"sh", "-lc", prepareScript}, "", false, ShellStreams{}); err != nil {
		return err
	}

	return s.sandbox.CopyToGuest(ctx, node, sourcePath, targetPath, true)
}

func (s *Service) resolveWorkspaceSeedPrepareCommand(node Node, sourcePath, targetPath string) (string, error) {
	commands, err := s.sandbox.ResolveCommands(node, runtimeCommandWorkspaceSeedPrepare, map[string]string{
		"sandbox_name":  shellQuote(node.SandboxName),
		"source_path":   shellQuote(sourcePath),
		"target_path":   shellQuote(targetPath),
		"target_parent": shellQuote(filepath.Dir(targetPath)),
	})
	if err != nil {
		return "", err
	}

	return strings.Join(commands, " && "), nil
}

func normalizeShellCommand(command []string) []string {
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}

	return append([]string(nil), command...)
}

// interactiveShellScript is the login-shell bootstrap for managed terminals.
// It lives in a real .sh file so it can be read and linted as shell; the
// @INPUTRC_LINES@ token is replaced with quoted readline bindings at launch.
//
//go:embed embedded/interactive_shell.sh
var interactiveShellScript string

func interactiveShellLaunchCommand() []string {
	shellInputRCLines := []string{
		`"\e[27;2;13~": "\C-v\C-j"`,
		`"\e[13;2u": "\C-v\C-j"`,
	}
	shellInputRCArgs := make([]string, 0, len(shellInputRCLines))
	for _, line := range shellInputRCLines {
		shellInputRCArgs = append(shellInputRCArgs, shellQuote(line))
	}

	script := strings.ReplaceAll(interactiveShellScript, "@INPUTRC_LINES@", strings.Join(shellInputRCArgs, " "))
	return []string{"sh", "-lc", script}
}

func (s *Service) nodeGuestWorkspacePath(node Node) string {
	if node.GuestWorkspacePath != "" {
		return node.GuestWorkspacePath
	}

	if node.WorkspaceMountPath != "" {
		return node.WorkspaceMountPath
	}

	return node.DirectoryPath
}

func normalizeWorkspaceMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "", WorkspaceModeCopy:
		return WorkspaceModeCopy
	case WorkspaceModeMounted:
		return WorkspaceModeMounted
	default:
		return ""
	}
}

func nodeWorkspaceMode(node Node) string {
	if mode := normalizeWorkspaceMode(node.WorkspaceMode); mode != "" {
		return mode
	}
	if node.WorkspaceMountPath != "" {
		return WorkspaceModeMounted
	}
	return WorkspaceModeCopy
}

// reconcileNode merges a live runtime listing into the node in memory and
// never writes. Persisting a reconciliation is reconcileNodePersisted's job,
// because that needs the node's metadata lock and this does not.
func (s *Service) reconcileNode(ctx context.Context, node Node) (Node, error) {
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return Node{}, err
	}

	return s.reconcileNodeWithObservations(node, observations, false, s.now())
}

// reconcileNodeUncached is reconcileNode for the paths whose answer decides
// whether to mutate the runtime.
//
// Read surfaces are happy to be a few seconds stale — that is the whole point
// of the observation cache (ADR 37). A start/stop decision is not: acting on a
// stale "already running" skips the boot and then runs bootstrap commands
// against a VM that is not there, and acting on a stale "stopped" starts an
// instance that is already up. Those paths pay for one `limactl list`.
func (s *Service) reconcileNodeUncached(ctx context.Context, node Node) (Node, error) {
	observations, err := s.listRuntimeUncached(ctx)
	if err != nil {
		return Node{}, err
	}

	return s.reconcileNodeWithObservations(node, observations, false, s.now())
}

// listRuntimeUncached bypasses any caching the runtime client does. Clients
// that have no cache (every test fake, and any future provider) satisfy the
// contract with plain List.
func (s *Service) listRuntimeUncached(ctx context.Context) ([]RuntimeObservation, error) {
	if direct, ok := s.sandbox.(uncachedSandboxLister); ok {
		return direct.ListUncached(ctx)
	}
	return s.sandbox.List(ctx)
}

// reconcileNodePersisted refreshes a node from a live runtime listing and
// persists the result. Listing the runtime is a subprocess round-trip, so it
// happens before the node's metadata lock is taken; only the write is inside it.
func (s *Service) reconcileNodePersisted(ctx context.Context, node Node) (Node, error) {
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return Node{}, err
	}

	var result Node
	if err := s.withLocks(ctx, nil, []string{node.ID}, func() error {
		var reconcileErr error
		result, reconcileErr = s.reconcileNodeWithObservations(node, observations, true, s.now())
		return reconcileErr
	}); err != nil {
		return Node{}, err
	}
	return result, nil
}

// reconcileNodes is the list-path counterpart of reconcileNode: one runtime
// listing merged into every node in memory, with no writes.
func (s *Service) reconcileNodes(ctx context.Context, nodes []Node) ([]Node, error) {
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return nil, err
	}

	now := s.now()
	reconciled := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		refreshed, err := s.reconcileNodeWithObservations(node, observations, false, now)
		if err != nil {
			return nil, err
		}
		reconciled = append(reconciled, refreshed)
	}

	return reconciled, nil
}

func (s *Service) reconcileNodeWithObservations(node Node, observations []RuntimeObservation, persist bool, now time.Time) (Node, error) {
	observation, ok := findObservation(observations, node.SandboxName)
	node.LastReconciledAt = &now
	if ok {
		node.LastRuntimeObservation = &observation
		switch observation.Status {
		case ObservationRunning:
			if node.Status != NodeStatusFailed && node.Status != NodeStatusTerminating && node.Status != NodeStatusTerminated {
				node.Status = NodeStatusRunning
			}
		case ObservationStopped:
			if node.Status != NodeStatusFailed && node.Status != NodeStatusTerminating && node.Status != NodeStatusTerminated {
				node.Status = NodeStatusStopped
			}
		}
	} else {
		node.LastRuntimeObservation = &RuntimeObservation{Name: node.SandboxName, Exists: false}
	}

	if persist {
		bootstrap, bootstrapErr := s.store.LoadBootstrapState(node.ID)
		if bootstrapErr != nil {
			return Node{}, bootstrapErr
		}

		node.UpdatedAt = now
		if saveErr := s.store.SaveNode(node, bootstrap); saveErr != nil {
			return Node{}, saveErr
		}
	}

	return node, nil
}

func findObservation(observations []RuntimeObservation, instanceName string) (RuntimeObservation, bool) {
	for _, observation := range observations {
		if observation.Name == instanceName {
			return observation, true
		}
	}

	return RuntimeObservation{}, false
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}
