package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/google/uuid"
)

// The server freezes this plan before admitting any side effect. Clients select
// an environment; they cannot supply a node to be deleted or a workspace path.
type EnvironmentTestPlan struct {
	Node      Node           `json:"node"`
	Bootstrap BootstrapState `json:"bootstrap"`
}

type EnvironmentTestState struct {
	EnvironmentID   string               `json:"environment_id"`
	EnvironmentSlug string               `json:"environment_slug"`
	Plan            *EnvironmentTestPlan `json:"plan,omitempty"`
	CleanupPending  bool                 `json:"cleanup_pending"`
	CleanupError    string               `json:"cleanup_error,omitempty"`
	Outcome         string               `json:"outcome,omitempty"`
	Error           string               `json:"error,omitempty"`
	ErrorCode       int                  `json:"error_code,omitempty"`
	ErrorCategory   string               `json:"error_category,omitempty"`
}

func environmentTestSlug(id string) string { return "env-test-" + strings.ReplaceAll(id, "-", "") }

func (s *Service) environmentTestWorkspace(id string) string {
	return filepath.Join(s.store.nodeDir(id), "test-workspace")
}

func (s *Service) prepareEnvironmentTest(ctx context.Context, ref string) (*EnvironmentTestState, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return nil, err
	}
	var state EnvironmentTestState
	err := s.withLocks(ctx, []lockKey{lockConfigurations, lockEnvironments}, nil, func() error {
		env, err := s.store.EnvironmentConfigByIDOrSlug(ref)
		if err != nil {
			return err
		}
		if env.DeletedAt != nil {
			return notFound("environment not found", nil)
		}
		c, err := s.store.ConfigurationByIDOrSlug("xsmall")
		if err != nil {
			return err
		}
		if c.DeletedAt != nil {
			return notFound("xsmall configuration not found", nil)
		}
		steps := appendBootstrapSteps(nil, env.BootstrapCommands, "bootstrap", "environment", env.ID, env.Slug)
		if len(steps) == 0 {
			return invalidArgument("environment has no commands to test", nil)
		}
		commands := make([]string, len(steps))
		for i, step := range steps {
			commands[i] = step.Command
		}
		id, now := newID(), s.now()
		node := Node{ID: id, Slug: environmentTestSlug(id), SandboxName: environmentTestSlug(id),
			ConfigurationID: c.ID, ConfigurationSlug: c.Slug, Image: c.Image, VCPUs: c.VCPUs, MemoryMiB: c.MemoryMiB, DiskMiB: c.DiskMiB,
			Runtime: RuntimeVM, Provider: ProviderLima, DirectoryPath: s.environmentTestWorkspace(id),
			WorkspaceMode: WorkspaceModeCopy, GuestWorkspacePath: "/tmp/codelima-environment-test",
			AgentProfileName: "environment-test", Environments: []string{env.Slug}, BootstrapCommands: commands,
			Status: NodeStatusCreated, CreatedAt: now, UpdatedAt: now}
		state = EnvironmentTestState{EnvironmentID: env.ID, EnvironmentSlug: env.Slug, CleanupPending: true,
			Plan: &EnvironmentTestPlan{Node: node, Bootstrap: BootstrapState{AgentProfileName: node.AgentProfileName, BootstrapCommands: commands, Steps: steps}}}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if len(data) > 128<<10 {
			return invalidArgument("environment test recipe exceeds 128 KiB", nil)
		}
		return nil
	})
	return &state, err
}

func environmentTestKeys(state *EnvironmentTestState) []string {
	n := state.Plan.Node
	return []string{"environment-test:" + state.EnvironmentID, "node:" + n.ID, "slug:" + n.Slug}
}

func (s *Service) validateEnvironmentTestPlan(plan *EnvironmentTestPlan) error {
	if plan == nil {
		return errors.New("environment test plan is unavailable")
	}
	n := plan.Node
	if id, err := uuid.Parse(n.ID); err != nil || id.String() != n.ID {
		return errors.New("invalid test node identity")
	}
	if n.Slug != environmentTestSlug(n.ID) || n.SandboxName != n.Slug || n.DirectoryPath != s.environmentTestWorkspace(n.ID) ||
		n.WorkspaceMode != WorkspaceModeCopy || n.WorkspaceMountPath != "" || n.ImportHostAuth || len(n.Ports) != 0 {
		return errors.New("environment test ownership does not match its private node")
	}
	return nil
}

