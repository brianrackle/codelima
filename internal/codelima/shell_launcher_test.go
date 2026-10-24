package codelima

import (
	"reflect"
	"testing"
)

func TestShellWindowExpandsCommandAsLiteralArguments(t *testing.T) {
	command := []string{"/a b/codelima", "terminal", "attach", "it's $(unsafe)"}
	got, err := shellWindowArgs(`'/Applications/Ghostty App/ghostty' -e {cmd}`, command)
	want := append([]string{"/Applications/Ghostty App/ghostty", "-e"}, command...)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("argv=%q err=%v", got, err)
	}
	for _, template := range []string{"ghostty", "ghostty {cmd} {cmd}", "ghostty prefix{cmd}", "{cmd}", "ghostty {cmd}; touch bad", "ghostty $(bad) {cmd}"} {
		if _, err := shellWindowArgs(template, command); err == nil {
			t.Errorf("accepted ambiguous template %q", template)
		}
	}
}

func TestShellLaunchKeepsHomeAndSessionInBothModes(t *testing.T) {
	cfg := DefaultConfig("/a b/home")
	for _, mode := range []string{"window", "tmux"} {
		args, err := shellLaunchArgs(cfg, mode, "/my bin/codelima", "session-id", "/work dir", "node name")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"env", "CODELIMA_HOME=/a b/home", "/my bin/codelima", "terminal", "attach", "session-id"}
		if len(args) < len(want) || !reflect.DeepEqual(args[len(args)-len(want):], want) {
			t.Fatalf("%s: %q", mode, args)
		}
	}
	t.Setenv("TMUX", "")
	if err := checkShellLaunch(cfg, "tmux"); err == nil {
		t.Fatal("tmux offered outside a session")
	}
}
