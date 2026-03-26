package codelima

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLimaCommandsFileAcceptsWrappedAndBareYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	wrappedPath := filepath.Join(dir, "wrapped.yaml")
	barePath := filepath.Join(dir, "bare.yaml")

	if err := os.WriteFile(wrappedPath, []byte("lima_commands:\n  start: \"{{binary}} start {{instance_name}} --vm-type=vz\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(wrapped) error = %v", err)
	}
	if err := os.WriteFile(barePath, []byte("start: \"{{binary}} start {{instance_name}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(bare) error = %v", err)
	}

	wrapped, err := loadLimaCommandsFile(wrappedPath)
	if err != nil {
		t.Fatalf("loadLimaCommandsFile(wrapped) error = %v", err)
	}
	if wrapped.Start != "{{binary}} start {{instance_name}} --vm-type=vz" {
		t.Fatalf("expected wrapped file start override, got %q", wrapped.Start)
	}

	bare, err := loadLimaCommandsFile(barePath)
	if err != nil {
		t.Fatalf("loadLimaCommandsFile(bare) error = %v", err)
	}
	if bare.Start != "{{binary}} start {{instance_name}} --tty=false" {
		t.Fatalf("expected bare file start override, got %q", bare.Start)
	}
}
