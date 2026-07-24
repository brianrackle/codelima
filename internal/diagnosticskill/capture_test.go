package diagnosticskill

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnoseTerminalFreezesSkillCaptureIsReadOnlyAndContinuesAfterProbeFailure(t *testing.T) {
	t.Parallel()

	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(
		projectRoot,
		".agents",
		"skills",
		"diagnose-codelima-terminal-freezes",
		"scripts",
		"capture.sh",
	)
	testRoot := t.TempDir()
	home := filepath.Join(testRoot, "home")
	output := filepath.Join(testRoot, "capture")
	commandLog := filepath.Join(testRoot, "commands.log")
	if err := os.MkdirAll(filepath.Join(home, "_daemon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "_logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(home, "_daemon", "daemon.pid"):      "424242\n",
		filepath.Join(home, "_daemon", "daemon.identity"): `{"pid":424242,"version":"test"}` + "\n",
		filepath.Join(home, "_daemon", "session.json"):    `{"version":2,"terminals":[]}` + "\n",
		filepath.Join(home, "_daemon", "daemon.log"):      "daemon-log\n",
		filepath.Join(home, "_logs", "codelima.log"):      "tui-log\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	fakeBinary := filepath.Join(testRoot, "fake-codelima")
	fakeSource := `#!/bin/sh
printf '%s\n' "$*" >> "$CODELIMA_CAPTURE_TEST_LOG"
case "$*" in
  *"--version"*)
    printf 'test-version\n'
    ;;
  *"daemon status"*)
    printf '{"ok":true,"data":{"running":true,"pid":424242}}\n'
    ;;
  *"terminal list"*)
    printf '{"ok":true,"data":[{"terminal_id":"term_one"}]}\n'
    ;;
  *"daemon snapshot"*)
    printf '{"ok":true,"data":{"session":{"terminals":[]}}}\n'
    ;;
  *"terminal read term_one"*)
    if [ "${CODELIMA_CAPTURE_TEST_READ_OK:-}" = "1" ]; then
      printf '{"ok":true,"data":{"terminal_id":"term_one","generation":9}}\n'
    else
      printf 'terminal actor did not respond\n' >&2
      exit 7
    fi
    ;;
  *)
    printf 'unexpected invocation: %s\n' "$*" >&2
    exit 64
    ;;
esac
`
	if err := os.WriteFile(fakeBinary, []byte(fakeSource), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(
		"/bin/sh",
		scriptPath,
		"--home", home,
		"--binary", fakeBinary,
		"--output", output,
		"--terminal-id", "term_one",
		"--sample-seconds", "0",
	)
	command.Dir = projectRoot
	command.Env = append(os.Environ(), "CODELIMA_CAPTURE_TEST_LOG="+commandLog)
	outputBytes, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("capture script error = %v\n%s", err, outputBytes)
	}

	for _, relative := range []string{
		"capture.txt",
		"daemon-status.json",
		"terminal-list.json",
		"daemon-snapshot.json",
		"terminal-read.json",
		"terminal-read.err",
		"terminal-read.exit",
		"daemon.identity",
		"session.json",
		"daemon.log.tail",
		"codelima.log.tail",
		"summary.md",
	} {
		if _, err := os.Stat(filepath.Join(output, relative)); err != nil {
			t.Errorf("expected capture artifact %s: %v", relative, err)
		}
	}

	exitData, err := os.ReadFile(filepath.Join(output, "terminal-read.exit"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(exitData)); got != "7" {
		t.Fatalf("terminal read exit = %q, want 7", got)
	}

	summary, err := os.ReadFile(filepath.Join(output, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "control plane responded but the terminal actor probe failed") {
		t.Fatalf("summary did not classify the failed actor probe:\n%s", summary)
	}

	invocations, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	gotInvocations := string(invocations)
	for _, want := range []string{"daemon status", "terminal list", "daemon snapshot", "terminal read term_one"} {
		if !strings.Contains(gotInvocations, want) {
			t.Errorf("missing read-only invocation %q in:\n%s", want, gotInvocations)
		}
	}
	for _, forbidden := range []string{"daemon stop", "daemon update", "terminal send", "terminal close", "input takeover"} {
		if strings.Contains(gotInvocations, forbidden) {
			t.Errorf("capture invoked destructive or state-changing command %q:\n%s", forbidden, gotInvocations)
		}
	}

	healthyOutput := filepath.Join(testRoot, "healthy-capture")
	healthyCommand := exec.Command(
		"/bin/sh",
		scriptPath,
		"--home", home,
		"--binary", fakeBinary,
		"--output", healthyOutput,
		"--sample-seconds", "0",
	)
	healthyCommand.Dir = projectRoot
	healthyCommand.Env = append(
		os.Environ(),
		"CODELIMA_CAPTURE_TEST_LOG="+commandLog,
		"CODELIMA_CAPTURE_TEST_READ_OK=1",
	)
	healthyBytes, err := healthyCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("healthy capture script error = %v\n%s", err, healthyBytes)
	}
	probedID, err := os.ReadFile(filepath.Join(healthyOutput, "probed-terminal-id.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(probedID)); got != "term_one" {
		t.Fatalf("automatically probed terminal = %q, want term_one", got)
	}
	healthySummary, err := os.ReadFile(filepath.Join(healthyOutput, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(healthySummary), "control plane and selected terminal actor both responded") {
		t.Fatalf("healthy summary did not classify the client-side boundary:\n%s", healthySummary)
	}
}

func TestDiagnoseTerminalFreezesSkillCaptureHasValidShellSyntax(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(
		"..",
		"..",
		".agents",
		"skills",
		"diagnose-codelima-terminal-freezes",
		"scripts",
		"capture.sh",
	)
	output, err := exec.Command("/bin/sh", "-n", scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("sh -n %s: %v\n%s", scriptPath, err, output)
	}
	source, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"daemon stop", "daemon update", "terminal send", "terminal close", "input takeover", "kill "} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("capture script contains forbidden mutating operation %q", forbidden)
		}
	}
}

func TestDiagnoseTerminalFreezeMakeTargetDoesNotBuildOrInitialize(t *testing.T) {
	t.Parallel()

	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "-n", "diagnose-terminal-freeze")
	command.Dir = projectRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n diagnose-terminal-freeze: %v\n%s", err, output)
	}
	got := string(output)
	if !strings.Contains(got, "diagnose-codelima-terminal-freezes/scripts/capture.sh") {
		t.Fatalf("diagnostic make target did not invoke the skill capture script:\n%s", got)
	}
	for _, forbidden := range []string{"go build", "install_go.sh", "install_ghostty_vt.sh"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("diagnostic make target unexpectedly runs %q:\n%s", forbidden, got)
		}
	}
}
