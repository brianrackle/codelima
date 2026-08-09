package codelima

// Creation-time host credential import.
//
// A fresh node is useless to an agent that has to re-authenticate before it can
// do anything, so the first bring-up copies the host user's git identity and
// agent credential caches into the guest. The import happens exactly once, on
// the first start of a node whose frozen `import_host_auth` choice is on, and
// never again: the copied Codex/Claude caches carry refresh tokens, and each
// guest rotates its own copy independently. Re-importing on a later start would
// overwrite a chain the guest had already rotated with one the host has since
// invalidated, which logs the guest out. See ADR 128.
//
// Nothing here ever moves secret CONTENT through argv, a log record, or an
// event payload. Files reach the guest as file copies; a secret that has no
// host file of its own (macOS Keychain extraction) is staged into a 0600 host
// temp file first and that path — never the value — is what a command sees.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// hostAuthKeychainTimeout bounds the macOS Keychain read. `security` can
	// raise a modal authorization prompt that nobody is watching (a node created
	// from a daemon or a detached TUI), and an unbounded wait there would hang
	// node creation forever. A denied or unanswered prompt is a skip.
	hostAuthKeychainTimeout = 20 * time.Second

	// hostAuthKeychainService is the login-keychain service name Claude Code
	// stores its credentials under when it does not use a credentials file.
	hostAuthKeychainService = "Claude Code-credentials"

	// hostAuthGitHubHost is the only host known_hosts material is pinned for.
	// Git over SSH against anything else falls back to the accept-new policy
	// written beside it.
	hostAuthGitHubHost = "github.com"

	hostAuthPrivateMode = "0600"
	hostAuthPublicMode  = "0644"

	// hostAuthDirMode is applied to every guest directory the import creates.
	// 0700 is what OpenSSH requires of ~/.ssh and what the agent caches deserve.
	hostAuthDirMode = "0700"
)

// standardSSHIdentities is the fixed v1 identity set: the file names ssh-agent
// and OpenSSH try by default. Importing arbitrary key paths would need a
// per-node configuration surface and a story for keys the user deliberately
// keeps off a sandbox, so v1 imports only the standard names. The extension
// point is this slice plus a settings-level allowlist (ADR 128).
var standardSSHIdentities = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// hostAuthArtifact is one file to place in the guest. HostPath is what gets
// copied; GuestPath is relative to the guest login user's home.
type hostAuthArtifact struct {
	// Name is the stable, content-free label used in the node event and as the
	// staged file name in the guest. Names are unique within one bundle.
	Name      string
	HostPath  string
	GuestPath string
	Mode      string
}

// hostAuthSkip records one artifact that was not imported, with a reason that
// is safe to persist in an event.
type hostAuthSkip struct {
	Name   string
	Reason string
}

// hostAuthBundle is the result of one host collection pass. stageDir holds
// temp files the collector materialized (filtered known_hosts, the accept-new
// fallback, a Keychain secret); Close removes it.
type hostAuthBundle struct {
	Artifacts []hostAuthArtifact
	Skipped   []hostAuthSkip

	stageDir string
}

func (b *hostAuthBundle) Close() error {
	if b == nil || b.stageDir == "" {
		return nil
	}
	dir := b.stageDir
	b.stageDir = ""
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove host auth staging directory: %w", err)
	}
	return nil
}

// importedNames lists what was imported, by name only.
func (b *hostAuthBundle) importedNames() []string {
	names := make([]string, 0, len(b.Artifacts))
	for _, artifact := range b.Artifacts {
		names = append(names, artifact.Name)
	}
	return names
}

// skippedReasons maps each skipped artifact to why. Reasons are authored
// strings and host paths, never file contents.
func (b *hostAuthBundle) skippedReasons() map[string]string {
	if len(b.Skipped) == 0 {
		return nil
	}
	reasons := make(map[string]string, len(b.Skipped))
	for _, skip := range b.Skipped {
		reasons[skip.Name] = skip.Reason
	}
	return reasons
}

func (b *hostAuthBundle) add(artifact hostAuthArtifact) {
	b.Artifacts = append(b.Artifacts, artifact)
}

