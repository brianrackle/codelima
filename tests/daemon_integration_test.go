//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima"
)

type envelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

type harness struct {
	t     *testing.T
	bin   string
	home  string
	work  string
	extra []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	bin := os.Getenv("CODELIMA_TEST_BIN")
	root := os.Getenv("CODELIMA_TEST_TMP")
	if bin == "" || root == "" {
		t.Fatal("CODELIMA_TEST_BIN and CODELIMA_TEST_TMP are required")
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	h := &harness{t: t, bin: bin, home: filepath.Join(root, "home-"+stamp), work: filepath.Join(root, "work-"+stamp)}
	if err := os.MkdirAll(h.work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.run(false, "daemon", "stop")
		_ = os.RemoveAll(h.home)
		_ = os.RemoveAll(h.work)
	})
	return h
}

func (h *harness) run(ok bool, args ...string) (string, int) {
	h.t.Helper()
	all := append([]string{"--home", h.home}, args...)
	cmd := exec.Command(h.bin, all...)
	cmd.Env = append(os.Environ(), h.extra...)
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errorAs(err, &exitErr) {
			h.t.Fatalf("run %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	if ok && code != 0 {
		h.t.Fatalf("run %v exited %d: %s", args, code, output)
	}
	return string(output), code
}

func (h *harness) json(args ...string) json.RawMessage {
	h.t.Helper()
	output, _ := h.run(true, append([]string{"--json"}, args...)...)
	var result envelope
	if err := json.Unmarshal([]byte(output), &result); err != nil || !result.OK {
		h.t.Fatalf("decode %v output %q: %v", args, output, err)
	}
	return result.Data
}

func (h *harness) createNode() string {
	h.t.Helper()
	store := codelima.NewStore(codelima.DefaultConfig(h.home))
	if err := store.EnsureLayout(); err != nil {
		h.t.Fatal(err)
	}
	configuration, err := store.ConfigurationByIDOrSlug(codelima.DefaultConfigurationSlug)
	if err != nil {
		h.t.Fatal(err)
	}
	now := time.Now().UTC()
	node := codelima.Node{
		ID:                 "integration-" + strconv.FormatInt(now.UnixNano(), 36),
		Slug:               "integration",
		ConfigurationID:    configuration.ID,
		ConfigurationSlug:  configuration.Slug,
		DirectoryPath:      h.work,
		Runtime:            codelima.RuntimeVM,
		Provider:           codelima.ProviderMicrosandbox,
		SandboxName:        "integration-" + strconv.FormatInt(now.UnixNano(), 36),
		Image:              configuration.Image,
		VCPUs:              configuration.VCPUs,
		MemoryMiB:          configuration.MemoryMiB,
		DiskMiB:            configuration.DiskMiB,
		Environments:       append([]string(nil), configuration.Environments...),
		Status:             codelima.NodeStatusCreated,
		AgentProfileName:   configuration.AgentProfileName,
		BootstrapCommands:  append([]string(nil), configuration.BootstrapCommands...),
		WorkspaceMode:      codelima.WorkspaceModeCopy,
		GuestWorkspacePath: h.work,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := store.SaveNode(node, codelima.BootstrapState{AgentProfileName: configuration.AgentProfileName}); err != nil {
		h.t.Fatal(err)
	}
	return node.ID
}

func TestDaemonLifecycleAndStaleRecovery(t *testing.T) {
	h := newHarness(t)
	h.createNode()
	sessionPath := filepath.Join(h.home, "_daemon", "session.json")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacySession := []byte("{\"version\":1,\"terminals\":[]}\n")
	if err := os.WriteFile(sessionPath, legacySession, 0o600); err != nil {
		t.Fatal(err)
	}
	h.run(true, "daemon", "start")
	var recoveredSession struct {
		Version   int               `json:"version"`
		Terminals []json.RawMessage `json:"terminals"`
	}
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &recoveredSession); err != nil || recoveredSession.Version != 2 || len(recoveredSession.Terminals) != 0 {
		t.Fatalf("recovered daemon session = %#v, %v", recoveredSession, err)
	}
	backups, err := filepath.Glob(sessionPath + ".unsupported-v1-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("legacy session backups = %v, want one", backups)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(legacySession) {
		t.Fatalf("legacy session backup = %q, want %q", backup, legacySession)
	}
	var status struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(h.json("daemon", "status"), &status); err != nil || status.PID <= 0 {
		t.Fatalf("status = %#v, %v", status, err)
	}
	if output, code := h.run(false, "daemon", "start"); code != 5 || !strings.Contains(output, "already running") {
		t.Fatalf("double start = %d, %q", code, output)
	}
	if err := syscall.Kill(status.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	h.run(true, "daemon", "start")
	h.run(true, "daemon", "stop")
}

func TestDaemonLiveHandoffAndRollback(t *testing.T) {
	t.Run("counter continuity", func(t *testing.T) {
		h := newHarness(t)
		nodeID := h.createNode()
		h.run(true, "daemon", "start")
		var terminal struct {
			TerminalID string `json:"terminal_id"`
		}
		if err := json.Unmarshal(h.json("terminal", "open", "node:"+nodeID, "--kind", "node-host-shell"), &terminal); err != nil {
			t.Fatal(err)
		}
		command := "i=0; while [ $i -lt 80 ]; do echo COUNT=$i; i=$((i+1)); sleep 0.02; done\r"
		h.run(true, "terminal", "send", terminal.TerminalID, "--text", command)
		time.Sleep(200 * time.Millisecond)
		h.run(true, "daemon", "update", h.bin)
		time.Sleep(500 * time.Millisecond)
		var read struct {
			TerminalID string `json:"terminal_id"`
			Text       string `json:"text"`
		}
		if err := json.Unmarshal(h.json("terminal", "read", terminal.TerminalID, "--source", "recent"), &read); err != nil {
			t.Fatal(err)
		}
		if read.TerminalID != terminal.TerminalID {
			t.Fatalf("terminal id changed: %q -> %q", terminal.TerminalID, read.TerminalID)
		}
		for i := 0; i < 30; i++ {
			needle := fmt.Sprintf("COUNT=%d", i)
			if !containsCounter(read.Text, needle) {
				t.Fatalf("handoff output lost %s:\n%s", needle, read.Text)
			}
		}
		h.run(true, "terminal", "close", terminal.TerminalID)
	})

	t.Run("import failure rolls back", func(t *testing.T) {
		h := newHarness(t)
		nodeID := h.createNode()
		h.extra = []string{"CODELIMA_HANDOFF_FAIL_IMPORT=1"}
		h.run(true, "daemon", "start")
		h.extra = nil
		var before struct {
			PID int `json:"pid"`
		}
		_ = json.Unmarshal(h.json("daemon", "status"), &before)
		var terminal struct {
			TerminalID string `json:"terminal_id"`
		}
		_ = json.Unmarshal(h.json("terminal", "open", "node:"+nodeID, "--kind", "node-host-shell"), &terminal)
		h.run(true, "terminal", "send", terminal.TerminalID, "--text", "i=0; while [ $i -lt 40 ]; do echo ROLLBACK=$i; i=$((i+1)); sleep 0.03; done\r")
		time.Sleep(200 * time.Millisecond)
		if output, code := h.run(false, "daemon", "update", h.bin); code != 6 || !strings.Contains(output, "injected import failure") {
			t.Fatalf("failed update = %d, %q", code, output)
		}
		time.Sleep(300 * time.Millisecond)
		var after struct {
			PID int `json:"pid"`
		}
		_ = json.Unmarshal(h.json("daemon", "status"), &after)
		if after.PID != before.PID {
			t.Fatalf("rollback replaced daemon pid %d with %d", before.PID, after.PID)
		}
		var read struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(h.json("terminal", "read", terminal.TerminalID, "--source", "recent"), &read)
		if !containsCounter(read.Text, "ROLLBACK=10") {
			t.Fatalf("terminal did not continue after rollback:\n%s", read.Text)
		}
		h.run(true, "terminal", "close", terminal.TerminalID)
	})
}

func containsCounter(text, needle string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == needle {
			return true
		}
	}
	return false
}

func errorAs(err error, target any) bool {
	switch value := target.(type) {
	case **exec.ExitError:
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			*value = exitErr
		}
		return ok
	default:
		return false
	}
}
