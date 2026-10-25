//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima"
)

func TestKeyboardSettingsCLI(t *testing.T) {
	h := newHarness(t)
	h.run(true, "settings", "set", "keys.node.delete", "[alt+d]")
	var info codelima.KeyBindingInspection
	if err := json.Unmarshal(h.json("settings", "keys"), &info); err != nil {
		t.Fatal(err)
	}
	if info.ValidationError != "" {
		t.Fatal(info.ValidationError)
	}
	found := false
	for _, binding := range info.Bindings {
		if binding.Action == "node.delete" {
			found = len(binding.Effective) == 1 && binding.Effective[0] == "alt+d"
		}
	}
	if !found {
		t.Fatal("CLI inspection did not return effective mapping")
	}
	path := filepath.Join(h.home, "_config", "settings.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if output, code := h.run(false, "settings", "set", "keys.node.clone", "[alt+d]"); code == 0 {
		t.Fatalf("conflicting update succeeded: %s", output)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("conflicting CLI update changed settings")
	}
	h.run(true, "settings", "reset", "keys", "node.delete")
	if err := os.WriteFile(path, []byte("keys: invalid\nfuture: retained\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(h.json("settings", "keys"), &info); err != nil || info.ValidationError == "" {
		t.Fatal("CLI did not report malformed keyboard preferences", err)
	}
	h.run(true, "settings", "reset", "keys")
	after, _ = os.ReadFile(path)
	if strings.Contains(string(after), "keys:") || !strings.Contains(string(after), "future: retained") {
		t.Fatal("CLI recovery damaged settings", string(after))
	}
}
