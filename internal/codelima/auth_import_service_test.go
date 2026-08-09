package codelima

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubHostAuth points a Service's credential import at a temp HOME. Nothing in
// these tests may read the real one.
func stubHostAuth(service *Service, home string) {
	service.hostAuth = func() (hostAuthCollector, error) {
		return hostAuthCollector{home: home}, nil
	}
}

// seedHostCredentials creates a plausible host credential set out of generated
// throwaway values and returns its HOME.
func seedHostCredentials(t *testing.T) string {
	t.Helper()
	home := collectorHome(t)
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519"), throwawaySecret(t, "ed25519"))
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519.pub"), "ssh-ed25519 "+throwawaySecret(t, "pub"))
	writeHostFile(t, filepath.Join(home, ".ssh", "known_hosts"), "github.com ssh-ed25519 AAAAgithubkey\n")
	writeHostFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = Test\n")
	writeHostFile(t, filepath.Join(home, ".codex", "auth.json"), `{"token":"`+throwawaySecret(t, "codex")+`"}`)
	writeHostFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"`+throwawaySecret(t, "claude")+`"}`)
	return home
}

func authImportEvents(t *testing.T, service *Service, nodeID string) []Event {
	t.Helper()
	events, err := service.store.NodeEvents(nodeID)
	if err != nil {
		t.Fatalf("NodeEvents() error = %v", err)
	}
	matched := make([]Event, 0, 1)
	for _, event := range events {
		if event.Type == "node.auth.imported" {
			matched = append(matched, event)
		}
	}
	return matched
}

// countCopiesTo counts host→guest copies landing at exactly targetPath. It
// compares the recorded target rather than the flattened call string so
// "id_ed25519" cannot also count "id_ed25519.pub".
func countCopiesTo(fake *fakeSandbox, targetPath string) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	count := 0
	for _, copyCall := range fake.copyCalls {
		if copyCall.targetPath == targetPath {
			count++
		}
	}
	return count
}

func callsMatching(fake *fakeSandbox, substring string) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	matched := make([]string, 0, 4)
	for _, call := range fake.calls {
		if strings.Contains(call, substring) {
			matched = append(matched, call)
		}
	}
	return matched
}

func TestNodeStartImportsHostCredentialsOnFirstStartOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	stubHostAuth(service, seedHostCredentials(t))
	fake := service.sandbox.(*fakeSandbox)

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "auth-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if !node.ImportHostAuth {
		t.Fatalf("new nodes must default to importing host credentials: %+v", node)
	}
	// Creation only renders a template and creates a stopped instance, so no
	// credential may have reached a guest yet.
	if calls := callsMatching(fake, "codelima-auth-"); len(calls) != 0 {
		t.Fatalf("credentials copied at creation time: %v", calls)
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}
	if !node.AuthImportCompleted {
		t.Fatalf("first start did not mark the credential import complete: %+v", node)
	}

	staging := guestAuthStagingDir(node)
	for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "gitconfig", "codex", "claude"} {
		want := "copy " + node.SandboxName + " "
		found := false
		for _, call := range callsMatching(fake, staging+"/"+name) {
			if strings.HasPrefix(call, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("artifact %q was not copied to the guest staging directory: %v", name, callsMatching(fake, "codelima-auth-"))
		}
	}

	placement := ""
	for _, call := range callsMatching(fake, "install -m") {
		placement = call
	}
	if placement == "" {
		t.Fatalf("no guest placement command ran: %v", fake.calls)
	}
	// Staging and placement are service-issued: the staging directory is chowned
	// to the login user before the copy, and the placement writes into root's
	// home. Both keep the root identity (ADR 129).
	for _, call := range recordedShellCalls(fake) {
		script := strings.Join(call.command, " ")
		if !strings.Contains(script, staging) {
			continue
		}
		if call.identity != guestRootUser {
			t.Fatalf("credential import command ran as %q, want %q: %s", call.identity, guestRootUser, script)
		}
	}
	// The login user's home serves bootstrap, agent validation, and — since
	// ADR 129 — managed terminals and `codelima shell`; root's home serves the
	// explicitly privileged sessions (`sudo -i`, `sudo claude`) and CodeLima's
	// own service-issued provisioning. Both must be filled or an agent reached
	// through the other identity prompts for a login.
	for _, want := range []string{
		`install -d -m 0700 -o "$guest_user" -g "$guest_group" "$guest_home"/'.ssh'`,
		`install -m 0600 -o "$guest_user" -g "$guest_group" "$staging"/'id_ed25519' "$guest_home"/'.ssh/id_ed25519'`,
		`install -m 0644 -o "$guest_user" -g "$guest_group" "$staging"/'known_hosts' "$guest_home"/'.ssh/known_hosts'`,
		`install -m 0600 -o "$guest_user" -g "$guest_group" "$staging"/'codex' "$guest_home"/'.codex/auth.json'`,
		`install -m 0600 -o "$guest_user" -g "$guest_group" "$staging"/'claude' "$guest_home"/'.claude/.credentials.json'`,

		`install -d -m 0700 -o root -g root "$root_home"/'.ssh'`,
		`install -m 0600 -o root -g root "$staging"/'id_ed25519' "$root_home"/'.ssh/id_ed25519'`,
		`install -m 0644 -o root -g root "$staging"/'known_hosts' "$root_home"/'.ssh/known_hosts'`,
		`install -m 0600 -o root -g root "$staging"/'codex' "$root_home"/'.codex/auth.json'`,
		`install -m 0600 -o root -g root "$staging"/'claude' "$root_home"/'.claude/.credentials.json'`,
	} {
		if !strings.Contains(placement, want) {
			t.Fatalf("placement command missing %q:\n%s", want, placement)
		}
	}

	// Both homes are served by one staged copy per artifact: the host is read
	// once and copied to the guest once.
	for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "gitconfig", "codex", "claude"} {
		if got := countCopiesTo(fake, staging+"/"+name); got != 1 {
			t.Fatalf("artifact %q was copied from the host %d times, want exactly 1", name, got)
		}
	}

	// The agent that gets validated at the end of a start must already be
	// authenticated, so every credential command precedes the bootstrap and
	// validation commands.
	assertAuthImportPrecedesBootstrap(t, fake)

	events := authImportEvents(t, service, node.ID)
	if len(events) != 1 {
		t.Fatalf("node.auth.imported events = %d, want 1", len(events))
	}

	firstStartCalls := len(callsMatching(fake, "codelima-auth-"))
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart(second) error = %v", err)
	}
	if got := len(callsMatching(fake, "codelima-auth-")); got != firstStartCalls {
		t.Fatalf("second start re-imported credentials: %d calls, want %d", got, firstStartCalls)
	}
	if events := authImportEvents(t, service, node.ID); len(events) != 1 {
		t.Fatalf("second start appended another import event: %d", len(events))
	}
}

func assertAuthImportPrecedesBootstrap(t *testing.T, fake *fakeSandbox) {
	t.Helper()
	fake.mu.Lock()
	calls := append([]string(nil), fake.calls...)
	fake.mu.Unlock()

	lastAuth, firstBootstrap := -1, -1
	for index, call := range calls {
		if strings.Contains(call, "codelima-auth-") {
			lastAuth = index
		}
		if firstBootstrap < 0 && strings.Contains(call, "npm install -g") {
			firstBootstrap = index
		}
	}
	if lastAuth < 0 || firstBootstrap < 0 {
		t.Fatalf("expected both credential and bootstrap commands, got %v", calls)
	}
	if lastAuth > firstBootstrap {
		t.Fatalf("credential import ran after bootstrap: auth=%d bootstrap=%d\n%v", lastAuth, firstBootstrap, calls)
	}
}

func TestNodeStartSkipsCredentialImportWhenFrozenChoiceIsOff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	stubHostAuth(service, seedHostCredentials(t))
	fake := service.sandbox.(*fakeSandbox)

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:      workspace,
		Slug:           "no-auth-node",
		ImportHostAuth: boolPointer(false),
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if node.ImportHostAuth {
		t.Fatalf("explicit opt-out was not frozen onto the node: %+v", node)
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if calls := callsMatching(fake, "codelima-auth-"); len(calls) != 0 {
		t.Fatalf("opted-out node still ran credential commands: %v", calls)
	}
	if events := authImportEvents(t, service, node.ID); len(events) != 0 {
		t.Fatalf("opted-out node recorded an import event: %#v", events)
	}
	// The one-shot marker is still set, so the decision is not revisited on
	// every later start.
	if !node.AuthImportCompleted {
		t.Fatalf("opted-out node did not settle the import decision: %+v", node)
	}
}

func TestNodeStartRecordsImportedAndSkippedArtifactsByNameOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	// A host with a git identity and nothing else exercises every skip path in
	// one start.
	home := collectorHome(t)
	privateKey := throwawaySecret(t, "ed25519")
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519"), privateKey)
	stubHostAuth(service, home)

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "partial-auth-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	events := authImportEvents(t, service, node.ID)
	if len(events) != 1 {
		t.Fatalf("node.auth.imported events = %d, want 1", len(events))
	}
	payload, err := json.Marshal(events[0])
	if err != nil {
		t.Fatalf("Marshal(event) error = %v", err)
	}
	text := string(payload)

	for _, want := range []string{`"id_ed25519"`, `"ssh_config"`, `"gitconfig"`, `"codex"`, `"claude"`, `"known_hosts"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("event payload missing name %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, privateKey) {
		t.Fatalf("event payload leaked credential content:\n%s", text)
	}

	// The whole events log must be free of the secret, not just this record.
	logData, err := os.ReadFile(service.store.nodeEventsPath(node.ID))
	if err != nil {
		t.Fatalf("ReadFile(events) error = %v", err)
	}
	if strings.Contains(string(logData), privateKey) {
		t.Fatalf("events log leaked credential content")
	}
}

