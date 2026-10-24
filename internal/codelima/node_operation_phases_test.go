package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestNodeOperationsCaptureBootOutputOnceAndFailBeforeCredentials(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	binary := filepath.Join(t.TempDir(), "limactl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'limactl version 2.1.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	lima := NewLimaClient(s.cfg.MetadataRoot)
	lima.Binary = binary
	lima.RuntimeCommands.List = []string{fmt.Sprintf("printf '%%s\\n' '%s'", fmt.Sprintf(`{"name":%q,"status":"Stopped"}`, node.SandboxName))}
	lima.RuntimeCommands.Start = []string{"printf 'BOOT-STDOUT\\n'; printf 'BOOT-STDERR\\n' >&2; exit 23"}
	s.sandbox = lima
	operations := testNodeOperations(t, s)
	record, err := operations.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	result := waitNodeOperation(t, operations, record.ID, "failed")
	output := strings.Join(result.Output, "\n")
	if strings.Count(output, "BOOT-STDOUT") != 1 || strings.Count(output, "BOOT-STDERR") != 1 {
		t.Fatal("boot streams lost or duplicated", output, result.Error)
	}
	if operationPhaseForTest(t, result, "boot").State != "failed" || operationPhaseForTest(t, result, "credentials").State != "pending" {
		t.Fatal(result.Phases)
	}
}

func TestNodeOperationsPhasePersistenceFailureStopsBeforeCommands(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	want := errors.New("phase storage unavailable")
	s.operationPhase = func(id, _ string, _ bool) error {
		if id == "bootstrap" {
			return want
		}
		return nil
	}
	if _, err := s.NodeStart(context.Background(), node.ID); !errors.Is(err, want) {
		t.Fatal(err)
	}
	fake := s.sandbox.(*fakeSandbox)
	if len(fake.shellCalls) != 0 {
		t.Fatal("commands ran without a persisted phase", fake.shellCalls)
	}
	stored, err := s.store.NodeByID(node.ID)
	if err != nil || stored.Status != NodeStatusFailed {
		t.Fatal("phase failure left the node provisioning", stored, err)
	}
	s.operationPhase = nil
	if _, err := s.NodeStart(context.Background(), node.ID); err != nil {
		t.Fatal("phase failure retained the liveness token", err)
	}
}

func operationPhaseForTest(t *testing.T, record NodeOperation, id string) NodeOperationPhase {
	t.Helper()
	for _, phase := range record.Phases {
		if phase.ID == id {
			return phase
		}
	}
	t.Fatalf("operation %s has no %s phase: %+v", record.ID, id, record.Phases)
	return NodeOperationPhase{}
}

func TestNodeOperationsPhasesTrackFailureRetryAndCompletedStarts(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	fake := s.sandbox.(*fakeSandbox)
	fake.failCommand = "step-second"
	operations := testNodeOperations(t, s)
	run := func(kind, state string) NodeOperation {
		t.Helper()
		record, err := operations.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: kind, Node: node.ID})
		if err != nil {
			t.Fatal(err)
		}
		return waitNodeOperation(t, operations, record.ID, state)
	}
	failed := run("start", "failed")
	boot := operationPhaseForTest(t, failed, "boot")
	bootstrap := operationPhaseForTest(t, failed, "bootstrap")
	validation := operationPhaseForTest(t, failed, "validation")
	if boot.State != "succeeded" || boot.StartedAt == nil || boot.FinishedAt == nil || bootstrap.State != "failed" || bootstrap.Detail != "step 2/3 · step-second" || bootstrap.FinishedAt == nil || validation.State != "pending" || validation.StartedAt != nil {
		t.Fatal("phase evidence does not match the failed command", failed.Phases)
	}
	steps, err := s.store.LoadBootstrapState(node.ID)
	if err != nil || bootstrap.StartedAt == nil || bootstrap.StartedAt.After(*steps.Steps[0].StartedAt) {
		t.Fatal("changing the current command restarted the phase clock", bootstrap, err)
	}
	var persisted NodeOperation
	if err := readJSONFile(filepath.Join(operations.dir, failed.ID+".json"), &persisted); err != nil || operationPhaseForTest(t, persisted, "bootstrap").State != "failed" {
		t.Fatal("phase evidence not retained", err)
	}
	fake.failCommand = ""
	retried := run("retry", "succeeded")
	if phase := operationPhaseForTest(t, retried, "boot"); phase.State != "skipped" || phase.StartedAt != nil || phase.FinishedAt != nil {
		t.Fatal("already-running VM invented boot timing", phase)
	}
	if operationPhaseForTest(t, retried, "bootstrap").State != "succeeded" || operationPhaseForTest(t, retried, "ready").State != "succeeded" {
		t.Fatal(retried.Phases)
	}
	again := run("start", "succeeded")
	if phase := operationPhaseForTest(t, again, "bootstrap"); phase.State != "skipped" || phase.StartedAt != nil {
		t.Fatal("completed bootstrap was presented as new work", phase)
	}
	if operationPhaseForTest(t, again, "validation").State != "succeeded" {
		t.Fatal("subsequent start omitted validation", again.Phases)
	}
	for _, summary := range operations.List() {
		if len(summary.Phases) != 0 {
			t.Fatal("list leaked phase body", summary)
		}
	}
}

func TestNodeOperationsPhasesKeepInterruptedFinishUnknown(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	s.sandbox = outputOperationSandbox{SandboxClient: s.sandbox}
	operations := testNodeOperations(t, s)
	record, err := operations.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		current, _ := operations.Get(record.ID)
		for _, phase := range current.Phases {
			if phase.ID == "bootstrap" && phase.State == "running" {
				return true
			}
		}
		return false
	}, "running phase")
	if err := operations.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := newNodeOperations(s)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recovered.Close(context.Background()) }()
	result, err := recovered.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	phase := operationPhaseForTest(t, result, "bootstrap")
	if result.State != "interrupted" || phase.State != "interrupted" || phase.StartedAt == nil || phase.FinishedAt != nil {
		t.Fatal("shutdown invented phase completion", result)
	}
}
