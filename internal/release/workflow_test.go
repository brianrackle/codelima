package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
	"gopkg.in/yaml.v3"
)

type releaseWorkflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Name string
			Run  string
		}
	}
}

func loadReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(installerScript(t, "package_release.sh")), "../.github/workflows/release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

// Run the actual publication shell with a recording GitHub CLI so a beta
// cannot regress to a stable/latest release or lose its packaged assets.
func TestReleaseWorkflowPublishesChannelAndAssets(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	var script string
	for _, step := range workflow.Jobs["publish-release"].Steps {
		if step.Name == "Publish GitHub release" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("missing publication step")
	}
	for _, tag := range []string{"v0.2.3", "v0.3.0-beta.1"} {
		t.Run(tag, func(t *testing.T) {
			root := testutil.TempDir(t, "release-workflow-")
			meta, err := ParseTag(tag)
			if err != nil {
				t.Fatal(err)
			}
			installerWrite(t, filepath.Join(root, "bin/gh"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GH_RECORD\"\n", 0o755)
			if meta.Prerelease {
				installerWrite(t, filepath.Join(root, ".github/release-notes", tag+".md"), "Native QA remains unverified.\n", 0o644)
			}
			for _, target := range []string{"darwin_arm64", "linux_amd64", "linux_arm64"} {
				asset := "codelima_" + meta.Version + "_" + target + ".tar.gz"
				installerWrite(t, filepath.Join(root, "tmp/release/dist", asset), "archive", 0o644)
				installerWrite(t, filepath.Join(root, "tmp/release/dist", asset+".json"), "manifest", 0o644)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-euo", "pipefail", "-c", script)
			cmd.Dir = root
			prerelease := "false"
			if meta.Prerelease {
				prerelease = "true"
			}
			record := filepath.Join(root, "gh-args")
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "GH_RECORD="+record, "GH_REPO=brianrackle/codelima", "TAG="+tag, "PRERELEASE="+prerelease)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("publish: %v\n%s", err, output)
			}
			args, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"release\ncreate\n" + tag + "\n", "--verify-tag\n", "--prerelease=" + prerelease + "\n"} {
				if !strings.Contains(string(args), want) {
					t.Errorf("missing %q in %s", want, args)
				}
			}
			if strings.Contains(string(args), "--latest=false\n") != meta.Prerelease {
				t.Errorf("wrong latest policy: %s", args)
			}
			if strings.Contains(string(args), "--notes-file\n.github/release-notes/"+tag+".md\n") != meta.Prerelease {
				t.Errorf("wrong beta qualification notes: %s", args)
			}
			if strings.Count(string(args), ".tar.gz\n") != 3 || strings.Count(string(args), ".tar.gz.json\n") != 3 {
				t.Errorf("missing release assets: %s", args)
			}
			if meta.Prerelease {
				if err := os.Remove(filepath.Join(root, ".github/release-notes", tag+".md")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(record); err != nil {
					t.Fatal(err)
				}
				missingNotes := exec.CommandContext(ctx, "bash", "-euo", "pipefail", "-c", script)
				missingNotes.Dir, missingNotes.Env = cmd.Dir, cmd.Env
				if output, err := missingNotes.CombinedOutput(); err == nil {
					t.Fatalf("published beta without qualification notes: %s", output)
				}
				if _, err := os.Stat(record); !os.IsNotExist(err) {
					t.Fatalf("GitHub CLI was called without beta notes: %v", err)
				}
			}
		})
	}
}

func TestReleaseWorkflowAddsBetaWithoutChangingStable(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	var script string
	for _, step := range workflow.Jobs["update-homebrew-tap"].Steps {
		if step.Name == "Update tap repository" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("missing tap update step")
	}
	root := testutil.TempDir(t, "beta-tap-")
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	git := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	git("init", "--bare", "--initial-branch=main", origin)
	git("init", "--initial-branch=main", seed)
	installerWrite(t, filepath.Join(seed, "Formula/codelima.rb"), "stable formula\n", 0o644)
	git("-C", seed, "add", "Formula/codelima.rb")
	git("-C", seed, "-c", "user.name=Release Test", "-c", "user.email=release@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "Stable release")
	git("-C", seed, "push", origin, "main")
	installerWrite(t, filepath.Join(root, "tmp/release/generated/Formula/codelima-beta.rb"), "beta formula\n", 0o644)
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, "bash", "-euo", "pipefail", "-c", script)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GITHUB_WORKSPACE="+root, "HOMEBREW_TAP_REPO=example/tap", "HOMEBREW_TAP_BRANCH=main",
			"HOMEBREW_TAP_TOKEN=test-token", "FORMULA_NAME=codelima-beta", "TAG=v0.3.0-beta.1",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=url.file://"+origin+".insteadOf",
			"GIT_CONFIG_VALUE_0=https://x-access-token:test-token@github.com/example/tap.git")
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("tap update: %v\n%s", err, output)
		}
		if got := git("--git-dir", origin, "show", "main:Formula/codelima.rb"); got != "stable formula\n" {
			t.Fatalf("stable formula changed: %s", got)
		}
		if got := git("--git-dir", origin, "show", "main:Formula/codelima-beta.rb"); got != "beta formula\n" {
			t.Fatalf("beta formula not published: %s", got)
		}
		if got := strings.TrimSpace(git("--git-dir", origin, "rev-list", "--count", "main")); got != "2" {
			t.Fatalf("unexpected tap commit count: %s", got)
		}
		if err := os.RemoveAll(filepath.Join(root, "tmp/release/tap")); err != nil {
			t.Fatal(err)
		}
	}
}
