package codelima

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSelfHostProjectExampleIsSanitizedAndValid(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	examplePath := filepath.Join(filepath.Dir(currentFile), "..", "..", "examples", "self-host", "project.yaml")
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("ReadFile(example project) error = %v", err)
	}

	content := string(raw)
	if strings.Contains(content, "/Users/brianrackle/personal/codelima") {
		t.Fatalf("expected self-host example to avoid the original workspace path, got %s", content)
	}
	if strings.Contains(content, "usermod -aG kvm brianrackle") {
		t.Fatalf("expected self-host example to avoid a hard-coded local username, got %s", content)
	}

	var project Project
	if err := readYAMLFile(examplePath, &project); err != nil {
		t.Fatalf("readYAMLFile(example project) error = %v", err)
	}

	if project.ID != "019d25e8-6b6f-73ad-aa54-499f76f03f55" {
		t.Fatalf("expected stable project id, got %q", project.ID)
	}
	if project.Slug != "codelima" {
		t.Fatalf("expected slug codelima, got %q", project.Slug)
	}
	if project.WorkspacePath != "/path/to/codelima" {
		t.Fatalf("expected sanitized workspace path, got %q", project.WorkspacePath)
	}
	if project.AgentProfileName != "codex-cli" {
		t.Fatalf("expected codex-cli agent profile, got %q", project.AgentProfileName)
	}
	if got := strings.Join(project.EnvironmentConfigs, "|"); got != "codex" {
		t.Fatalf("expected codex environment config, got %q", got)
	}
	if project.DefaultRuntime != RuntimeVM {
		t.Fatalf("expected runtime %q, got %q", RuntimeVM, project.DefaultRuntime)
	}
	if project.DefaultProvider != ProviderLima {
		t.Fatalf("expected provider %q, got %q", ProviderLima, project.DefaultProvider)
	}
	if project.DefaultLimaTemplate != "template:default" {
		t.Fatalf("expected default template, got %q", project.DefaultLimaTemplate)
	}
	if got := strings.Join(project.LimaCommands.Create, "|"); got != "{{binary}} create -y --name {{instance_name}} --cpus=6 --memory=16 --disk=100 {{template_path}}" {
		t.Fatalf("expected create override, got %q", got)
	}
	if got := strings.Join(project.LimaCommands.Start, "|"); got != "{{binary}} start -y --set \".nestedVirtualization=true\" {{instance_name}}" {
		t.Fatalf("expected start override, got %q", got)
	}
	if got := strings.Join(project.LimaCommands.Bootstrap, "|"); got != `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"|echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> ~/.profile|eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"|sudo apt-get install -yq make build-essential bubblewrap|brew install lima|sudo usermod -aG kvm "$(id -un)"|newgrp kvm` {
		t.Fatalf("expected bootstrap overrides, got %q", got)
	}

	createdAt := time.Date(2026, time.March, 25, 16, 51, 22, 95210000, time.UTC)
	if !project.CreatedAt.Equal(createdAt) {
		t.Fatalf("expected created_at %s, got %s", createdAt.Format(time.RFC3339Nano), project.CreatedAt.Format(time.RFC3339Nano))
	}
	if !project.UpdatedAt.Equal(createdAt) {
		t.Fatalf("expected updated_at %s, got %s", createdAt.Format(time.RFC3339Nano), project.UpdatedAt.Format(time.RFC3339Nano))
	}
}