func (b *hostAuthBundle) skip(name, reason string) {
	b.Skipped = append(b.Skipped, hostAuthSkip{Name: name, Reason: reason})
}

// stage writes content to a 0600 file inside the bundle's staging directory and
// returns its path. It is the only way a secret with no host file of its own
// reaches the guest: as a path, never as an argument or an environment value.
func (b *hostAuthBundle) stage(name string, content []byte) (string, error) {
	if b.stageDir == "" {
		dir, err := os.MkdirTemp("", "codelima-auth-")
		if err != nil {
			return "", fmt.Errorf("create host auth staging directory: %w", err)
		}
		// MkdirTemp already creates 0700; restate it so the guarantee is local
		// to this file rather than to the standard library's documentation.
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("restrict host auth staging directory: %w", err)
		}
		b.stageDir = dir
	}

	staged := filepath.Join(b.stageDir, name)
	if err := os.WriteFile(staged, content, 0o600); err != nil {
		return "", fmt.Errorf("stage host auth artifact %s: %w", name, err)
	}
	return staged, nil
}

// hostAuthCollector reads the host side of the import. Every input it depends
// on is a field so tests can point it at a temp HOME and a stub Keychain
// instead of mutating process state.
type hostAuthCollector struct {
	home      string
	codexHome string
	// keychain is the platform seam for credentials that live in an OS keyring
	// rather than a file. The non-darwin build returns an error, which the
	// collector reports as an ordinary skip.
	keychain func(ctx context.Context) ([]byte, error)
}

// hostAuthRealHomeSealed is set once, from an init in this package's tests. It
// makes the one function that resolves the developer's actual home refuse to
// run inside a test binary, so no present or future test can read a real
// ~/.ssh or raise a real macOS Keychain prompt during `make test`. Tests
// construct hostAuthCollector directly with a temp home instead; Service.hostAuth
// is the seam they install it through.
var hostAuthRealHomeSealed bool

func newHostAuthCollector() (hostAuthCollector, error) {
	if hostAuthRealHomeSealed {
		return hostAuthCollector{}, errors.New("the host credential collector must be given a test home; it will not read the real one from a test")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return hostAuthCollector{}, fmt.Errorf("resolve host home directory: %w", err)
	}
	return hostAuthCollector{
		home:      home,
		codexHome: os.Getenv("CODEX_HOME"),
		keychain:  platformClaudeKeychainCredentials,
	}, nil
}

// collect gathers every importable artifact. A missing artifact is recorded as
// a skip and never returned as an error: the host simply not having a gitconfig
// must not fail node creation. Only a host-side fault that prevents staging at
// all (no writable temp directory) is an error.
func (c hostAuthCollector) collect(ctx context.Context) (*hostAuthBundle, error) {
	bundle := &hostAuthBundle{}

	// known_hosts material exists to serve an imported identity: pinning host
	// keys or relaxing the checking policy for a guest that has no key to
	// authenticate with buys nothing, so a host with no identity contributes no
	// SSH files at all.
	if c.collectSSHIdentities(bundle) {
		if err := c.collectKnownHosts(bundle); err != nil {
			_ = bundle.Close()
			return nil, err
		}
	}
	c.collectGitConfig(bundle)
	c.collectCodexAuth(bundle)
	if err := c.collectClaudeAuth(ctx, bundle); err != nil {
		_ = bundle.Close()
		return nil, err
	}

	return bundle, nil
}

// collectSSHIdentities adds every standard identity pair present on the host
// and reports whether it found any.
func (c hostAuthCollector) collectSSHIdentities(bundle *hostAuthBundle) bool {
	found := 0
	for _, identity := range standardSSHIdentities {
		privatePath := filepath.Join(c.home, ".ssh", identity)
		if !exists(privatePath) {
			continue
		}
		found++
		bundle.add(hostAuthArtifact{
			Name:      identity,
			HostPath:  privatePath,
			GuestPath: path.Join(".ssh", identity),
			Mode:      hostAuthPrivateMode,
		})

		// The public half is optional: OpenSSH can derive it from the private
		// key, but git hosts and agents read it when it is there.
		publicPath := privatePath + ".pub"
		if !exists(publicPath) {
			continue
		}
		bundle.add(hostAuthArtifact{
			Name:      identity + ".pub",
			HostPath:  publicPath,
			GuestPath: path.Join(".ssh", identity+".pub"),
			Mode:      hostAuthPublicMode,
		})
	}

	if found == 0 {
		bundle.skip("ssh_identity", "no standard SSH identity in "+filepath.Join(c.home, ".ssh"))
	}
	return found != 0
}