func (s *Service) createEnvironmentTestNode(ctx context.Context, plan *EnvironmentTestPlan) error {
	if err := s.validateEnvironmentTestPlan(plan); err != nil {
		return err
	}
	if err := s.ensureReady(ctx, true); err != nil {
		return err
	}
	n := plan.Node
	var token *nodeOperationToken
	err := s.withLocks(ctx, []lockKey{lockNodes}, []string{n.ID}, func() error {
		if _, err := os.Lstat(s.store.nodeDir(n.ID)); !errors.Is(err, os.ErrNotExist) {
			return preconditionFailed("test node identity is already occupied", nil)
		}
		if err := s.ensureUniqueNodeSlug(n.Slug); err != nil {
			return err
		}
		if _, err := s.generateSandboxName(n.Slug); err != nil {
			return err
		}
		claim, err := s.claimNodeOperation("node.create", n.ID)
		if err != nil {
			return err
		}
		if err := s.store.SaveIncompleteNodeReference(n.ID, n.SandboxName); err != nil {
			claim.release()
			return err
		}
		token = claim
		return nil
	})
	if err != nil {
		return err
	}
	defer token.release()
	// This private directory is never mounted. Copying its empty contents is the
	// only internal exception to the public CODELIMA_HOME workspace exclusion.
	if err := os.Mkdir(n.DirectoryPath, 0700); err != nil {
		return err
	}
	_, err = s.createReservedNode(ctx, n, plan.Bootstrap)
	return err
}

func (s *Service) cleanupEnvironmentTest(ctx context.Context, plan *EnvironmentTestPlan) error {
	if err := s.validateEnvironmentTestPlan(plan); err != nil {
		return err
	}
	n := plan.Node
	stored, err := s.store.NodeByID(n.ID)
	if err == nil {
		if stored.ID != n.ID || stored.SandboxName != n.SandboxName || stored.DirectoryPath != n.DirectoryPath {
			return preconditionFailed("test node ownership changed; refusing cleanup", nil)
		}
		// A previous cleanup may already have deleted the VM. Never delete its
		// old name again: another node could now own that runtime name.
		if stored.DeletedAt == nil {
			if err := s.checkEnvironmentTestInstanceOwner(n); err != nil {
				return err
			}
			if _, err := s.deleteNode(ctx, n.ID, false); err != nil {
				return err
			}
		}
		return os.RemoveAll(n.DirectoryPath)
	}
	if !IsNotFound(err) {
		return err
	}
	claim, err := s.claimNodeOperation("node.delete", n.ID)
	if err != nil {
		return err
	}
	defer claim.release()
	item, err := s.store.incompleteNodeMetadata(n.ID)
	if err != nil {
		return err
	}
	if item.InstanceRefPath == "" {
		if _, err := os.Lstat(s.store.nodeDir(n.ID)); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return preconditionFailed("test node ownership record is unavailable", nil)
	}
	if item.SandboxName != n.SandboxName {
		return preconditionFailed("test instance ownership changed; refusing cleanup", nil)
	}
	if err := s.checkEnvironmentTestInstanceOwner(n); err != nil {
		return err
	}
	if err := s.sandbox.Delete(ctx, n); err != nil {
		return err
	}
	return s.withLocks(ctx, []lockKey{lockNodes}, []string{n.ID}, func() error {
		return s.store.RemoveIncompleteNodeMetadata([]IncompleteNodeMetadata{item})
	})
}

func (s *Service) checkEnvironmentTestInstanceOwner(n Node) error {
	data, err := os.ReadFile(s.store.nodeInstanceIndexPath(n.SandboxName))
	if errors.Is(err, os.ErrNotExist) {
		// The owned reference survives an incomplete index write.
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) != n.ID {
		return preconditionFailed("test instance belongs to another node; refusing cleanup", nil)
	}
	return nil
}

func environmentTestOutcome(ctx context.Context, err error) string {
	switch {
	case errors.Is(context.Cause(ctx), errNodeOperationsClosed):
		return "interrupted"
	case errors.Is(context.Cause(ctx), errNodeOperationCanceled):
		return "canceled"
	case err != nil:
		return "failed"
	default:
		return "succeeded"
	}
}

