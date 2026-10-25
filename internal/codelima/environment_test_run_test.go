package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestNodeOperationsEnvironmentTestRunsOnlyFrozenCommandsAndDeletesVM(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, _ := newTestService(t)
		ctx := context.Background()
		env, err := s.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: "isolated-test", BootstrapCommands: []string{"printf FIRST", "printf SECOND"}})
		if err != nil {
			t.Fatal(err)
		}
		real, err := s.NodeCreate(ctx, NodeCreateInput{Slug: "real-node", Directory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if fail {
			s.sandbox.(*fakeSandbox).failCommand = "printf FIRST"
		}
		ops := testNodeOperations(t, s)
		op, err := ops.Submit(ctx, NodeOperationRequest{ID: newID(), Kind: "test-environment", Environment: env.ID})
		if err != nil {
			t.Fatal(err)
		}
		state := "succeeded"
		if fail {
			state = "failed"
		}
		finished := waitNodeOperation(t, ops, op.ID, state)
		if finished.EnvironmentTest == nil || finished.EnvironmentTest.CleanupPending || finished.NodeID == real.ID {
			t.Fatal("test completed without owned cleanup", finished)
		}
		node, err := s.store.NodeByID(finished.NodeID)
		if err != nil || node.DeletedAt == nil || node.ImportHostAuth || node.WorkspaceMode != WorkspaceModeCopy || len(node.Ports) != 0 || node.WorkspaceMountPath != "" {
			t.Fatal("test leaked a live VM or host resources", node, err)
		}
		if _, err := os.Stat(node.DirectoryPath); !os.IsNotExist(err) {
			t.Fatal("test workspace survived cleanup", err)
		}
		b, err := s.store.LoadBootstrapState(node.ID)
		if err != nil || !slices.Equal(b.BootstrapCommands, env.BootstrapCommands) || len(b.InstallCommands) != 0 || b.ValidationCommand != "" || len(b.Steps) != 2 {
			t.Fatal("test ran unrelated agent/template commands", b, err)
		}
		if b.Steps[0].SourceID != env.ID || b.Steps[0].ExecutionNodeID != node.ID {
			t.Fatal("test lost execution provenance", b.Steps)
		}
		if fail && b.Steps[1].StartedAt != nil {
			t.Fatal("test continued after a failed command")
		}
		current, err := s.store.NodeByID(real.ID)
		if err != nil || current.DeletedAt != nil || current.Status != real.Status {
			t.Fatal("test touched a real node", current, err)
		}
		if !strings.HasPrefix(node.DirectoryPath, filepath.Join(s.cfg.MetadataRoot, "nodes", node.ID)+string(os.PathSeparator)) {
			t.Fatal("workspace escaped the owned node", node.DirectoryPath)
		}
	}
}

type environmentTestSandbox struct {
	SandboxClient
	entered    chan Node
	failDelete atomic.Bool
	deletes    atomic.Int32
}

func (s *environmentTestSandbox) Shell(ctx context.Context, n Node, identity guestIdentity, command []string, workdir string, interactive bool, streams ShellStreams) error {
	if strings.Contains(strings.Join(command, " "), "ENV_TEST_BLOCK") {
		if streams.Stdout != nil {
			_, _ = fmt.Fprintln(streams.Stdout, "TEST-LIVE-OUTPUT")
		}
		select {
		case s.entered <- n:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return s.SandboxClient.Shell(ctx, n, identity, command, workdir, interactive, streams)
}

func (s *environmentTestSandbox) Delete(ctx context.Context, n Node) error {
	s.deletes.Add(1)
	if s.failDelete.Load() {
		return errors.New("fixture delete unavailable")
	}
	return s.SandboxClient.Delete(ctx, n)
}

func TestNodeOperationsEnvironmentTestCancelSurvivesCallerExitAndCleans(t *testing.T) {
	s, _ := newTestService(t)
	sandbox := &environmentTestSandbox{SandboxClient: s.sandbox, entered: make(chan Node, 1)}
	s.sandbox = sandbox
	env, err := s.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{Slug: "blocking", BootstrapCommands: []string{"printf ENV_TEST_BLOCK"}})
	if err != nil {
		t.Fatal(err)
	}
	ops := testNodeOperations(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	req := NodeOperationRequest{ID: newID(), Kind: "test-environment", Environment: env.ID}
	op, err := ops.Submit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	var node Node
	select {
	case node = <-sandbox.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("test stopped when the requester left")
	}
	if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "delete", Node: node.ID}); err == nil {
		t.Fatal("test VM was not reserved during execution")
	}
	if repeated, err := ops.Submit(context.Background(), req); err != nil || repeated.ID != op.ID {
		t.Fatal("repeat delivery did not reuse receipt", err)
	}
	if _, err := s.EnvironmentConfigUpdate(context.Background(), env.ID, EnvironmentConfigUpdateInput{BootstrapCommands: []string{"must not replace admitted commands"}}); err != nil {
		t.Fatal(err)
	}
	changedImage := "template:changed-after-admission"
	if _, err := s.ConfigurationUpdate(context.Background(), "xsmall", ConfigurationUpdateInput{Image: &changedImage}); err != nil {
		t.Fatal(err)
	}
	frozen, err := ops.Get(op.ID)
	if err != nil || !slices.Equal(frozen.EnvironmentTest.Plan.Bootstrap.BootstrapCommands, env.BootstrapCommands) || frozen.EnvironmentTest.Plan.Node.Image != node.Image {
		t.Fatal("source edits changed an admitted test", frozen, err)
	}
	if err := ops.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	result := waitNodeOperation(t, ops, op.ID, "canceled")
	if result.EnvironmentTest.CleanupPending || !strings.Contains(strings.Join(result.Output, "\n"), "TEST-LIVE-OUTPUT") {
		t.Fatal("cancel lost cleanup or captured output", result)
	}
	stored, err := s.store.NodeByID(node.ID)
	if err != nil || stored.DeletedAt == nil {
		t.Fatal("canceled test retained VM", stored, err)
	}
}