func TestNodeStartCredentialImportKeepsSecretsOutOfGuestCommands(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	home := collectorHome(t)
	privateKey := throwawaySecret(t, "ed25519")
	codexToken := throwawaySecret(t, "codex")
	writeHostFile(t, filepath.Join(home, ".ssh", "id_ed25519"), privateKey)
	writeHostFile(t, filepath.Join(home, ".codex", "auth.json"), `{"token":"`+codexToken+`"}`)
	stubHostAuth(service, home)

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "argv-safety-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	secrets := []string{privateKey, codexToken}

	// Guard against a vacuous pass: the dual-home placement lines this test is
	// meant to cover have to actually be among the commands it scans.
	placement := callsMatching(fake, `-o root -g root "$staging"/`)
	if len(placement) != 1 {
		t.Fatalf("expected exactly one command carrying the root-home install lines, got %d", len(placement))
	}
	for _, home := range []string{`"$guest_home"/`, `"$root_home"/`} {
		if !strings.Contains(placement[0], home) {
			t.Fatalf("placement command does not install into %s:\n%s", home, placement[0])
		}
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	// Every guest command, including both install passes of the placement.
	for _, call := range fake.calls {
		for _, secret := range secrets {
			if strings.Contains(call, secret) {
				t.Fatalf("credential content reached a guest command: %s", call)
			}
		}
	}
	// Every copy argument, recursive or not: paths only, never contents.
	for _, copyCall := range fake.copyCalls {
		for _, secret := range secrets {
			if strings.Contains(copyCall.sourcePath, secret) || strings.Contains(copyCall.targetPath, secret) {
				t.Fatalf("credential content reached a copy argument: %+v", copyCall)
			}
		}
	}
}

func TestNodeCreateFreezesTheSettingsCredentialImportDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	service.cfg.ImportHostAuth = false

	defaulted, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "settings-default-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if defaulted.ImportHostAuth {
		t.Fatalf("settings default was ignored: %+v", defaulted)
	}

	// An explicit answer outranks the settings default in both directions.
	overridden, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:      workspace,
		Slug:           "settings-override-node",
		ImportHostAuth: boolPointer(true),
	})
	if err != nil {
		t.Fatalf("NodeCreate(override) error = %v", err)
	}
	if !overridden.ImportHostAuth {
		t.Fatalf("explicit opt-in did not override the settings default: %+v", overridden)
	}

	// The frozen choice must round-trip through node.yaml.
	reloaded, err := service.store.NodeByID(defaulted.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	if reloaded.ImportHostAuth || reloaded.AuthImportCompleted {
		t.Fatalf("persisted credential import state = %+v", reloaded)
	}
}

func TestNodeCloneInheritsFrozenCredentialImportState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	stubHostAuth(service, seedHostCredentials(t))

	source, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "clone-auth-source"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(ctx, source.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	child, err := service.NodeClone(ctx, NodeCloneInput{SourceNode: source.ID, NodeSlug: "clone-auth-child"})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}
	if !child.ImportHostAuth || !child.AuthImportCompleted {
		t.Fatalf("clone did not inherit the source's credential import state: %+v", child)
	}

	fake := service.sandbox.(*fakeSandbox)
	before := len(callsMatching(fake, "codelima-auth-"))
	if _, err := service.NodeStart(ctx, child.ID); err != nil {
		t.Fatalf("NodeStart(child) error = %v", err)
	}
	// The clone booted from a disk that already carries the imported
	// credentials, so starting it must not copy them again.
	if got := len(callsMatching(fake, "codelima-auth-")); got != before {
		t.Fatalf("clone start re-imported credentials: %d calls, want %d", got, before)
	}
}
