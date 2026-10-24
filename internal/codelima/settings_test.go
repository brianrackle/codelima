package codelima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestManagerSettingsPreserveCommentsUnknownKeysAndConcurrentEdits(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.ensureReadyForWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := service.store.configPath()
	contents := "# preferences\nshell:\n  mode: attach # preferred mode\n  future: keep # future comment\ncolor: auto\nunknown: [a, b]\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for key, value := range map[string]string{"shell.mode": "embedded", "color": "ansi", "import_host_auth": "false"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.SettingsUpdate(context.Background(), key, value); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# preferences", "mode: embedded # preferred mode", "future: keep # future comment", "unknown: [a, b]", "color: ansi", "import_host_auth: false"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lost %q:\n%s", want, data)
		}
	}
	cfg, err := service.SettingsRead()
	if err != nil || cfg.Shell.Mode != "embedded" || cfg.Color != "ansi" || cfg.ImportHostAuth {
		t.Fatalf("preferences not applied: %+v %v", cfg, err)
	}
}

func TestManagerSettingsRejectInvalidInputWithoutRewriting(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.ensureReadyForWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := service.store.configPath()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"shell.mode": "magic", "shell.window": "ghostty -e", "color": "purple", "import_host_auth": "maybe", "unknown": "bad", "shell.detach_key": "ctrl+c"} {
		if _, err := service.SettingsUpdate(context.Background(), key, value); err == nil {
			t.Errorf("accepted %s=%s", key, value)
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("invalid settings rewrote the file")
	}
	malformed := "shell: [broken"
	if err := os.Remove(filepath.Join(service.cfg.MetadataRoot, "_config", "seed.version")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(malformed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SettingsUpdate(context.Background(), "color", "ansi"); err == nil {
		t.Fatal("overwrote malformed settings")
	}
	after, _ = os.ReadFile(path)
	if string(after) != malformed {
		t.Fatal("malformed settings were destroyed")
	}
}

func TestManagerSettingsAffectOnlyFutureNodeCredentials(t *testing.T) {
	service, workspace := newTestService(t)
	ctx := context.Background()
	before, err := service.NodeCreate(ctx, NodeCreateInput{Slug: "before-setting", Directory: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SettingsUpdate(ctx, "import_host_auth", "false"); err != nil {
		t.Fatal(err)
	}
	after, err := service.NodeCreate(ctx, NodeCreateInput{Slug: "after-setting", Directory: workspace})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := service.store.NodeByIDOrSlug(before.ID)
	if err != nil || !retained.ImportHostAuth || after.ImportHostAuth {
		t.Fatalf("frozen/new defaults: before=%v after=%v err=%v", retained.ImportHostAuth, after.ImportHostAuth, err)
	}
}
