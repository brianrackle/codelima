package codelima

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRuntimeCommandsFileAcceptsWrappedAndBareYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	wrappedPath := filepath.Join(dir, "wrapped.yaml")
	barePath := filepath.Join(dir, "bare.yaml")

	if err := os.WriteFile(wrappedPath, []byte("runtime_commands:\n  start:\n    - \"{{binary}} start {{sandbox_name}} --vm-type=vz\"\n    - \"{{binary}} start {{sandbox_name}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(wrapped) error = %v", err)
	}
	if err := os.WriteFile(barePath, []byte("start: \"{{binary}} start {{sandbox_name}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(bare) error = %v", err)
	}

	wrapped, err := loadRuntimeCommandsFile(wrappedPath)
	if err != nil {
		t.Fatalf("loadRuntimeCommandsFile(wrapped) error = %v", err)
	}
	if got := len(wrapped.Start); got != 2 {
		t.Fatalf("expected wrapped file to load both start overrides, got %v", wrapped.Start)
	}
	if wrapped.Start[0] != "{{binary}} start {{sandbox_name}} --vm-type=vz" || wrapped.Start[1] != "{{binary}} start {{sandbox_name}} --tty=false" {
		t.Fatalf("expected wrapped file start override, got %q", wrapped.Start)
	}

	bare, err := loadRuntimeCommandsFile(barePath)
	if err != nil {
		t.Fatalf("loadRuntimeCommandsFile(bare) error = %v", err)
	}
	if got := strings.Join(bare.Start, "|"); got != "{{binary}} start {{sandbox_name}} --tty=false" {
		t.Fatalf("expected bare file start override, got %q", bare.Start)
	}
}

func TestSDKRuntimeCommandsAcceptGuestCommandsAndRejectCLICustomization(t *testing.T) {
	t.Parallel()
	if err := validateSDKRuntimeCommandTemplates(RuntimeCommandTemplates{
		Bootstrap:            []string{"apt-get update"},
		WorkspaceSeedPrepare: []string{"mkdir -p {{target_path}}"},
	}); err != nil {
		t.Fatalf("validateSDKRuntimeCommandTemplates(guest commands) error = %v", err)
	}

	err := validateSDKRuntimeCommandTemplates(RuntimeCommandTemplates{Start: []string{"msb start --custom {{sandbox_name}}"}})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Category != "PreconditionFailed" {
		t.Fatalf("validateSDKRuntimeCommandTemplates(custom start) error = %#v", err)
	}
	if appErr.Fields["command"] != "start" {
		t.Fatalf("custom override error fields = %#v", appErr.Fields)
	}
}

func TestRemoveLegacyMSBCommandTemplatesDoesNotRemoveCustomization(t *testing.T) {
	t.Parallel()
	legacy := legacyMSBCommandTemplates()
	legacy.Start = []string{"custom start"}
	got := removeLegacyMSBCommandTemplates(legacy)
	if len(got.Version) != 0 || len(got.Create) != 0 || len(got.Clone) != 0 {
		t.Fatalf("removeLegacyMSBCommandTemplates() retained exact defaults: %#v", got)
	}
	if len(got.Start) != 1 || got.Start[0] != "custom start" {
		t.Fatalf("removeLegacyMSBCommandTemplates() removed customization: %#v", got.Start)
	}
}
