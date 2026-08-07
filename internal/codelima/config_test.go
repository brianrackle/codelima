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
	if cfg.Daemon.Restore != "respawn" || !cfg.Daemon.Autostart || !cfg.Daemon.VirtioFSReclaim {
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
	if !strings.Contains(text, "daemon:") || !strings.Contains(text, "restore: respawn") || !strings.Contains(text, "virtiofs_reclaim: true") {
		t.Fatalf("settings file missing daemon settings:\n%s", text)
	}
	if strings.Contains(text, "virtiofs_reclaim_threshold_percent") {
		t.Fatalf("settings file still carries the retired reclaim threshold:\n%s", text)
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
	if cfg.Daemon.Autostart || cfg.Daemon.Restore != "forget" || cfg.Daemon.VirtioFSReclaim {
		t.Fatalf("settings not loaded: %+v", cfg.Daemon)
	}
	if cfg.DefaultImage != DefaultConfig(home).DefaultImage {
		t.Fatalf("configuration seed default changed while loading settings")
	}
}

// A settings.yaml written before the reclaim threshold was retired, or by a
// newer build that added keys this one has never heard of, must still load.
func TestLoadConfigToleratesRetiredAndUnknownSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "_config"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := "" +
		"future_top_level_key: 7\n" +
		"daemon:\n" +
		"  autostart: false\n" +
		"  restore: forget\n" +
		"  virtiofs_reclaim: true\n" +
		"  virtiofs_reclaim_threshold_percent: 75\n" +
		"  future_daemon_key: yes\n"
	if err := os.WriteFile(filepath.Join(home, "_config", "settings.yaml"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Daemon.Autostart || cfg.Daemon.Restore != "forget" || !cfg.Daemon.VirtioFSReclaim {
		t.Fatalf("settings not loaded alongside retired keys: %+v", cfg.Daemon)
	}
}

// The reader unmarshals the whole Config, so a refresh that ships a new daemon
// key must not take the rest of settings.yaml with it. Only the daemon block is
// the writer's to rewrite.
func TestSettingsRefreshPreservesUserOwnedFields(t *testing.T) {
	home := t.TempDir()
	seed := NewStore(DefaultConfig(home))
	if err := seed.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	settingsPath := filepath.Join(home, "_config", "settings.yaml")
	authored := "" +
		"# operator notes\n" +
		"default_image: template:custom\n" +
		"runtime_commands:\n" +
		"  start:\n" +
		"    - \"{{binary}} start {{sandbox_name}}\"\n" +
		"future_top_level_key: 7\n" +
		"daemon:\n" +
		"  autostart: false\n" +
		"  restore: forget\n" +
		"  virtiofs_reclaim: true\n" +
		"  virtiofs_reclaim_threshold_percent: 75\n"
	if err := os.WriteFile(settingsPath, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := NewStore(cfg).EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() after edit error = %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	for _, preserved := range []string{"# operator notes", "default_image: template:custom", "runtime_commands:", "{{binary}} start {{sandbox_name}}", "future_top_level_key: 7"} {
		if !strings.Contains(text, preserved) {
			t.Fatalf("settings refresh wiped %q:\n%s", preserved, text)
		}
	}
	for _, daemonSetting := range []string{"autostart: false", "restore: forget", "virtiofs_reclaim: true"} {
		if !strings.Contains(text, daemonSetting) {
			t.Fatalf("settings refresh dropped daemon setting %q:\n%s", daemonSetting, text)
		}
	}
	if strings.Contains(text, "virtiofs_reclaim_threshold_percent") {
		t.Fatalf("settings refresh kept the retired reclaim threshold:\n%s", text)
	}
	if configFileNeedsRefresh(data) {
		t.Fatalf("refreshed settings still report a needed refresh:\n%s", text)
	}

	reloaded, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig() after refresh error = %v", err)
	}
	if reloaded.DefaultImage != "template:custom" || len(reloaded.RuntimeCommands.Start) != 1 {
		t.Fatalf("user settings lost across refresh: %+v", reloaded)
	}
	if reloaded.Daemon.Autostart || reloaded.Daemon.Restore != "forget" || !reloaded.Daemon.VirtioFSReclaim {
		t.Fatalf("daemon settings lost across refresh: %+v", reloaded.Daemon)
	}
}
