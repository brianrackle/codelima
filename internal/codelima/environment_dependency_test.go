package codelima

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestEnvironmentDependencyFindsExecutableCurlAndEarlierInstalls(t *testing.T) {
	tests := []struct {
		name       string
		commands   []string
		warnings   []int
		installed  bool
		fixCommand int
	}{
		{"reference", []string{"apt-get update && apt-get install -y build-essential pkg-config libssl-dev", "curl -fsSL https://example.invalid/rust | sh", "rustup component add clippy"}, []int{1}, false, 0},
		{"installed earlier", []string{"apt-get update && apt-get install -y curl", "curl --version"}, nil, true, -1},
		{"installed in same block", []string{"apt-get install -y curl && curl --version"}, nil, true, -1},
		{"later install", []string{"curl --version", "apt-get install -y curl"}, []int{0}, true, -1},
		{"quoted data and comments", []string{"printf '%s' 'curl --version'\n# curl --version\ncat <<'EOF'\ncurl --version\napt-get install -y curl\nEOF"}, nil, false, -1},
		{"uncalled function", []string{"setup() { apt-get install -y curl; curl --version; }\ncurl --version"}, []int{0}, false, -1},
		{"quoted executable", []string{"\"curl\" --version"}, []int{0}, false, -1},
		{"substitution executes", []string{"printf '%s' \"$(curl --version)\""}, []int{0}, false, -1},
		{"heredoc substitution executes", []string{"cat <<EOF\n$(curl --version)\nEOF"}, []int{0}, false, -1},
		{"command lookup", []string{"command -v curl\nwhich curl\ntype curl"}, nil, false, -1},
		{"command wrapper", []string{"command -- curl --version"}, []int{0}, false, -1},
		{"sudo wrapper", []string{"sudo -H -- apt-get install -y curl", "sudo -H curl --version"}, nil, true, -1},
		{"conditional install", []string{"if test -f /optional; then apt-get install -y curl; fi", "curl --version"}, []int{1}, false, -1},
		{"both branches install", []string{"if test -f /optional; then apt-get install -y curl; else apt install -y curl; fi", "curl --version"}, nil, true, -1},
		{"pipeline does not order install", []string{"apt-get install -y curl | curl --version"}, []int{0}, false, -1},
		{"background does not order install", []string{"apt-get install -y curl &\ncurl --version"}, []int{0}, false, -1},
		{"short circuit skips install", []string{"false && apt-get install -y curl; curl --version"}, []int{0}, false, -1},
		{"or can skip install", []string{"test -f /optional || apt-get install -y curl", "curl --version"}, []int{1}, false, -1},
		{"loop may not execute", []string{"for x in $items; do apt-get install -y curl; done", "curl --version"}, []int{1}, false, -1},
		{"download only", []string{"apt-get install --download-only curl", "curl --version"}, []int{1}, false, -1},
		{"simulated install", []string{"apt-get -s install curl", "curl --version"}, []int{1}, false, -1},
		{"dynamic options", []string{"apt-get $options install curl", "curl --version"}, []int{1}, false, -1},
		{"removed later", []string{"apt-get install -y curl", "apt-get remove -y curl", "curl --version"}, []int{2}, false, -1},
		{"not libcurl", []string{"apt-get install -y libcurl4", "curl --version"}, []int{1}, false, 0},
		{"repeated use", []string{"curl --version; curl --help"}, []int{0}, false, -1},
		{"different branches cannot share a fix", []string{"if test -f /optional; then apt-get install -y make && curl --version; else curl --version; fi"}, []int{0}, false, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := analyzeEnvironmentDependencies(tt.commands)
			var steps []int
			for _, w := range r.Warnings {
				steps = append(steps, w.Command)
			}
			if !slices.Equal(steps, tt.warnings) || r.CurlInstalled != tt.installed || len(r.Unavailable) != 0 {
				t.Fatalf("wrong dependency evidence: %+v", r)
			}
			if len(r.Warnings) > 0 {
				fix := -1
				if r.Warnings[0].Fix != nil {
					fix = r.Warnings[0].Fix.Command
				}
				if fix != tt.fixCommand {
					t.Fatalf("fix points to command %d, want %d", fix, tt.fixCommand)
				}
			}
		})
	}
}

func TestEnvironmentDependencyFixPreservesSourceAndRejectsStaleOffsets(t *testing.T) {
	original := "# 保持\napt-get update && apt-get install -y \\\n  build-essential 'pkg-config' # retained comment\n"
	commands := []string{original, "curl --version"}
	report := analyzeEnvironmentDependencies(commands)
	if len(report.Warnings) != 1 || report.Warnings[0].Fix == nil {
		t.Fatal("missing actionable warning", report)
	}
	draft, ok := applyEnvironmentDependencyFix(commands, report.Warnings[0])
	want := strings.Replace(original, "'pkg-config' #", "'pkg-config' curl #", 1)
	if !ok || draft[0] != want || commands[0] != original || draft[1] != commands[1] {
		t.Fatal("fix changed source trivia or saved commands", draft)
	}
	if got := analyzeEnvironmentDependencies(draft); len(got.Warnings) != 0 || !got.CurlInstalled {
		t.Fatal("reviewable fix did not resolve the warning", got)
	}
	commands[0] = "printf changed"
	if _, ok := applyEnvironmentDependencyFix(commands, report.Warnings[0]); ok {
		t.Fatal("applied stale parser offsets to a changed command")
	}
}

func TestEnvironmentDependencyChecksAreBoundedAndDoNotExecuteCommands(t *testing.T) {
	for _, script := range []string{"if", strings.Repeat("#", 64<<10+1)} {
		r := analyzeEnvironmentDependencies([]string{script})
		if len(r.Unavailable) != 1 || len(r.Warnings) != 0 || r.CurlInstalled {
			t.Fatal("unavailable analysis invented evidence", r)
		}
	}
	commands := make([]string, 1024)
	for i := range commands {
		commands[i] = "true"
	}
	if r := analyzeEnvironmentDependencies(commands); len(r.Unavailable) != 1 || r.Unavailable[0] != 256 || r.CurlInstalled {
		t.Fatal("command-count limit did not bound the report", r)
	}
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	analyzeEnvironmentDependencies([]string{"touch " + shellQuote(marker) + "; curl --version"})
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("dependency analysis executed shell code", err)
	}
	for _, env := range builtInEnvironmentConfigs() {
		r := analyzeEnvironmentDependencies(env.BootstrapCommands)
		if !r.CurlInstalled || len(r.Warnings) != 0 {
			t.Fatal("current built-in install evidence was not recognized", env.Slug, r)
		}
	}
}
