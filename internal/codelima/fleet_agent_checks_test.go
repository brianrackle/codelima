package codelima

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFleetPreviewAgentChecksFollowRecordedExecution(t *testing.T) {
	s, dir := newTestService(t)
	n, err := s.NodeCreate(context.Background(), NodeCreateInput{Slug: "agent-checks", Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	h := newDaemonHost(s)
	t.Cleanup(func() { _ = h.Close() })
	read := func() []fleetAgentCheck {
		t.Helper()
		page := requestFleetEvidence(t, h, fleetEvidenceRequest{Nodes: []string{n.ID}, RecentNode: n.ID})
		if page.Preview.AgentError != "" {
			t.Fatal(page.Preview.AgentError)
		}
		return page.Preview.AgentChecks
	}
	checks := read()
	if len(checks) != 2 || checks[0].Name != "codex" || checks[1].Name != "claude-code" || checks[0].State != BootstrapStepPending || checks[1].State != BootstrapStepPending {
		t.Fatalf("new node checks: %+v", checks)
	}
	fake := s.sandbox.(*fakeSandbox)
	fake.failCommand = defaultCodexValidationCommand
	if _, err := s.NodeStart(context.Background(), n.ID); err == nil {
		t.Fatal("expected codex validation failure")
	}
	checks = read()
	if checks[0].State != BootstrapStepFailed || checks[1].State != BootstrapStepPending {
		t.Fatalf("pending profile check hid failed environment check: %+v", checks)
	}
	fake.failCommand = ""
	if _, err := s.NodeRetry(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.store.nodeBootstrapPath(n.ID))
	if err != nil {
		t.Fatal(err)
	}
	checks = read()
	for _, check := range checks {
		if check.State != BootstrapStepSucceeded || !check.LoginUser || check.Inherited || check.FinishedAt == nil || check.ExitCode == nil || *check.ExitCode != 0 {
			t.Fatalf("missing actual recorded validation: %+v", check)
		}
	}
	// Editing the reusable environment must not rewrite a frozen result.
	env, err := s.store.EnvironmentConfigByIDOrSlug("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	env.BootstrapCommands = []string{"false"}
	if err := s.store.SaveEnvironmentConfig(env); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[1].State != BootstrapStepSucceeded {
		t.Fatalf("current environment replaced frozen evidence: %+v", got)
	}
	after, err := os.ReadFile(s.store.nodeBootstrapPath(n.ID))
	if err != nil || string(after) != string(before) {
		t.Fatal("preview modified execution records", err)
	}
	if err := os.Remove(s.store.nodeBootstrapPath(n.ID)); err != nil {
		t.Fatal(err)
	}
	page := requestFleetEvidence(t, h, fleetEvidenceRequest{Nodes: []string{n.ID}, RecentNode: n.ID})
	if page.Preview.AgentError == "" || len(page.Preview.AgentChecks) != 2 || page.Preview.AgentChecks[0].State != BootstrapStepUnrecorded {
		t.Fatalf("missing records claimed success: %+v", page.Preview)
	}
}

func TestFleetPreviewAgentChecksRejectUnsupportedSuccess(t *testing.T) {
	now := time.Now().UTC()
	node := Node{ID: newID(), AgentProfileName: "codex-cli", Environments: []string{"codex", "claude-code"}}
	newState := func() BootstrapState {
		b := BootstrapState{AgentProfileName: node.AgentProfileName, Completed: true, BootstrapCommands: []string{defaultCodexValidationCommand, defaultClaudeCodeValidationCommand}, ValidationCommand: defaultCodexValidationCommand}
		b.ensureSteps()
		for i := range b.Steps {
			start, finish, exit := now.Add(-time.Minute), now.Add(-time.Second), 0
			b.Steps[i].State, b.Steps[i].AttemptID, b.Steps[i].Attempts = BootstrapStepSucceeded, newID(), 1
			b.Steps[i].ExecutionNodeID = node.ID
			b.Steps[i].StartedAt, b.Steps[i].FinishedAt, b.Steps[i].ExitCode = &start, &finish, &exit
		}
		return b
	}
	for _, tc := range []struct {
		name string
		edit func(*BootstrapState)
		want BootstrapStepState
	}{
		{"legacy completion", func(b *BootstrapState) { b.Steps = nil }, BootstrapStepUnrecorded},
		{"changed command", func(b *BootstrapState) { b.ValidationCommand = "changed" }, BootstrapStepUnrecorded},
		{"new validation pending", func(b *BootstrapState) {
			b.Steps[2] = BootstrapStep{Kind: "validation", Command: b.ValidationCommand, State: BootstrapStepPending}
		}, BootstrapStepPending},
		{"missing exit", func(b *BootstrapState) { b.Steps[2].ExitCode = nil }, BootstrapStepUnrecorded},
		{"missing execution", func(b *BootstrapState) { b.Steps[2].ExecutionNodeID = "" }, BootstrapStepUnrecorded},
		{"missing attempt", func(b *BootstrapState) { b.Steps[2].AttemptID = "" }, BootstrapStepUnrecorded},
		{"contradictory exit", func(b *BootstrapState) { code := 1; b.Steps[2].ExitCode = &code }, BootstrapStepUnrecorded},
		{"contradictory error", func(b *BootstrapState) { b.Steps[2].Error = "failed" }, BootstrapStepUnrecorded},
		{"clock reversal", func(b *BootstrapState) { ts := now.Add(-time.Hour); b.Steps[2].FinishedAt = &ts }, BootstrapStepUnrecorded},
		{"future result", func(b *BootstrapState) { ts := now.Add(time.Hour); b.Steps[2].FinishedAt = &ts }, BootstrapStepUnrecorded},
		{"orphaned running check", func(b *BootstrapState) { b.Steps[2].State = BootstrapStepRunning }, BootstrapStepInterrupted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newState()
			tc.edit(&b)
			checks := summarizeFleetAgentChecks(node, b, false, now)
			if len(checks) != 2 || checks[0].State != tc.want {
				t.Fatalf("got %+v, want codex %s", checks, tc.want)
			}
		})
	}
	b := newState()
	b.Steps[2].State = BootstrapStepRunning
	if got := summarizeFleetAgentChecks(node, b, true, now)[0]; got.State != BootstrapStepRunning {
		t.Fatalf("active running check lost: %+v", got)
	}
	b = newState()
	for i := range b.Steps {
		b.Steps[i].ExecutionNodeID = "parent-node"
	}
	checks := summarizeFleetAgentChecks(node, b, false, now)
	if !checks[0].Inherited || !checks[1].Inherited {
		t.Fatal("clone claimed local checks", checks)
	}
	b.Steps[2].ExecutionNodeID = node.ID
	checks = summarizeFleetAgentChecks(node, b, false, now)
	if checks[0].Inherited || !checks[1].Inherited {
		t.Fatal("child validation lost inherited distinction", checks)
	}
	*checks[0].FinishedAt = time.Time{}
	if b.Steps[2].FinishedAt.IsZero() {
		t.Fatal("projection aliases stored evidence")
	}
}

func TestFleetPreviewAgentChecksSeparateConfiguredProfileChecks(t *testing.T) {
	now, exit := time.Now().UTC(), 0
	node := Node{ID: newID(), AgentProfileName: "codex-cli", Environments: []string{"codex", "claude-code"}}
	b := BootstrapState{AgentProfileName: node.AgentProfileName, ValidationCommand: "command -v codex", Completed: true}
	b.ensureSteps()
	b.Steps[0] = BootstrapStep{Kind: "validation", Command: b.ValidationCommand, State: BootstrapStepSucceeded, Attempts: 1, AttemptID: newID(), ExecutionNodeID: node.ID, StartedAt: &now, FinishedAt: &now, ExitCode: &exit}
	checks := summarizeFleetAgentChecks(node, b, false, now)
	if len(checks) != 2 || checks[0].LoginUser || checks[0].State != BootstrapStepSucceeded || checks[1].State != BootstrapStepUnrecorded {
		t.Fatalf("lookup-only check claimed login-user execution: %+v", checks)
	}
	node.AgentProfileName = "custom\x1b[31m" + strings.Repeat("x", 500)
	b.AgentProfileName = node.AgentProfileName
	checks = summarizeFleetAgentChecks(node, b, false, now)
	if len(checks) != 3 || len(checks[2].Name) > 64 || strings.Contains(checks[2].Name, "\x1b") {
		t.Fatal("custom label is not bounded/inert", checks)
	}
	wire, err := json.Marshal(checks)
	if err != nil || strings.Contains(string(wire), b.ValidationCommand) || strings.Contains(string(wire), "attempt_id") {
		t.Fatalf("raw execution content escaped projection: %s %v", wire, err)
	}
}