func TestNodeOperationsEnvironmentTestRejectsEmptyRecipesAndRealTargets(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	env, err := s.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: "empty-test"})
	if err != nil {
		t.Fatal(err)
	}
	ops := testNodeOperations(t, s)
	for _, req := range []NodeOperationRequest{
		{ID: newID(), Kind: "test-environment", Environment: env.ID},
		{ID: newID(), Kind: "test-environment", Environment: "codex", Node: newID()},
		{ID: newID(), Kind: "test-environment", Environment: "codex", Create: &NodeCreateInput{Slug: "injected"}},
	} {
		if _, err := ops.Submit(ctx, req); err == nil {
			t.Fatal("unsafe or empty test was admitted", req)
		}
	}
	nodes, err := s.store.ListNodes(true)
	if err != nil || len(nodes) != 0 {
		t.Fatal("rejection created a node", nodes, err)
	}
}

func TestNodeOperationsEnvironmentCleanupRecoversWithoutExecutingAgain(t *testing.T) {
	s, _ := newTestService(t)
	sandbox := &environmentTestSandbox{SandboxClient: s.sandbox}
	sandbox.failDelete.Store(true)
	s.sandbox = sandbox
	env, err := s.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{Slug: "recovery", BootstrapCommands: []string{"printf ONCE"}})
	if err != nil {
		t.Fatal(err)
	}
	ops := testNodeOperations(t, s)
	op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "test-environment", Environment: env.ID})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		r, err := ops.Get(op.ID)
		return err == nil && r.EnvironmentTest.Outcome == "succeeded" && r.EnvironmentTest.CleanupError != ""
	}, "failed cleanup retained intent")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := ops.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, err := ops.Get(op.ID)
	if err != nil || !stopped.EnvironmentTest.CleanupPending || stopped.State != "interrupted" {
		t.Fatal("shutdown lost cleanup ownership", stopped, err)
	}
	// Pending teardown never expires, even if a machine is offline for weeks.
	if operationExpired(stopped, s.now().Add(30*24*time.Hour)) {
		t.Fatal("retention discarded pending cleanup")
	}
	// The runtime can remain unavailable across a daemon restart. New cleanup
	// diagnostics must append to the retained command output, not replace it.
	retrying := testNodeOperations(t, s)
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		r, err := retrying.Get(op.ID)
		return err == nil && len(r.Output) > len(stopped.Output) &&
			slices.Equal(r.Output[:len(stopped.Output)], stopped.Output)
	}, "recovered cleanup appends output without discarding the command receipt")
	sandbox.failDelete.Store(false)
	var finished NodeOperation
	testutil.WaitForCondition(t, 7*time.Second, func() bool {
		finished, err = retrying.Get(op.ID)
		return err == nil && finished.State == "succeeded"
	}, "recovered cleanup succeeds once the runtime is repaired")
	if finished.EnvironmentTest.CleanupPending {
		t.Fatal("recovery did not finish cleanup")
	}
	for _, phase := range finished.Phases {
		if phase.ID == "cleanup" && strings.Contains(phase.Detail, "retry") {
			t.Fatal("successful cleanup still advertises an earlier failure", phase)
		}
	}
	b, err := s.store.LoadBootstrapState(op.NodeID)
	if err != nil || b.Steps[0].Attempts != 1 {
		t.Fatal("cleanup recovery executed commands again", b, err)
	}
	if _, err := os.Stat(finished.EnvironmentTest.Plan.Node.DirectoryPath); !os.IsNotExist(err) {
		t.Fatal("workspace survived recovered cleanup", err)
	}
}