// collectKnownHosts pins github.com's host keys from the host's known_hosts.
//
// Hashed known_hosts entries (HashKnownHosts, the default on many
// distributions) store an HMAC of the hostname, so an entry for github.com is
// unrecognizable without the salt-and-compare that only ssh performs. There is
// therefore no way to select the right lines out of a hashed file. Rather than
// import a file whose contents cannot be verified — or leave the guest with no
// policy at all, which makes the first `git fetch` hang on an interactive
// fingerprint prompt no agent can answer — the collector falls back to a guest
// ~/.ssh/config that scopes StrictHostKeyChecking=accept-new to github.com.
func (c hostAuthCollector) collectKnownHosts(bundle *hostAuthBundle) error {
	knownHostsPath := filepath.Join(c.home, ".ssh", "known_hosts")
	data, err := os.ReadFile(knownHostsPath)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		data = nil
	default:
		bundle.skip("known_hosts", "unreadable "+knownHostsPath)
		data = nil
	}

	lines := knownHostsLinesForHost(data, hostAuthGitHubHost)
	if len(lines) > 0 {
		staged, stageErr := bundle.stage("known_hosts", []byte(strings.Join(lines, "\n")+"\n"))
		if stageErr != nil {
			return stageErr
		}
		bundle.add(hostAuthArtifact{
			Name:      "known_hosts",
			HostPath:  staged,
			GuestPath: path.Join(".ssh", "known_hosts"),
			Mode:      hostAuthPublicMode,
		})
		return nil
	}

	if !hasSkip(bundle, "known_hosts") {
		bundle.skip("known_hosts", "no plaintext "+hostAuthGitHubHost+" entry in "+knownHostsPath)
	}

	fallback := "Host " + hostAuthGitHubHost + "\n    StrictHostKeyChecking accept-new\n"
	staged, stageErr := bundle.stage("ssh_config", []byte(fallback))
	if stageErr != nil {
		return stageErr
	}
	// 0600 rather than the 0644 used for other public material: OpenSSH refuses
	// a config file that is writable by anyone but its owner, and a config is
	// policy the guest must not be able to weaken from another account.
	bundle.add(hostAuthArtifact{
		Name:      "ssh_config",
		HostPath:  staged,
		GuestPath: path.Join(".ssh", "config"),
		Mode:      hostAuthPrivateMode,
	})
	return nil
}

func (c hostAuthCollector) collectGitConfig(bundle *hostAuthBundle) {
	gitConfigPath := filepath.Join(c.home, ".gitconfig")
	if !exists(gitConfigPath) {
		bundle.skip("gitconfig", "no "+gitConfigPath)
		return
	}
	bundle.add(hostAuthArtifact{
		Name:      "gitconfig",
		HostPath:  gitConfigPath,
		GuestPath: ".gitconfig",
		Mode:      hostAuthPublicMode,
	})
}

// collectCodexAuth imports the documented portable Codex auth cache. Copying
// $CODEX_HOME/auth.json into another machine's $CODEX_HOME is the flow the
// Codex CLI documents for authenticating locally and reusing the session
// elsewhere; the tokens then refresh in place inside the guest.
func (c hostAuthCollector) collectCodexAuth(bundle *hostAuthBundle) {
	codexHome := strings.TrimSpace(c.codexHome)
	if codexHome == "" {
		codexHome = filepath.Join(c.home, ".codex")
	}
	authPath := filepath.Join(codexHome, "auth.json")
	if !exists(authPath) {
		bundle.skip("codex", "no "+authPath)
		return
	}
	// The guest path is always ~/.codex/auth.json regardless of the host's
	// CODEX_HOME: the guest has no such override, and the CLI's default is
	// where it will look.
	bundle.add(hostAuthArtifact{
		Name:      "codex",
		HostPath:  authPath,
		GuestPath: path.Join(".codex", "auth.json"),
		Mode:      hostAuthPrivateMode,
	})
}

