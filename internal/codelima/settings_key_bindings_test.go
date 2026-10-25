package codelima

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestSettingsKeyBindingsPreserveAuthoredOverridesAndValidateProspectiveMap(t *testing.T) {
	s, _ := newTestService(t)
	if err := s.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	path := s.store.configPath()
	initial := "# preferences\nkeys:\n  node.delete: [ctrl+d] # review\nfuture: keep # unknown\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.SettingsUpdate(context.Background(), "keys.node.clone", "[alt+c]")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Keys) != 2 || cfg.Keys["node.clone"][0] != "alt+c" {
		t.Fatalf("dotted action was not one map key: %#v", cfg.Keys)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"# preferences", "# review", "future: keep # unknown", "node.clone:"} {
		if !strings.Contains(string(data), want) {
			t.Fatal("lost authored data", want, string(data))
		}
	}
	for _, value := range []string{"[ctrl+d]", "[delete-everything]", "[n, n]", "false", "null", "[42]"} {
		if _, err := s.SettingsUpdate(context.Background(), "keys.node.clone", value); err == nil {
			t.Fatal("accepted invalid prospective map", value)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(data) {
			t.Fatal("failed update changed settings")
		}
	}
	inspection, err := s.SettingsKeys()
	if err != nil || inspection.ValidationError != "" {
		t.Fatal(inspection, err)
	}
	found := false
	for _, entry := range inspection.Bindings {
		if entry.Action == "node.clone" {
			found = len(entry.Effective) == 1 && entry.Effective[0] == "alt+c"
		}
	}
	if !found {
		t.Fatal("inspection omitted effective override")
	}
}

func TestSettingsKeyBindingsRecovery(t *testing.T) {
	for _, contents := range []string{
		"keys:\n  node.delete: [c]\n", "keys:\n  unknown.action: [q]\n", "keys: invalid\n", "keys:\n  node.delete: [42]\n",
	} {
		t.Run(contents, func(t *testing.T) {
			s, _ := newTestService(t)
			if err := s.ensureDirectories(); err != nil {
				t.Fatal(err)
			}
			path := s.store.configPath()
			if err := os.WriteFile(path, []byte(contents+"future: keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfigAtHome(s.cfg.MetadataRoot)
			if err != nil {
				t.Fatal("frontend preference broke general config load", err)
			}
			if _, err := compileConfiguredKeyBindings(cfg); err == nil {
				t.Fatal("invalid binding configuration was lost")
			}
			inspection, err := s.SettingsKeys()
			if err != nil || inspection.ValidationError == "" || len(inspection.Bindings) == 0 {
				t.Fatal("inspection did not expose safe defaults plus error", inspection, err)
			}
			if _, err := s.SettingsResetKeys(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), "keys:") || !strings.Contains(string(data), "future: keep") {
				t.Fatal("reset failed", string(data))
			}
		})
	}
}

func TestSettingsKeyBindingsResetOneAndRejectMalformedYAML(t *testing.T) {
	s, _ := newTestService(t)
	if err := s.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	path := s.store.configPath()
	contents := "keys:\n  unknown.action: [q]\n  node.delete: [ctrl+d] # retained\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cfg.Keys = KeyBindingConfig{"node.clone": {"alt+c"}}
	if _, err := s.SettingsResetKeys(context.Background(), "unknown.action"); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.SettingsRead()
	if err != nil || len(cfg.Keys) != 1 || cfg.Keys["node.delete"][0] != "ctrl+d" {
		t.Fatal("reset merged stale in-memory overrides", cfg.Keys, err)
	}
	cfg.Keys["node.delete"][0] = "q"
	if s.cfg.Keys["node.clone"][0] != "alt+c" {
		t.Fatal("read mutated service defaults")
	}
	malformed := "keys: [unterminated\n"
	if err := os.WriteFile(path, []byte(malformed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettingsResetKeys(context.Background(), ""); err == nil {
		t.Fatal("reset overwrote malformed YAML")
	}
	data, _ := os.ReadFile(path)
	if string(data) != malformed {
		t.Fatal("malformed YAML was changed")
	}
}

func TestSettingsKeyBindingsConcurrentCollision(t *testing.T) {
	s, _ := newTestService(t)
	if err := s.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for _, action := range []string{"node.clone", "node.delete"} {
		go func() {
			<-start
			_, err := s.SettingsUpdate(context.Background(), "keys."+action, "[alt+d]")
			results <- err
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent conflicting updates: %d succeeded", successes)
	}
	cfg, err := s.SettingsRead()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Keys) != 1 {
		t.Fatalf("wrong persisted map: %v", cfg.Keys)
	}
	if _, err := compileConfiguredKeyBindings(cfg); err != nil {
		t.Fatal("persisted invalid map", err)
	}
}