func TestEnvironmentTestCleanupRefusesForeignInstanceOwner(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			s, _ := newTestService(t)
			ctx := context.Background()
			env, err := s.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: "ownership", BootstrapCommands: []string{"true"}})
			if err != nil {
				t.Fatal(err)
			}
			state, err := s.prepareEnvironmentTest(ctx, env.ID)
			if err != nil {
				t.Fatal(err)
			}
			n := state.Plan.Node
			if complete {
				err = s.createEnvironmentTestNode(ctx, state.Plan)
			} else {
				err = s.store.SaveIncompleteNodeReference(n.ID, n.SandboxName)
			}
			if err != nil {
				t.Fatal(err)
			}
			foreign := newID()
			if err := atomicWriteFile(s.store.nodeInstanceIndexPath(n.SandboxName), []byte(foreign+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			sandbox := &environmentTestSandbox{SandboxClient: s.sandbox}
			s.sandbox = sandbox
			if err := s.cleanupEnvironmentTest(ctx, state.Plan); err == nil || sandbox.deletes.Load() != 0 {
				t.Fatal("cleanup deleted an instance claimed by another node", err)
			}
			if err := atomicWriteFile(s.store.nodeInstanceIndexPath(n.SandboxName), []byte(n.ID+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := s.cleanupEnvironmentTest(ctx, state.Plan); err != nil {
				t.Fatal(err)
			}
			if err := s.cleanupEnvironmentTest(ctx, state.Plan); err != nil || sandbox.deletes.Load() != 1 {
				t.Fatal("repeated cleanup deleted the runtime again", err, sandbox.deletes.Load())
			}
		})
	}
}

func TestNodeOperationsEnvironmentRecoveryAfterCreateOrDeleteCrash(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			s, _ := newTestService(t)
			ctx := context.Background()
			env, err := s.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: "crash-test", BootstrapCommands: []string{"never execute on recovery"}})
			if err != nil {
				t.Fatal(err)
			}
			state, err := s.prepareEnvironmentTest(ctx, env.ID)
			if err != nil {
				t.Fatal(err)
			}
			n := state.Plan.Node
			if deleted {
				if err := s.createEnvironmentTestNode(ctx, state.Plan); err != nil {
					t.Fatal(err)
				}
				if err := s.cleanupEnvironmentTest(ctx, state.Plan); err != nil {
					t.Fatal(err)
				}
				state.Outcome = "succeeded"
			} else if err := s.store.SaveIncompleteNodeReference(n.ID, n.SandboxName); err != nil {
				t.Fatal(err)
			}
			record := NodeOperation{ID: newID(), NodeID: n.ID, NodeSlug: n.Slug, State: "running", EnvironmentTest: state,
				Request: NodeOperationRequest{Kind: "test-environment", Environment: env.ID}, CreatedAt: s.now(), Phases: initialNodeOperationPhases("test-environment")}
			record.Request.ID = record.ID
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(s.cfg.MetadataRoot, "_operations")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := atomicWriteFile(filepath.Join(dir, record.ID+".json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			sandbox := &environmentTestSandbox{SandboxClient: s.sandbox}
			s.sandbox = sandbox
			ops := testNodeOperations(t, s)
			want := "interrupted"
			if deleted {
				want = "succeeded"
			}
			finished := waitNodeOperation(t, ops, record.ID, want)
			if finished.EnvironmentTest.CleanupPending || deleted && sandbox.deletes.Load() != 0 || !deleted && sandbox.deletes.Load() != 1 {
				t.Fatal("crash recovery lost cleanup or deleted a released name", finished, sandbox.deletes.Load())
			}
			if deleted {
				b, err := s.store.LoadBootstrapState(n.ID)
				if err != nil || b.Steps[0].Attempts != 0 {
					t.Fatal("recovery ran commands", b, err)
				}
			} else if _, err := os.Stat(s.store.nodeDir(n.ID)); !os.IsNotExist(err) {
				t.Fatal("incomplete node survived", err)
			}
		})
	}
}