// collectClaudeAuth prefers the credentials file and falls back to the platform
// keyring seam. Claude Code stores credentials in a file on Linux and in the
// macOS login Keychain on darwin, so on a macOS host the file is usually
// absent and the seam is the only source.
func (c hostAuthCollector) collectClaudeAuth(ctx context.Context, bundle *hostAuthBundle) error {
	credentialsPath := filepath.Join(c.home, ".claude", ".credentials.json")
	if exists(credentialsPath) {
		bundle.add(hostAuthArtifact{
			Name:      "claude",
			HostPath:  credentialsPath,
			GuestPath: path.Join(".claude", ".credentials.json"),
			Mode:      hostAuthPrivateMode,
		})
		return nil
	}

	if c.keychain == nil {
		bundle.skip("claude", "no "+credentialsPath)
		return nil
	}

	keychainCtx, cancel := context.WithTimeout(ctx, hostAuthKeychainTimeout)
	defer cancel()
	secret, err := c.keychain(keychainCtx)
	if err != nil {
		// Every failure shape lands here on purpose: no keyring on this
		// platform, no such item, a denied prompt, or a prompt that timed out.
		// None of them is worth failing a node creation over, and the error
		// text comes from the tool, not from the secret.
		bundle.skip("claude", "no "+credentialsPath+" and keychain lookup failed: "+err.Error())
		return nil
	}
	// Normalize here rather than in each platform seam: `security -w` prints a
	// trailing newline, and the credential file the guest reads should be the
	// blob and nothing else.
	secret = bytes.TrimSpace(secret)
	if len(secret) == 0 {
		bundle.skip("claude", "keychain returned an empty credential")
		return nil
	}

	staged, stageErr := bundle.stage("claude", secret)
	if stageErr != nil {
		return stageErr
	}
	bundle.add(hostAuthArtifact{
		Name:      "claude",
		HostPath:  staged,
		GuestPath: path.Join(".claude", ".credentials.json"),
		Mode:      hostAuthPrivateMode,
	})
	return nil
}

func hasSkip(bundle *hostAuthBundle, name string) bool {
	for _, skip := range bundle.Skipped {
		if skip.Name == name {
			return true
		}
	}
	return false
}

// knownHostsLinesForHost returns the plaintext known_hosts lines that name
// host. Hashed entries (`|1|salt|hash`) are unmatchable by construction and are
// ignored; so are comments and blank lines. Patterns are compared after
// stripping the `[host]:port` bracket form.
func knownHostsLinesForHost(data []byte, host string) []string {
	if len(data) == 0 {
		return nil
	}

	host = strings.ToLower(host)
	matched := make([]string, 0, 4)
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		fields := strings.Fields(trimmed)
		// A marker line (@cert-authority, @revoked) shifts the pattern field
		// right by one.
		if strings.HasPrefix(fields[0], "@") {
			fields = fields[1:]
		}
		if len(fields) == 0 || strings.HasPrefix(fields[0], "|") {
			continue
		}

		for _, pattern := range strings.Split(fields[0], ",") {
			if knownHostsPatternHost(pattern) == host {
				matched = append(matched, line)
				break
			}
		}
	}

	if len(matched) == 0 {
		return nil
	}
	return matched
}

// knownHostsPatternHost normalizes one known_hosts host pattern to a bare
// lowercase hostname. `[github.com]:22` and `github.com` both yield
// "github.com"; negations and wildcards yield something that will not match.
func knownHostsPatternHost(pattern string) string {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if strings.HasPrefix(pattern, "[") {
		if end := strings.LastIndex(pattern, "]"); end > 0 {
			pattern = pattern[1:end]
		}
	}
	return pattern
}

