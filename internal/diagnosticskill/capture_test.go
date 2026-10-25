package diagnosticskill

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnoseTerminalFreezesSkillCaptureDelegatesLiteralArgumentsWithoutBuilding(t *testing.T) {
	t.Parallel()
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(projectRoot, ".agents", "skills", "diagnose-codelima-terminal-freezes", "scripts", "capture.sh")
	root := t.TempDir()
	binary, log := filepath.Join(root, "capture binary"), filepath.Join(root, "arguments")
	source := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CODELIMA_CAPTURE_TEST_LOG\"\nexit 7\n"
	if err := os.WriteFile(binary, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	home, output := filepath.Join(root, "home with spaces"), filepath.Join(root, "capture 世界")
	command := exec.Command("/bin/sh", scriptPath, "--home", home, "--binary", binary, "--output", output, "--terminal-id", "term_one", "--sample-seconds", "0")
	command.Env = append(os.Environ(), "CODELIMA_CAPTURE_TEST_LOG="+log)
	data, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("collector exit was not preserved: %v, %s", err, data)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"--home", home, "daemon", "diagnose", "--output", output, "--terminal-id", "term_one", "--sample-seconds", "0", ""}, "\n")
	if string(args) != want {
		t.Fatalf("arguments changed: %q, want %q", args, want)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("wrapper created output before the collector", err)
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
