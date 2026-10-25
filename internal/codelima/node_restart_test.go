package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestNodeRestartKeepsOwnershipAndCompletedBootstrap(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	ctx := context.Background()
	if _, err := s.NodeStart(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := s.sandbox.(*fakeSandbox)
	fake.calls = nil
	checkedOwnership := false
	s.operationPhase = func(id, _ string, _ bool) error {
		if id == "prepare" {
			checkedOwnership = true
			if _, err := s.NodeStop(ctx, node.ID); exitCodeForError(err) != ExitPreconditionFailed {
				t.Fatalf("restart released ownership between stop/start: %v", err)
			}
		}
		return nil
	}
	got, err := s.NodeRestart(ctx, node.ID)
	if err != nil || got.ID != node.ID || !nodeIsRunning(got) || !checkedOwnership {
		t.Fatal("restart failed or changed identity", got, err, checkedOwnership)
	}
	stop, start := slices.Index(fake.calls, "stop "+node.SandboxName), slices.Index(fake.calls, "start "+node.SandboxName)
	if stop < 0 || start <= stop {
		t.Fatal("restart did not stop before booting", fake.calls)
	}
	after, err := s.store.LoadBootstrapState(node.ID)
	if err != nil || !reflect.DeepEqual(before.Steps[:3], after.Steps[:3]) {
		t.Fatal("restart replayed completed bootstrap", before, after, err)
	}
	token, err := s.claimNodeOperation("test", node.ID)
	if err != nil {
		t.Fatal("restart retained its lifecycle claim", err)
	}
	token.release()
}

type restartStopSandbox struct {
	SandboxClient
	err         error
	unconfirmed bool
	cancel      context.CancelFunc
}

func (s restartStopSandbox) Stop(ctx context.Context, node Node) error {
	if s.err != nil || s.unconfirmed {
		return s.err
	}
	err := s.SandboxClient.Stop(ctx, node)
	if s.cancel != nil {
		s.cancel()
	}
	return err
}

func TestNodeRestartNeverBootsAfterFailedStopOrCancellation(t *testing.T) {
	for _, mode := range []string{"failed", "unconfirmed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			s, node := setupBootstrapStepsTest(t)
			if _, err := s.NodeStart(context.Background(), node.ID); err != nil {
				t.Fatal(err)
			}
			fake := s.sandbox.(*fakeSandbox)
			fake.calls = nil
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wrapper := restartStopSandbox{SandboxClient: fake}
			switch mode {
			case "failed":
				wrapper.err = errors.New("stop failed")
			case "unconfirmed":
				wrapper.unconfirmed = true
			case "canceled":
				wrapper.cancel = cancel
			}
			s.sandbox = wrapper
			if _, err := s.NodeRestart(ctx, node.ID); err == nil {
				t.Fatal("restart ignored unsuccessful stop/cancellation")
			}
			for _, call := range fake.calls {
				if strings.HasPrefix(call, "start ") {
					t.Fatal("restart booted after unsuccessful stop/cancellation", fake.calls)
				}
			}
		})
	}
}

func TestNodeRestartReceiptIncludesStopAndStartup(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	if _, err := s.NodeStart(context.Background(), node.ID); err != nil {
		t.Fatal(err)
	}
	ops := testNodeOperations(t, s)
	record, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "restart", Node: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	record = waitNodeOperation(t, ops, record.ID, "succeeded")
	for _, phase := range []string{"stop", "prepare", "boot", "validation", "ready"} {
		if p := operationPhaseForTest(t, record, phase); p.State != "succeeded" || p.StartedAt == nil || p.FinishedAt == nil {
			t.Fatal("restart phase lacks observed completion", p)
		}
	}
	if p := operationPhaseForTest(t, record, "bootstrap"); p.State != "skipped" {
		t.Fatal("restart did not report completed bootstrap as skipped", p)
	}
	latest, err := ops.LatestStart(node.ID)
	if err != nil || latest.ID != record.ID {
		t.Fatal("restart missing from current lifecycle receipt", latest, err)
	}
}

func TestNodeRestartCapturesStopAndBootOutputOnce(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	dir := t.TempDir()
	binary, stopped := filepath.Join(dir, "limactl"), filepath.Join(dir, "stopped")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'limactl version 2.1.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	lima := NewLimaClient(s.cfg.MetadataRoot)
	lima.Binary = binary
	running := fmt.Sprintf(`{"name":%q,"status":"Running"}`, node.SandboxName)
	stoppedJSON := fmt.Sprintf(`{"name":%q,"status":"Stopped"}`, node.SandboxName)
	lima.RuntimeCommands.List = []string{fmt.Sprintf("if test -f %s; then printf '%%s\\n' %s; else printf '%%s\\n' %s; fi", shellQuote(stopped), shellQuote(stoppedJSON), shellQuote(running))}
	lima.RuntimeCommands.Stop = []string{"printf 'RESTART-STOP-OUT\\n'; printf 'RESTART-STOP-ERR\\n' >&2; touch " + shellQuote(stopped)}
	lima.RuntimeCommands.Start = []string{"printf 'RESTART-BOOT-OUT\\n'; printf 'RESTART-BOOT-ERR\\n' >&2; exit 23"}
	s.sandbox = lima
	ops := testNodeOperations(t, s)
	record, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "restart", Node: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	record = waitNodeOperation(t, ops, record.ID, "failed")
	output := strings.Join(record.Output, "\n")
	for _, want := range []string{"RESTART-STOP-OUT", "RESTART-STOP-ERR", "RESTART-BOOT-OUT", "RESTART-BOOT-ERR"} {
		if strings.Count(output, want) != 1 {
			t.Fatal("restart dropped or duplicated output", want, output, record.Error)
		}
	}
	if operationPhaseForTest(t, record, "stop").State != "succeeded" || operationPhaseForTest(t, record, "boot").State != "failed" {
		t.Fatal("restart failure attributed to the wrong phase", record.Phases)
	}
}
