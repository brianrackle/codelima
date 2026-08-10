package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Seal the real host home for the whole package's test binary. Runs before any
// test, so a Service built without a stubbed hostAuth seam fails loudly instead
// of quietly reading the developer's ~/.ssh or prompting their Keychain.
func init() {
	hostAuthRealHomeSealed = true
}

// Every credential-shaped string in this file is generated at test time. No
// real key, token, or credential blob is committed here, and the fixtures below
// are deliberately not parseable as any real credential format.
func throwawaySecret(t *testing.T, label string) string {
	t.Helper()
	return "generated-" + label + "-" + newID()
}

// writeHostFile creates a host fixture file, making its parents.
func writeHostFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

// collectorHome builds a temp HOME the collector can be pointed at without
// touching the real one.
func collectorHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", home, err)
	}
	return home
}

func collectBundle(t *testing.T, collector hostAuthCollector) *hostAuthBundle {
	t.Helper()
	bundle, err := collector.collect(context.Background())
	if err != nil {
		t.Fatalf("collect() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := bundle.Close(); closeErr != nil {
			t.Errorf("bundle.Close() error = %v", closeErr)
		}
	})
	return bundle
}

func artifactByName(t *testing.T, bundle *hostAuthBundle, name string) hostAuthArtifact {
	t.Helper()
	for _, artifact := range bundle.Artifacts {
		if artifact.Name == name {
			return artifact
		}
	}
	t.Fatalf("artifact %q not collected, got %v", name, bundle.importedNames())
	return hostAuthArtifact{}
}

func hasArtifact(bundle *hostAuthBundle, name string) bool {
	for _, artifact := range bundle.Artifacts {
		if artifact.Name == name {
			return true
		}
	}
	return false
}

