package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSettingsAreApplicationWide(t *testing.T) {
	home := t.TempDir()
	cfg := DefaultConfig(home)
	if cfg.Daemon.Restore != "respawn" || !cfg.Daemon.Autostart || !cfg.Daemon.VirtioFSReclaim || cfg.Daemon.VirtioFSReclaimThresholdPercent != 20 {
		t.Fatalf("unexpected daemon defaults: %+v", cfg.Daemon)
	}
	if cfg.DefaultImage == "" || cfg.DefaultAgentProfile == "" {
		t.Fatalf("internal seed defaults must be populated")
	}
	if got := cfg.Summary(); got["default_image"] != nil || got["default_agent_profile"] != nil {
		t.Fatalf("settings summary leaked configuration-owned fields: %+v", got)
	}
}

func TestSettingsFileContainsOnlyDaemonSettings(t *testing.T) {
	home := t.TempDir()
	cfg := DefaultConfig(home)
	store := NewStore(cfg)
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "_config", "settings.yaml"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "daemon:") || !strings.Contains(text, "restore: respawn") || !strings.Contains(text, "virtiofs_reclaim: true") || !strings.Contains(text, "virtiofs_reclaim_threshold_percent: 20") {
		t.Fatalf("settings file missing daemon settings:\n%s", text)
	}
	for _, forbidden := range []string{"default_image", "default_agent_profile", "default_ports", "snapshot", "runtime_commands"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("settings file contains configuration-owned/internal field %q:\n%s", forbidden, text)
		}
	}
}

func TestLoadConfigReadsSettingsYAML(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "_config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "_config", "settings.yaml"), []byte("daemon:\n  autostart: false\n  restore: forget\n  virtiofs_reclaim: false\n  virtiofs_reclaim_threshold_percent: 75\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Daemon.Autostart || cfg.Daemon.Restore != "forget" || cfg.Daemon.VirtioFSReclaim || cfg.Daemon.VirtioFSReclaimThresholdPercent != 75 {
		t.Fatalf("settings not loaded: %+v", cfg.Daemon)
	}
	if cfg.DefaultImage != DefaultConfig(home).DefaultImage {
		t.Fatalf("configuration seed default changed while loading settings")
	}
}

func TestValidateConfigRejectsUnsafeVirtioFSReclaimThresholds(t *testing.T) {
	t.Parallel()
	for _, threshold := range []int{0, 96} {
		cfg := DefaultConfig(t.TempDir())
		cfg.Daemon.VirtioFSReclaimThresholdPercent = threshold
		if err := validateConfig(cfg); err == nil {
			t.Fatalf("validateConfig() accepted threshold %d", threshold)
		}
	}
}
