package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
	"go.rockorager.dev/vaxis"
	"gopkg.in/yaml.v3"
)

func TestBootstrapStepsFreezeOrderedSourcesAtCreation(t *testing.T) {
	s, dir := newTestService(t)
	ctx := context.Background()
	env, err := s.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: "step-tools", BootstrapCommands: []string{"first", "second"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ConfigurationCreate(ctx, ConfigurationCreateInput{Slug: "step-recipe", Environments: []string{env.Slug}, BootstrapCommands: []string{"third"}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.NodeCreate(ctx, NodeCreateInput{Slug: "step-node", Directory: dir, Configuration: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Steps) != 4 {
		t.Fatalf("steps=%+v", b.Steps)
	}
	for i, want := range []string{"first", "second", "third", b.ValidationCommand} {
		if b.Steps[i].Command != want || b.Steps[i].State != BootstrapStepPending || b.Steps[i].Attempts != 0 {
			t.Fatalf("step %d=%+v", i, b.Steps[i])
		}
	}
	if b.Steps[0].SourceID != env.ID || b.Steps[0].SourceName != env.Slug || b.Steps[0].SourceKind != "environment" || b.Steps[2].SourceID != c.ID || b.Steps[2].SourceKind != "template" || b.Steps[3].Kind != "validation" {
		t.Fatalf("sources=%+v", b.Steps)
	}
	env.BootstrapCommands = []string{"edited"}
	if err := s.store.SaveEnvironmentConfig(env); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.store.LoadBootstrapState(n.ID)
	if err != nil || loaded.Steps[0].Command != "first" {
		t.Fatal("frozen source changed")
	}
}

func setupBootstrapStepsTest(t *testing.T) (*Service, Node) {
	t.Helper()
	s, dir := newTestService(t)
	n, err := s.NodeCreate(context.Background(), NodeCreateInput{Slug: "step-test", Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	n.AuthImportCompleted, n.WorkspaceSeeded = true, true
	b := BootstrapState{AgentProfileName: n.AgentProfileName, BootstrapCommands: []string{"step-first", "step-second", "step-third"}, ValidationCommand: "step-validate"}
	n.BootstrapCommands = b.CombinedCommands()
	if err := s.store.SaveNode(n, b); err != nil {
		t.Fatal(err)
	}
	return s, n
}

func TestBootstrapStepsRetryOnlyFailedSuffixAndPersistAttempts(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	fake := s.sandbox.(*fakeSandbox)
	fake.failCommand = "step-second"
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected failure")
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Steps) != 4 {
		t.Fatalf("missing steps after failure: %+v", b)
	}
	if b.Steps[0].State != BootstrapStepSucceeded || b.Steps[1].State != BootstrapStepFailed || b.Steps[2].State != BootstrapStepPending {
		t.Fatalf("failed plan=%+v", b.Steps)
	}
	if b.Steps[1].StartedAt == nil || b.Steps[1].FinishedAt == nil || b.Steps[1].DurationMillis == nil || b.Steps[1].Error == "" || b.Steps[1].ExecutionNodeID != n.ID {
		t.Fatalf("missing evidence=%+v", b.Steps[1])
	}
	first := b.Steps[0]
	fake.failCommand = ""
	fake.mu.Lock()
	fake.shellCalls = nil
	fake.mu.Unlock()
	if _, err := s.NodeRetry(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	calls := slices.Clone(fake.shellCalls)
	fake.mu.Unlock()
	var commands []string
	for _, call := range calls {
		commands = append(commands, strings.Join(call.command, " "))
	}
	if len(commands) != 3 || strings.Contains(strings.Join(commands, "\n"), "step-first") {
		t.Fatalf("retry executed=%q", commands)
	}
	b, err = s.store.LoadBootstrapState(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Completed || b.Steps[0].AttemptID != first.AttemptID || b.Steps[0].Attempts != 1 || b.Steps[1].Attempts != 2 {
		t.Fatalf("retry history=%+v", b)
	}
	for _, step := range b.Steps {
		if step.State != BootstrapStepSucceeded {
			t.Fatalf("not complete: %+v", step)
		}
	}
	if _, err := s.NodeRetry(context.Background(), n.ID); err == nil {
		t.Fatal("ready node accepted failed-step retry")
	}
}

func TestBootstrapStepsOrdinaryStartRerunsIncompleteBootstrap(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	fake := s.sandbox.(*fakeSandbox)
	fake.failCommand = "step-second"
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected failure")
	}
	fake.failCommand = ""
	if _, err := s.NodeStart(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil || b.Steps[0].Attempts != 2 || b.Steps[1].Attempts != 2 {
		t.Fatalf("ordinary start did not rerun incomplete plan: %+v, %v", b, err)
	}
	if _, err := s.NodeStart(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	b, err = s.store.LoadBootstrapState(n.ID)
	if err != nil || b.Steps[0].Attempts != 2 || b.Steps[3].Attempts != 2 {
		t.Fatalf("later start reran bootstrap or skipped validation: %+v, %v", b, err)
	}
}

type canceledBootstrapSandbox struct{ SandboxClient }

func (s canceledBootstrapSandbox) Shell(ctx context.Context, _ Node, _ guestIdentity, _ []string, _ string, _ bool, _ ShellStreams) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestBootstrapStepsCancellationIsRecordedWithoutMakingRetrySkipSteps(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	s.sandbox = canceledBootstrapSandbox{SandboxClient: s.sandbox}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a durable running record is visible, avoiding timing guesses.
	done := make(chan error, 1)
	go func() { defer close(done); _, err := s.NodeStart(ctx, n.ID); done <- err }()
	t.Cleanup(func() { cancel(); <-done })
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		b, err := s.store.LoadBootstrapState(n.ID)
		return err == nil && len(b.Steps) > 0 && b.Steps[0].State == BootstrapStepRunning
	}, "bootstrap running record")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil || b.Steps[0].State != BootstrapStepCanceled || b.Steps[0].FinishedAt == nil {
		t.Fatalf("cancel evidence=%+v, %v", b, err)
	}
	if _, err := s.NodeRetry(context.Background(), n.ID); err == nil {
		t.Fatal("cancel was treated as a resumable failure")
	}
}

func TestBootstrapStepsLegacyAndEditedCommandsCannotBorrowSuccess(t *testing.T) {
	b := BootstrapState{BootstrapCommands: []string{"old command"}, ValidationCommand: "old validation", Completed: true}
	b.ensureSteps()
	for _, step := range b.Steps {
		if step.State != BootstrapStepUnrecorded || step.StartedAt != nil || step.Attempts != 0 {
			t.Fatalf("invented legacy history: %+v", step)
		}
	}
	if b.Steps[0].SourceKind != "frozen" {
		t.Fatal("legacy environment provenance was guessed")
	}
	b.Completed = false
	b.Steps[0].State = BootstrapStepSucceeded
	b.Steps[1].State = BootstrapStepFailed
	b.BootstrapCommands = []string{"repaired command"}
	if _, err := b.prepareSteps(true); err == nil {
		t.Fatal("changed plan reused old success evidence")
	}
	if b.Steps[0].Command != "repaired command" || b.Steps[0].State != BootstrapStepPending {
		t.Fatalf("new plan=%+v", b.Steps)
	}
}

func TestBootstrapStepsClonePreservesExecutionOriginAndRevalidates(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	ctx := context.Background()
	if _, err := s.NodeStart(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	child, err := s.NodeClone(ctx, NodeCloneInput{SourceNode: n.ID, NodeSlug: "steps-clone"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.LoadBootstrapState(child.ID)
	if err != nil || len(b.Steps) != 4 || b.Steps[0].ExecutionNodeID != n.ID || b.Steps[0].Attempts != 1 {
		t.Fatalf("clone evidence=%+v, %v", b, err)
	}
	if _, err := s.NodeStart(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	b, err = s.store.LoadBootstrapState(child.ID)
	if err != nil || b.Steps[0].ExecutionNodeID != n.ID || b.Steps[3].ExecutionNodeID != child.ID || b.Steps[3].Attempts != 2 {
		t.Fatalf("child execution=%+v, %v", b, err)
	}
	source, err := s.store.LoadBootstrapState(n.ID)
	if err != nil || source.Steps[3].Attempts != 1 || source.Steps[3].ExecutionNodeID != n.ID {
		t.Fatal("child validation changed the source history")
	}
}

func TestBootstrapStepsRetryValidationWithoutRepeatingBootstrap(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	fake := s.sandbox.(*fakeSandbox)
	if _, err := s.NodeStart(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	fake.failCommand = "step-validate"
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected validation failure")
	}
	fake.failCommand = ""
	if _, err := s.NodeRetry(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil || b.Steps[0].Attempts != 1 || b.Steps[3].Attempts != 3 {
		t.Fatalf("validation retry=%+v, %v", b, err)
	}
}

type bootstrapExitFailure struct{}

func (bootstrapExitFailure) Error() string { return "guest command exited" }
func (bootstrapExitFailure) ExitCode() int { return 23 }

func TestBootstrapStepsStoreExitCodeAndRoundTripBothMetadataFormats(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	s.sandbox = bootstrapOutputSandbox{SandboxClient: s.sandbox, err: bootstrapExitFailure{}}
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected exit failure")
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil || b.Steps[0].ExitCode == nil || *b.Steps[0].ExitCode != 23 {
		t.Fatalf("missing exit code: %+v, %v", b, err)
	}
	for _, codec := range []struct {
		name      string
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{{"JSON", json.Marshal, json.Unmarshal}, {"YAML", yaml.Marshal, yaml.Unmarshal}} {
		data, err := codec.marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var got BootstrapState
		if err := codec.unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Steps) != 4 || got.Steps[0].AttemptID != b.Steps[0].AttemptID || got.Steps[0].ExitCode == nil || *got.Steps[0].ExitCode != 23 {
			t.Fatalf("%s lost evidence: %+v", codec.name, got)
		}
	}
}

func TestBootstrapStepsInterruptedExecutionCanRetryWithoutInventedDuration(t *testing.T) {
	b := BootstrapState{BootstrapCommands: []string{"first", "second", "third"}}
	b.ensureSteps()
	b.Steps[0].State = BootstrapStepSucceeded
	b.Steps[1].State = BootstrapStepRunning
	started := time.Now().Add(-time.Minute)
	b.Steps[1].StartedAt = &started
	b.interruptRunningSteps()
	if b.Steps[1].State != BootstrapStepInterrupted || b.Steps[1].DurationMillis != nil || b.Steps[1].FinishedAt != nil {
		t.Fatal("crash invented an execution end time")
	}
	index, err := b.prepareSteps(true)
	if err != nil || index != 1 || b.Steps[0].State != BootstrapStepSucceeded || b.Steps[1].State != BootstrapStepInterrupted {
		t.Fatalf("interrupted retry index=%d, %v, %+v", index, err, b.Steps)
	}
}

func TestBootstrapStepsRejectedStartKeepsFailureAvailableForRetry(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	fake := s.sandbox.(*fakeSandbox)
	fake.failCommand = "step-second"
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected step failure")
	}
	fake.failCommand = ""
	s.sandbox = bootstrapUncachedFailureSandbox{SandboxClient: fake}
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected preflight rejection")
	}
	s.sandbox = fake
	if _, err := s.NodeRetry(context.Background(), n.ID); err != nil {
		t.Fatalf("preflight rejection erased retry evidence: %v", err)
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil || b.Steps[0].Attempts != 1 {
		t.Fatalf("rejected start repeated a completed step: %+v, %v", b, err)
	}
}

type bootstrapUncachedFailureSandbox struct{ SandboxClient }

func (bootstrapUncachedFailureSandbox) ListUncached(context.Context) ([]RuntimeObservation, error) {
	return nil, errors.New("uncached runtime preflight unavailable")
}

func TestBootstrapStepsCLIAndTUIRetryUseRecordedBoundary(t *testing.T) {
	for _, mode := range []string{"cli", "tui"} {
		t.Run(mode, func(t *testing.T) {
			s, n := setupBootstrapStepsTest(t)
			fake := s.sandbox.(*fakeSandbox)
			fake.failCommand = "step-second"
			if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
				t.Fatal("expected failure")
			}
			fake.failCommand = ""
			if mode == "cli" {
				if _, err := dispatchNode(context.Background(), s, []string{"retry", n.ID}); err != nil {
					t.Fatal(err)
				}
			} else {
				a, manager := newTestManager(t, 80, 24)
				node, err := s.NodeShow(context.Background(), n.ID)
				if err != nil {
					t.Fatal(err)
				}
				a.state, err = newTUIState([]Node{node}, manager)
				if err != nil {
					t.Fatal(err)
				}
				a.service, a.fleet = s, nil
				a.handleKey(vaxis.Key{Keycode: 'r', Text: "r"})
			}
			b, err := s.store.LoadBootstrapState(n.ID)
			if err != nil || !b.Completed || b.Steps[0].Attempts != 1 || b.Steps[1].Attempts != 2 {
				t.Fatalf("retry result=%+v, %v", b, err)
			}
		})
	}
}

func TestBootstrapStepsFailedRunningNodeStopKeyStopsVM(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	fake := s.sandbox.(*fakeSandbox)
	fake.failCommand = "step-second"
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected failure")
	}
	node, err := s.NodeShow(context.Background(), n.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, manager := newTestManager(t, 80, 24)
	a.state, err = newTUIState([]Node{node}, manager)
	if err != nil {
		t.Fatal(err)
	}
	a.service, a.fleet = s, nil
	config := s.cfg
	config.ConfirmStop = "never"
	a.preferences = &config
	a.handleKey(vaxis.Key{Keycode: 's', Text: "s"})
	stored, err := s.NodeShow(context.Background(), n.ID)
	if err != nil || nodeIsRunning(stored) {
		t.Fatalf("failed-node s did not stop VM: %+v, %v", stored, err)
	}
}