func TestHostAuthCollectorFindsStandardIdentitiesAndAgentCaches(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519"), throwawaySecret(t, "ed25519"))
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519.pub"), "ssh-ed25519 "+throwawaySecret(t, "pub"))
	writeHostFile(t, filepath.Join(home, ".ssh", "id_rsa"), throwawaySecret(t, "rsa"))
	writeHostFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = Test\n")
	writeHostFile(t, filepath.Join(home, ".codex", "auth.json"), `{"token":"`+throwawaySecret(t, "codex")+`"}`)
	writeHostFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"`+throwawaySecret(t, "claude")+`"}`)
	writeHostFile(t, filepath.Join(home, ".claude.json"), `{"machineID":"host","oauthAccount":{"emailAddress":"`+throwawaySecret(t, "email")+`"}}`)

	bundle := collectBundle(t, hostAuthCollector{home: home})

	want := map[string]struct {
		guestPath string
		mode      string
	}{
		"id_ed25519":     {".ssh/id_ed25519", hostAuthPrivateMode},
		"id_ed25519.pub": {".ssh/id_ed25519.pub", hostAuthPublicMode},
		"id_rsa":         {".ssh/id_rsa", hostAuthPrivateMode},
		"gitconfig":      {".gitconfig", hostAuthPublicMode},
		"codex":          {".codex/auth.json", hostAuthPrivateMode},
		"claude":         {".claude/.credentials.json", hostAuthPrivateMode},
		"claude_state":   {".claude.json", hostAuthPrivateMode},
	}
	for name, expected := range want {
		artifact := artifactByName(t, bundle, name)
		if artifact.GuestPath != expected.guestPath || artifact.Mode != expected.mode {
			t.Fatalf("artifact %s = %+v, want guest %q mode %q", name, artifact, expected.guestPath, expected.mode)
		}
	}

	// id_ecdsa was never created, and an absent public half is not an artifact.
	if hasArtifact(bundle, "id_ecdsa") || hasArtifact(bundle, "id_rsa.pub") {
		t.Fatalf("collected artifacts that do not exist on the host: %v", bundle.importedNames())
	}
	if reasons := bundle.skippedReasons(); reasons["ssh_identity"] != "" {
		t.Fatalf("standard identities present but reported skipped: %v", reasons)
	}
}

func TestHostAuthCollectorSkipsEveryMissingArtifactWithoutFailing(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	bundle := collectBundle(t, hostAuthCollector{home: home})

	reasons := bundle.skippedReasons()
	for _, name := range []string{"ssh_identity", "gitconfig", "codex", "claude", "claude_state"} {
		if reasons[name] == "" {
			t.Fatalf("missing artifact %q was not recorded as a skip: %v", name, reasons)
		}
	}

	// A host with no identity contributes no SSH material at all, not even the
	// accept-new fallback: there is no key for it to serve.
	if names := bundle.importedNames(); len(names) != 0 {
		t.Fatalf("empty host imported = %v, want nothing", names)
	}
	if reasons["known_hosts"] != "" {
		t.Fatalf("known_hosts reported without any identity to serve: %v", reasons)
	}
}

func TestHostAuthCollectorPinsPlaintextGitHubKnownHostsLines(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519"), throwawaySecret(t, "ed25519"))
	knownHosts := strings.Join([]string{
		"# a comment",
		"gitlab.com ssh-ed25519 AAAAgitlab",
		"github.com ssh-ed25519 AAAAgithubed",
		"[github.com]:22 ssh-rsa AAAAgithubrsa",
		"github.com,140.82.121.4 ecdsa-sha2-nistp256 AAAAgithubecdsa",
		"|1|aGFzaGVk|c2FsdGVk= ssh-ed25519 AAAAhashed",
		"",
	}, "\n")
	writeHostFile(t, filepath.Join(home, ".ssh", "known_hosts"), knownHosts)

	bundle := collectBundle(t, hostAuthCollector{home: home})

	artifact := artifactByName(t, bundle, "known_hosts")
	if artifact.GuestPath != ".ssh/known_hosts" || artifact.Mode != hostAuthPublicMode {
		t.Fatalf("known_hosts artifact = %+v", artifact)
	}
	data, err := os.ReadFile(artifact.HostPath)
	if err != nil {
		t.Fatalf("ReadFile(staged known_hosts) error = %v", err)
	}
	staged := string(data)
	for _, want := range []string{"AAAAgithubed", "AAAAgithubrsa", "AAAAgithubecdsa"} {
		if !strings.Contains(staged, want) {
			t.Fatalf("staged known_hosts missing %q:\n%s", want, staged)
		}
	}
	for _, unwanted := range []string{"AAAAgitlab", "AAAAhashed", "# a comment"} {
		if strings.Contains(staged, unwanted) {
			t.Fatalf("staged known_hosts leaked %q:\n%s", unwanted, staged)
		}
	}

	// A pinned host key makes the accept-new fallback unnecessary.
	if hasArtifact(bundle, "ssh_config") {
		t.Fatalf("accept-new fallback written despite a pinned github.com key: %v", bundle.importedNames())
	}
}

func TestHostAuthCollectorFallsBackToAcceptNewWhenOnlyHashedEntriesExist(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519"), throwawaySecret(t, "ed25519"))
	writeHostFile(t, filepath.Join(home, ".ssh", "known_hosts"), "|1|aGFzaGVk|c2FsdGVk= ssh-ed25519 AAAAhashed\n")

	bundle := collectBundle(t, hostAuthCollector{home: home})

	if hasArtifact(bundle, "known_hosts") {
		t.Fatalf("hashed-only known_hosts must not be imported: %v", bundle.importedNames())
	}
	if reason := bundle.skippedReasons()["known_hosts"]; !strings.Contains(reason, "plaintext") {
		t.Fatalf("known_hosts skip reason = %q", reason)
	}

	artifact := artifactByName(t, bundle, "ssh_config")
	if artifact.GuestPath != ".ssh/config" || artifact.Mode != hostAuthPrivateMode {
		t.Fatalf("ssh_config artifact = %+v", artifact)
	}
	data, err := os.ReadFile(artifact.HostPath)
	if err != nil {
		t.Fatalf("ReadFile(staged ssh config) error = %v", err)
	}
	if text := string(data); !strings.Contains(text, "Host github.com") || !strings.Contains(text, "StrictHostKeyChecking accept-new") {
		t.Fatalf("accept-new fallback = %q", text)
	}
}

func TestHostAuthCollectorHonorsCodexHomeOverride(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	writeHostFile(t, filepath.Join(home, ".codex", "auth.json"), `{"token":"`+throwawaySecret(t, "default")+`"}`)
	overridden := filepath.Join(codexHome, "auth.json")
	writeHostFile(t, overridden, `{"token":"`+throwawaySecret(t, "override")+`"}`)

	bundle := collectBundle(t, hostAuthCollector{home: home, codexHome: codexHome})

	artifact := artifactByName(t, bundle, "codex")
	if artifact.HostPath != overridden {
		t.Fatalf("codex host path = %q, want the CODEX_HOME override %q", artifact.HostPath, overridden)
	}
	// The guest has no CODEX_HOME override, so the copy always lands at the
	// CLI's default location.
	if artifact.GuestPath != ".codex/auth.json" {
		t.Fatalf("codex guest path = %q", artifact.GuestPath)
	}
}

func TestHostAuthCollectorStagesKeychainCredentialAtRestrictedMode(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	secret := throwawaySecret(t, "keychain")
	writeHostFile(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"accountUuid":"`+throwawaySecret(t, "account")+`"}}`)
	collector := hostAuthCollector{
		home: home,
		keychain: func(context.Context) ([]byte, error) {
			return []byte(secret + "\n"), nil
		},
	}

	bundle := collectBundle(t, collector)

	// Keychain-sourced credentials admit the login-state seed exactly as
	// file-sourced ones do: the seed keys off the imported artifact, not its
	// origin.
	if !hasArtifact(bundle, "claude_state") {
		t.Fatalf("keychain credentials did not admit the state seed: %v", bundle.importedNames())
	}

	artifact := artifactByName(t, bundle, "claude")
	if artifact.GuestPath != ".claude/.credentials.json" || artifact.Mode != hostAuthPrivateMode {
		t.Fatalf("claude artifact = %+v", artifact)
	}
	info, err := os.Stat(artifact.HostPath)
	if err != nil {
		t.Fatalf("Stat(staged claude credential) error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("staged credential mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(artifact.HostPath)
	if err != nil {
		t.Fatalf("ReadFile(staged claude credential) error = %v", err)
	}
	if string(data) != secret {
		t.Fatalf("staged credential was not the keychain value")
	}

	// Close must take the whole staging directory with it.
	stageDir := bundle.stageDir
	if err := bundle.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if exists(stageDir) {
		t.Fatalf("staging directory %q survived Close()", stageDir)
	}
}

func TestHostAuthCollectorTreatsKeychainFailureAsSkip(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	collector := hostAuthCollector{
		home: home,
		keychain: func(context.Context) ([]byte, error) {
			return nil, errors.New("User interaction is not allowed")
		},
	}

	bundle := collectBundle(t, collector)

	if hasArtifact(bundle, "claude") {
		t.Fatalf("a failed keychain lookup produced an artifact: %v", bundle.importedNames())
	}
	if reason := bundle.skippedReasons()["claude"]; !strings.Contains(reason, "keychain lookup failed") {
		t.Fatalf("claude skip reason = %q", reason)
	}
}

// The seeded ~/.claude.json is what keeps the guest CLI from walking its
// first-run login flow: it decides it is signed in from oauthAccount and
// hasCompletedOnboarding, not from the credentials file alone. Exactly those
// two keys may cross — the rest of the host's file is machine-specific state
// that must not follow the user into the guest.
func TestHostAuthCollectorSeedsMinimalClaudeStateBesideCredentials(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	email := throwawaySecret(t, "email")
	writeHostFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"`+throwawaySecret(t, "claude")+`"}`)
	writeHostFile(t, filepath.Join(home, ".claude.json"),
		`{"machineID":"host-machine","numStartups":42,"oauthAccount":{"emailAddress":"`+email+`","organizationUuid":"org"},"projects":{"/host/repo":{}}}`)

	bundle := collectBundle(t, hostAuthCollector{home: home})

	artifact := artifactByName(t, bundle, "claude_state")
	if artifact.GuestPath != ".claude.json" || artifact.Mode != hostAuthPrivateMode {
		t.Fatalf("claude_state artifact = %+v", artifact)
	}
	info, err := os.Stat(artifact.HostPath)
	if err != nil {
		t.Fatalf("Stat(staged claude state) error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("staged state mode = %v, want 0600", info.Mode().Perm())
	}

	data, err := os.ReadFile(artifact.HostPath)
	if err != nil {
		t.Fatalf("ReadFile(staged claude state) error = %v", err)
	}
	var seeded map[string]json.RawMessage
	if err := json.Unmarshal(data, &seeded); err != nil {
		t.Fatalf("staged state is not JSON: %v\n%s", err, data)
	}
	if len(seeded) != 2 {
		t.Fatalf("seeded state carries %d keys, want only hasCompletedOnboarding and oauthAccount:\n%s", len(seeded), data)
	}
	if string(seeded["hasCompletedOnboarding"]) != "true" {
		t.Fatalf("hasCompletedOnboarding = %s, want true", seeded["hasCompletedOnboarding"])
	}
	var account map[string]any
	if err := json.Unmarshal(seeded["oauthAccount"], &account); err != nil {
		t.Fatalf("seeded oauthAccount is not an object: %v", err)
	}
	if account["emailAddress"] != email || account["organizationUuid"] != "org" {
		t.Fatalf("seeded oauthAccount lost the host's fields: %v", account)
	}
}

// oauthAccount without tokens would claim a login the guest cannot back up, so
// the state seed rides only beside imported credentials.
func TestHostAuthCollectorSkipsClaudeStateWhenCredentialsAreNotImported(t *testing.T) {
	t.Parallel()

	home := collectorHome(t)
	writeHostFile(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"emailAddress":"`+throwawaySecret(t, "email")+`"}}`)

	bundle := collectBundle(t, hostAuthCollector{home: home})

	if hasArtifact(bundle, "claude_state") {
		t.Fatalf("state was seeded without credentials beside it: %v", bundle.importedNames())
	}
	if reason := bundle.skippedReasons()["claude_state"]; !strings.Contains(reason, "credentials were not imported") {
		t.Fatalf("claude_state skip reason = %q", reason)
	}
}