// guestAuthStagingDir is the per-node guest scratch path the copies land in.
// limactl cp runs as Lima's unprivileged login user, so the destination has to
// be writable by that user — it cannot be the final 0700 home directories,
// which are created and owned by the root placement step below.
func guestAuthStagingDir(node Node) string {
	return "/tmp/codelima-auth-" + node.ID
}

// guestAuthStagePrepareScript creates the staging directory owned by the login
// user, mirroring the workspace-seed prepare command's chown-then-copy shape.
func guestAuthStagePrepareScript(stagingDir string) string {
	return strings.Join([]string{
		"set -e",
		"staging=" + shellQuote(stagingDir),
		`guest_user="${SUDO_USER:-$(id -un)}"`,
		`guest_group="$(id -gn "$guest_user")"`,
		`rm -rf "$staging"`,
		`mkdir -p "$staging"`,
		`chown "$guest_user:$guest_group" "$staging"`,
		"chmod " + hostAuthDirMode + ` "$staging"`,
	}, "\n")
}

// guestAuthTargetHome is one home directory the import installs into, expressed
// as the shell fragments that name it. The fragments are already quoted or are
// variable references, so the caller concatenates rather than quotes them.
type guestAuthTargetHome struct {
	homeRef  string
	ownerRef string
	groupRef string
	// ensure emits a mkdir -p before the installs. It is mkdir and not
	// `install -d` on purpose: install -d applies a mode to a directory that
	// already exists, and nothing here has any business loosening the mode of a
	// home directory the image already shipped.
	ensure bool
}

// guestAuthTargetHomes lists both homes every artifact is installed into.
//
// The guest has TWO working identities and an agent may be reached through
// either. The login user is the primary one: bootstrap, agent installation, and
// the validation probe drop to it (`sudo -u "$guest_user" -H env
// HOME="$guest_home"`, see config.go), and since ADR 129 managed terminals and
// `codelima shell` run as it too, so its home is where an agent normally looks.
// Root's home covers the sessions that are explicitly privileged — a `sudo -i`
// or `sudo claude` the user types, and CodeLima's own service-issued
// provisioning commands, all of which run with HOME=/root and would otherwise
// find no credentials at all.
//
// Root's home is resolved from passwd rather than hardcoded because that is
// exactly what `sudo -H` reads to set HOME, and it uses the same getent idiom
// as the login user two lines above. /root is the fallback. CodeLima has no
// concept of a privileged user other than root — nothing in the codebase
// configures one — so there is no third home to consider.
//
// Filling only the login user's home is now the obvious simplification, and
// ADR 129 records it as deliberately deferred rather than done: this pass is
// tested and shipped, and dropping a home is a separate, reversible decision.
//
// The two copies diverge after this command and that is intended: each is an
// independent holder of its own refresh chain, exactly as the host's copy is.
func guestAuthTargetHomes() []guestAuthTargetHome {
	return []guestAuthTargetHome{
		{homeRef: `"$guest_home"`, ownerRef: `"$guest_user"`, groupRef: `"$guest_group"`},
		{homeRef: `"$root_home"`, ownerRef: "root", groupRef: "root", ensure: true},
	}
}

// guestAuthPlacementScript installs every staged file into BOTH guest homes
// with its final owner and mode, in ONE root command.
//
// It is one command because limactl cp leaves files owned by Lima's default
// user with whatever mode scp chose, and a private key must not sit in that
// state for longer than it has to. The staging directory is 0700 for the same
// reason, and the trap removes it whether the placement succeeds or fails, so
// no copied secret survives a failed import.
//
// Both homes are filled from the SAME staged files — the host is read once and
// copied once. Either pass failing fails the whole placement, which keeps the
// two homes carrying identical artifact sets and lets the event report one list.
func guestAuthPlacementScript(stagingDir string, artifacts []hostAuthArtifact) string {
	lines := []string{
		"set -e",
		"staging=" + shellQuote(stagingDir),
		`cleanup() { rm -rf "$staging"; }`,
		"trap cleanup EXIT",
		`guest_user="${SUDO_USER:-$(id -un)}"`,
		`guest_group="$(id -gn "$guest_user")"`,
		`guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"`,
		`test -n "$guest_home"`,
		`root_home="$(getent passwd root | cut -d: -f6)"`,
		`test -n "$root_home" || root_home=/root`,
	}

	directories := guestAuthDirectories(artifacts)
	for _, home := range guestAuthTargetHomes() {
		if home.ensure {
			lines = append(lines, "mkdir -p "+home.homeRef)
		}
		for _, dir := range directories {
			lines = append(lines, "install -d -m "+hostAuthDirMode+" -o "+home.ownerRef+" -g "+home.groupRef+" "+home.homeRef+"/"+shellQuote(dir))
		}
		for _, artifact := range artifacts {
			lines = append(lines, "install -m "+artifact.Mode+" -o "+home.ownerRef+" -g "+home.groupRef+` "$staging"/`+shellQuote(artifact.Name)+" "+home.homeRef+"/"+shellQuote(artifact.GuestPath))
		}
	}

	return strings.Join(lines, "\n")
}

