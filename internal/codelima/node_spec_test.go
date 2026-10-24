package codelima

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestNodeSpecComparesFrozenRecipeWithoutRuntimeOrMutation(t *testing.T) {
	s, dir := newTestService(t)
	ctx := context.Background()
	c, err := s.ConfigurationCreate(ctx, ConfigurationCreateInput{Slug: "spec-template", BootstrapCommands: []string{"printf frozen"}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.NodeCreate(ctx, NodeCreateInput{Configuration: c.ID, Directory: dir, Slug: "spec-node"})
	if err != nil {
		t.Fatal(err)
	}
	before := readTestFile(t, s.store.nodePath(n.ID))
	initial, err := s.NodeSpec(ctx, n.ID)
	if err != nil || len(initial.Differences) != 0 || initial.TemplateError != nil {
		t.Fatalf("initial spec: %+v, %v", initial, err)
	}
	memory := c.MemoryMiB + 1024
	if _, err = s.ConfigurationUpdate(ctx, c.ID, ConfigurationUpdateInput{MemoryMiB: &memory, Environments: []string{"claude-code", "codex"}, BootstrapCommands: []string{"printf changed"}}); err != nil {
		t.Fatal(err)
	}
	s.sandbox.(*fakeSandbox).listErr = os.ErrPermission
	got, err := s.NodeSpec(ctx, n.Slug)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range got.Differences {
		names = append(names, d.Name)
	}
	for _, want := range []string{"memory", "environments", "bootstrap"} {
		if !slices.Contains(names, want) {
			t.Fatalf("missing drift %q: %+v", want, got.Differences)
		}
	}
	if got.Node.MemoryMiB != c.MemoryMiB || before != readTestFile(t, s.store.nodePath(n.ID)) {
		t.Fatal("inspection mutated frozen node")
	}
	if got.LimaPath != filepath.Join(s.store.nodeDir(n.ID), "instance.lima.yaml") {
		t.Fatal(got.LimaPath)
	}
}

func TestNodeSpecTracksEnvironmentAndProfileChangesAndDeletedTemplate(t *testing.T) {
	s, dir := newTestService(t)
	ctx := context.Background()
	c, err := s.ConfigurationCreate(ctx, ConfigurationCreateInput{Slug: "spec-recipe"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.NodeCreate(ctx, NodeCreateInput{Configuration: c.ID, Directory: dir, Slug: "recipe-node"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.store.EnvironmentConfigByIDOrSlug(c.Environments[0])
	if err != nil {
		t.Fatal(err)
	}
	env.BootstrapCommands = append(env.BootstrapCommands, "printf environment-edited")
	if err := s.store.SaveEnvironmentConfig(env); err != nil {
		t.Fatal(err)
	}
	got, err := s.NodeSpec(ctx, n.ID)
	if err != nil || len(got.Differences) != 1 || got.Differences[0].Name != "bootstrap" {
		t.Fatalf("environment drift: %+v, %v", got, err)
	}
	profile, err := s.store.LoadAgentProfile(c.AgentProfileName)
	if err != nil {
		t.Fatal(err)
	}
	profile.ValidationCommand = "printf new-validation"
	profile.Environment = map[string]string{"TOKEN": "secret-value"}
	if err := writeYAMLFile(s.store.agentProfilePath(profile.Name), profile); err != nil {
		t.Fatal(err)
	}
	got, err = s.NodeSpec(ctx, n.ID)
	if err != nil || len(got.Differences) != 3 {
		t.Fatalf("profile drift: %+v, %v", got, err)
	}
	for _, diff := range got.Differences {
		if strings.Contains(diff.Current, "secret-value") {
			t.Fatal("environment secret exposed in drift summary")
		}
	}
	deleted := s.now()
	c.DeletedAt = &deleted
	if err := s.store.SaveConfiguration(c); err != nil {
		t.Fatal(err)
	}
	got, err = s.NodeSpec(ctx, n.ID)
	if err != nil || got.Template != nil || got.TemplateError == nil || got.Node.ID != n.ID {
		t.Fatalf("deleted template lost frozen spec: %+v, %v", got, err)
	}
}

func TestNodeSpecReadsActualLimaYAMLAndRejectsUnboundedOrSpecialFiles(t *testing.T) {
	s, _ := newTestService(t)
	n := saveForwardingTestNode(t, s, "yaml-node")
	path := filepath.Join(s.store.nodeDir(n.ID), "instance.lima.yaml")
	data := "vmType: qemu\nmountType: 9p\nnestedVirtualization: false\ncontainerd: {system: false, user: false}\nmounts:\n  - {location: /work, mountPoint: /guest, writable: true}\nportForwards:\n  - {guestPortRange: [1, 65535], guestIP: 0.0.0.0, guestIPMustBeZero: false, proto: any, ignore: true}\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.NodeSpec(context.Background(), n.ID)
	if err != nil || got.LimaError != nil {
		t.Fatalf("read YAML: %+v, %v", got, err)
	}
	var values []string
	for _, field := range got.LimaFields {
		values = append(values, field.Value)
	}
	for _, want := range []string{"qemu", "9p", "false", "off", "/work → /guest · writable", "ignore all"} {
		if !slices.Contains(values, want) {
			t.Fatalf("missing actual YAML value %q: %+v", want, got.LimaFields)
		}
	}
	for _, data := range []string{"vmType: [bad", "null", "# empty", strings.Repeat("x", nodeSpecYAMLLimit+1)} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err = s.NodeSpec(context.Background(), n.ID)
		if err != nil || got.LimaError == nil || got.Node.ID != n.ID {
			t.Fatalf("bad YAML hid frozen node: %+v, %v", got, err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = s.NodeSpec(context.Background(), n.ID)
	if err != nil || got.LimaError == nil || !strings.Contains(got.LimaError.Error(), "regular file") {
		t.Fatalf("FIFO read: %+v, %v", got, err)
	}
}