// A host ~/.claude.json the seed cannot be built from is a skip that names the
// file and never quotes it, and it must not take the credential import down
// with it.
func TestHostAuthCollectorSkipsUnusableClaudeStateWithoutFailing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		state      string
		missing    bool
		wantReason string
	}{
		{name: "missing file", missing: true, wantReason: "no "},
		{name: "unparseable", state: `{"oauthAccount":`, wantReason: "unparseable"},
		{name: "no oauthAccount key", state: `{"machineID":"host"}`, wantReason: "no oauthAccount"},
		{name: "null oauthAccount", state: `{"oauthAccount":null}`, wantReason: "no oauthAccount"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			home := collectorHome(t)
			writeHostFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"`+throwawaySecret(t, "claude")+`"}`)
			if !testCase.missing {
				writeHostFile(t, filepath.Join(home, ".claude.json"), testCase.state)
			}

			bundle := collectBundle(t, hostAuthCollector{home: home})

			if hasArtifact(bundle, "claude_state") {
				t.Fatalf("unusable state was seeded anyway: %v", bundle.importedNames())
			}
			if reason := bundle.skippedReasons()["claude_state"]; !strings.Contains(reason, testCase.wantReason) {
				t.Fatalf("claude_state skip reason = %q, want it to contain %q", reason, testCase.wantReason)
			}
			if !hasArtifact(bundle, "claude") {
				t.Fatalf("an unusable state file took the credential import with it: %v", bundle.skippedReasons())
			}
		})
	}
}

// The platform seam is what stands between a Linux host and a keychain that
// does not exist there. Its failure must read as an ordinary skip.
func TestHostAuthCollectorPlatformKeychainSeamSkipsOffDarwin(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "darwin" {
		t.Skip("the darwin seam talks to a real login keychain")
	}

	// The real platform seam, deliberately — a stub would prove nothing here.
	// Only the home is faked, and newHostAuthCollector is sealed in tests.
	bundle := collectBundle(t, hostAuthCollector{
		home:     collectorHome(t),
		keychain: platformClaudeKeychainCredentials,
	})

	if hasArtifact(bundle, "claude") {
		t.Fatalf("non-darwin keychain seam produced an artifact: %v", bundle.importedNames())
	}
	if reason := bundle.skippedReasons()["claude"]; !strings.Contains(reason, "darwin") {
		t.Fatalf("claude skip reason = %q, want the platform explanation", reason)
	}
}

func TestKnownHostsLinesForHostMatchesOnlyUnhashedHostPatterns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
		want bool
	}{
		{name: "plain", line: "github.com ssh-ed25519 AAAA", want: true},
		{name: "bracketed port", line: "[github.com]:22 ssh-ed25519 AAAA", want: true},
		{name: "comma list", line: "example.com,github.com ssh-ed25519 AAAA", want: true},
		{name: "uppercase", line: "GitHub.COM ssh-ed25519 AAAA", want: true},
		{name: "cert authority marker", line: "@cert-authority github.com ssh-ed25519 AAAA", want: true},
		{name: "hashed", line: "|1|c2FsdA==|aGFzaA== ssh-ed25519 AAAA", want: false},
		{name: "other host", line: "gist.github.com ssh-ed25519 AAAA", want: false},
		{name: "comment", line: "# github.com ssh-ed25519 AAAA", want: false},
		{name: "blank", line: "   ", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := knownHostsLinesForHost([]byte(testCase.line+"\n"), hostAuthGitHubHost)
			if (len(got) == 1) != testCase.want {
				t.Fatalf("knownHostsLinesForHost(%q) = %v, want match=%v", testCase.line, got, testCase.want)
			}
		})
	}
}

func TestGuestAuthPlacementScriptAppliesStrictOwnershipAndModes(t *testing.T) {
	t.Parallel()

	artifacts := []hostAuthArtifact{
		{Name: "id_ed25519", GuestPath: ".ssh/id_ed25519", Mode: hostAuthPrivateMode},
		{Name: "id_ed25519.pub", GuestPath: ".ssh/id_ed25519.pub", Mode: hostAuthPublicMode},
		{Name: "gitconfig", GuestPath: ".gitconfig", Mode: hostAuthPublicMode},
		{Name: "codex", GuestPath: ".codex/auth.json", Mode: hostAuthPrivateMode},
		{Name: "claude", GuestPath: ".claude/.credentials.json", Mode: hostAuthPrivateMode},
	}
	script := guestAuthPlacementScript("/tmp/codelima-auth-node", artifacts)

	if !strings.Contains(script, `guest_user="${SUDO_USER:-$(id -un)}"`) {
		t.Fatalf("placement script does not resolve the login user:\n%s", script)
	}
	// Root's home comes from passwd, which is what `sudo -H` reads to set HOME
	// for a managed terminal, with /root as the fallback.
	for _, want := range []string{
		`root_home="$(getent passwd root | cut -d: -f6)"`,
		`test -n "$root_home" || root_home=/root`,
		`mkdir -p "$root_home"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("placement script missing %q:\n%s", want, script)
		}
	}

	for _, dir := range []string{".ssh", ".codex", ".claude"} {
		for _, want := range []string{
			`install -d -m 0700 -o "$guest_user" -g "$guest_group" "$guest_home"/'` + dir + `'`,
			`install -d -m 0700 -o root -g root "$root_home"/'` + dir + `'`,
		} {
			if !strings.Contains(script, want) {
				t.Fatalf("placement script missing %q:\n%s", want, script)
			}
		}
	}

	// Every artifact lands in BOTH homes at the same mode: the login user's,
	// which bootstrap and agent validation use, and root's, which is what a
	// managed terminal actually runs as.
	for _, want := range []string{
		`install -m 0600 -o "$guest_user" -g "$guest_group" "$staging"/'id_ed25519' "$guest_home"/'.ssh/id_ed25519'`,
		`install -m 0644 -o "$guest_user" -g "$guest_group" "$staging"/'id_ed25519.pub' "$guest_home"/'.ssh/id_ed25519.pub'`,
		`install -m 0644 -o "$guest_user" -g "$guest_group" "$staging"/'gitconfig' "$guest_home"/'.gitconfig'`,
		`install -m 0600 -o "$guest_user" -g "$guest_group" "$staging"/'codex' "$guest_home"/'.codex/auth.json'`,
		`install -m 0600 -o "$guest_user" -g "$guest_group" "$staging"/'claude' "$guest_home"/'.claude/.credentials.json'`,

		`install -m 0600 -o root -g root "$staging"/'id_ed25519' "$root_home"/'.ssh/id_ed25519'`,
		`install -m 0644 -o root -g root "$staging"/'id_ed25519.pub' "$root_home"/'.ssh/id_ed25519.pub'`,
		`install -m 0644 -o root -g root "$staging"/'gitconfig' "$root_home"/'.gitconfig'`,
		`install -m 0600 -o root -g root "$staging"/'codex' "$root_home"/'.codex/auth.json'`,
		`install -m 0600 -o root -g root "$staging"/'claude' "$root_home"/'.claude/.credentials.json'`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("placement script missing %q:\n%s", want, script)
		}
	}

	// Both homes are filled from the same staged copy — the host must not be
	// read or copied twice.
	if got := strings.Count(script, `"$staging"/'id_ed25519' `); got != 2 {
		t.Fatalf("id_ed25519 installed from staging %d times, want exactly 2 (one per home):\n%s", got, script)
	}

	// install -d would chmod an existing /root; only mkdir -p may create it.
	if strings.Contains(script, `install -d -m 0700 -o root -g root "$root_home"`+"\n") {
		t.Fatalf("placement script rewrites the mode of root's home directory:\n%s", script)
	}

	// The staging copies must not outlive the command, successful or not.
	if !strings.Contains(script, `cleanup() { rm -rf "$staging"; }`) || !strings.Contains(script, "trap cleanup EXIT") {
		t.Fatalf("placement script does not remove the staging directory:\n%s", script)
	}
}

func TestGuestAuthStagePrepareScriptHandsStagingToTheLoginUser(t *testing.T) {
	t.Parallel()

	script := guestAuthStagePrepareScript("/tmp/codelima-auth-node")
	for _, want := range []string{
		`guest_user="${SUDO_USER:-$(id -un)}"`,
		`chown "$guest_user:$guest_group" "$staging"`,
		`chmod 0700 "$staging"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("prepare script missing %q:\n%s", want, script)
		}
	}
}

func TestGuestAuthDirectoriesAreDeduplicatedAndExcludeHome(t *testing.T) {
	t.Parallel()

	dirs := guestAuthDirectories([]hostAuthArtifact{
		{GuestPath: ".ssh/id_ed25519"},
		{GuestPath: ".ssh/known_hosts"},
		{GuestPath: ".gitconfig"},
		{GuestPath: ".codex/auth.json"},
	})
	sort.Strings(dirs)
	if strings.Join(dirs, ",") != ".codex,.ssh" {
		t.Fatalf("guestAuthDirectories() = %v", dirs)
	}
}