// guestAuthDirectories is the sorted set of home-relative directories the
// artifacts need. The home directory itself (".gitconfig") contributes nothing.
func guestAuthDirectories(artifacts []hostAuthArtifact) []string {
	seen := map[string]struct{}{}
	dirs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		dir := path.Dir(artifact.GuestPath)
		if dir == "." || dir == "/" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

// importHostAuth runs the one-shot creation-time import for node. It is called
// from NodeStart at the first moment a guest is running and before any
// bootstrap or agent validation, so an agent that validates is already
// authenticated. Callers must have checked the frozen opt-in and the one-shot
// marker.
func (s *Service) importHostAuth(ctx context.Context, node Node) error {
	newCollector := s.hostAuth
	if newCollector == nil {
		newCollector = newHostAuthCollector
	}
	collector, err := newCollector()
	if err != nil {
		return err
	}

	bundle, err := collector.collect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := bundle.Close(); closeErr != nil {
			s.log().Warn("failed to remove host auth staging directory", "node", node.ID, "error", closeErr)
		}
	}()

	for _, skipped := range bundle.Skipped {
		s.log().Warn("skipping host credential artifact", "node", node.ID, "artifact", skipped.Name, "reason", skipped.Reason)
	}

	if len(bundle.Artifacts) != 0 {
		if err := s.copyHostAuthToGuest(ctx, node, bundle); err != nil {
			return err
		}
	}

	// One artifact list, not one per home: the placement installs the same
	// staged files into both the login user's home and root's, and either pass
	// failing fails the whole command, so the two homes always carry identical
	// sets. Names and skip reasons only — never contents.
	return s.withLocks(ctx, nil, []string{node.ID}, func() error {
		return s.store.AppendNodeEvent(node.ID, Event{
			Timestamp: s.now(),
			Type:      "node.auth.imported",
			Fields: map[string]any{
				"imported": bundle.importedNames(),
				"skipped":  bundle.skippedReasons(),
			},
		})
	})
}

func (s *Service) copyHostAuthToGuest(ctx context.Context, node Node, bundle *hostAuthBundle) error {
	stagingDir := guestAuthStagingDir(node)
	// Both guest commands here are root: the staging directory is created and
	// chowned to the login user before the copy, and the placement installs into
	// root's home as well as the login user's. Neither is anything the user
	// typed — they are service-issued provisioning (ADR 129).
	if err := s.sandbox.Shell(ctx, node, guestRootUser, []string{"sh", "-lc", guestAuthStagePrepareScript(stagingDir)}, "", false, ShellStreams{}); err != nil {
		return fmt.Errorf("prepare guest credential staging directory: %w", err)
	}

	for _, artifact := range bundle.Artifacts {
		if err := s.sandbox.CopyToGuest(ctx, node, artifact.HostPath, path.Join(stagingDir, artifact.Name), false); err != nil {
			return fmt.Errorf("copy credential artifact %s to the guest: %w", artifact.Name, err)
		}
	}

	if err := s.sandbox.Shell(ctx, node, guestRootUser, []string{"sh", "-lc", guestAuthPlacementScript(stagingDir, bundle.Artifacts)}, "", false, ShellStreams{}); err != nil {
		return fmt.Errorf("place imported credentials in the guest home: %w", err)
	}
	return nil
}
