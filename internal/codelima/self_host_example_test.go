package codelima

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSelfHostConfigurationExampleIsSanitizedAndValid(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	examplePath := filepath.Join(filepath.Dir(currentFile), "..", "..", "examples", "self-host", "configuration.yaml")
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("ReadFile(example configuration) error = %v", err)
	}

	content := string(raw)
	if strings.Contains(content, "/Users/brianrackle/personal/codelima") {
		t.Fatalf("expected self-host example to avoid the original workspace path, got %s", content)
	}
	if strings.Contains(content, "usermod -aG kvm brianrackle") {
		t.Fatalf("expected self-host example to avoid a hard-coded local username, got %s", content)
	}

	var configuration Configuration
	if err := readYAMLFile(examplePath, &configuration); err != nil {
		t.Fatalf("readYAMLFile(example configuration) error = %v", err)
	}

	if configuration.ID != "019d25e8-6b6f-73ad-aa54-499f76f03f55" {
		t.Fatalf("expected stable configuration id, got %q", configuration.ID)
	}
	if configuration.Slug != "codelima" {
		t.Fatalf("expected slug codelima, got %q", configuration.Slug)
	}
	if configuration.AgentProfileName != "codex-cli" {
		t.Fatalf("expected codex-cli agent profile, got %q", configuration.AgentProfileName)
	}
	if got := strings.Join(configuration.Environments, "|"); got != "codex" {
		t.Fatalf("expected codex environment, got %q", got)
	}
	if configuration.Image != "ghcr.io/superradcompany/debian-systemd:12" {
		t.Fatalf("expected microsandbox image, got %q", configuration.Image)
	}
	if got := strings.Join(configuration.BootstrapCommands, "|"); got != `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"|echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> ~/.profile|eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"|apt-get update && apt-get install -yq make build-essential bubblewrap curl|curl -fsSL https://install.microsandbox.dev | sh` {
		t.Fatalf("expected bootstrap overrides, got %q", got)
	}
	if configuration.VCPUs != 2 || configuration.MemoryMiB != 4096 || configuration.DiskMiB != 20480 {
		t.Fatalf("unexpected resources: %#v", configuration)
	}

	createdAt := time.Date(2026, time.March, 25, 16, 51, 22, 95210000, time.UTC)
	if !configuration.CreatedAt.Equal(createdAt) {
		t.Fatalf("expected created_at %s, got %s", createdAt.Format(time.RFC3339Nano), configuration.CreatedAt.Format(time.RFC3339Nano))
	}
	if !configuration.UpdatedAt.Equal(createdAt) {
		t.Fatalf("expected updated_at %s, got %s", createdAt.Format(time.RFC3339Nano), configuration.UpdatedAt.Format(time.RFC3339Nano))
	}
}