func (m *nodeOperations) runEnvironmentTest(ctx context.Context, operation *runningNodeOperation, recovery bool) {
	output := newNodeOperationOutput(m, operation)
	service := m.service.withIO(m.service.stdout, m.service.stderr)
	service.operationOutput = output
	service.operationPhase = func(id, detail string, skipped bool) error { return m.advancePhase(operation, id, detail, skipped) }
	m.mu.Lock()
	plan := cloneNodeOperation(operation.record).EnvironmentTest.Plan
	m.mu.Unlock()
	var runErr error
	if !recovery {
		runErr = service.advanceOperationPhase("create", "create private xsmall test VM", false)
		if runErr == nil {
			creation := service.withIO(operationOutputTee(service.stdout, output), operationOutputTee(service.stderr, output))
			runErr = creation.createEnvironmentTestNode(ctx, plan)
		}
		if runErr == nil {
			runErr = service.advanceOperationPhase("prepare", "run frozen environment commands", false)
			if runErr == nil {
				_, runErr = service.NodeStart(ctx, plan.Node.ID)
			}
		}
		m.mu.Lock()
		state := operation.record.EnvironmentTest
		state.Outcome = environmentTestOutcome(ctx, runErr)
		if runErr != nil {
			state.Error = boundedOperationText(runErr.Error(), 4096)
			wire := daemon.AsRPCError(toDaemonError(runErr))
			state.ErrorCode, state.ErrorCategory = wire.Code, wire.Category
		}
		now := m.service.now()
		finishNodeOperationPhases(&operation.record, state.Outcome, &now)
		runErr = errors.Join(runErr, m.persist(operation.record))
		m.mu.Unlock()
	}
	// Cancellation of execution never cancels teardown. Only daemon shutdown
	// interrupts cleanup, leaving its durable intent for the next daemon.
	var cleanupErr error
	for {
		_ = service.advanceOperationPhase("cleanup", "delete disposable VM and workspace", false)
		cleanupCtx, cancel := context.WithTimeout(m.cleanupCtx, 2*time.Minute)
		cleanupErr = service.cleanupEnvironmentTest(cleanupCtx, plan)
		cancel()
		if cleanupErr == nil {
			break // Persist cleanup completion together with the final outcome.
		}
		m.mu.Lock()
		state := operation.record.EnvironmentTest
		state.CleanupError = boundedOperationText(cleanupErr.Error(), 4096)
		cleanupDetail := state.CleanupError
		persistErr := m.persist(operation.record)
		m.mu.Unlock()
		if m.onChange != nil {
			m.onChange()
		}
		if persistErr != nil {
			runErr = errors.Join(runErr, persistErr)
		}
		if m.cleanupCtx.Err() != nil {
			break
		}
		_ = service.advanceOperationPhase("cleanup", "cleanup retry: "+cleanupDetail, false)
		_, _ = fmt.Fprintf(output, "Test cleanup failed; retrying: %s\n", cleanupErr)
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-m.cleanupCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if m.cleanupCtx.Err() != nil {
			break
		}
	}
	runErr = errors.Join(runErr, output.Close())
	m.mu.Lock()
	state := operation.record.EnvironmentTest
	if cleanupErr == nil {
		state.CleanupPending, state.CleanupError = false, ""
	}
	operation.record.State, operation.record.Error = state.Outcome, state.Error
	operation.record.ErrorCode, operation.record.ErrorCategory = state.ErrorCode, state.ErrorCategory
	now := m.service.now()
	operation.record.FinishedAt = &now
	cleanupState := "succeeded"
	if cleanupErr != nil {
		operation.record.State, operation.record.Error = "interrupted", "test cleanup is pending: "+state.CleanupError
		operation.record.FinishedAt = nil
		cleanupState = "interrupted"
	} else if runErr != nil && operation.record.State == "succeeded" {
		operation.record.State, operation.record.Error = "failed", boundedOperationText(runErr.Error(), 4096)
	}
	finishNodeOperationPhases(&operation.record, cleanupState, &now)
	operation.cancel = nil
	if err := m.persist(operation.record); err != nil {
		operation.record.State, operation.record.Error = "failed", boundedOperationText(err.Error(), 4096)
		m.service.log().Error("environment test completion could not be persisted", "operation", operation.record.ID, "error", err)
	}
	m.mu.Unlock()
	if m.onChange != nil {
		m.onChange()
	}
}
