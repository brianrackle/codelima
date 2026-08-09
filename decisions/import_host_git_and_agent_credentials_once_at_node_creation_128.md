# Import host git and agent credentials once, at node creation

## Context and Problem Statement

A node is created so an agent can work in it. The agent cannot work until it can reach GitHub and until Codex or Claude Code is signed in, and both of those are host facts the guest has no way to discover. Until now every fresh node started at the same place: a `git push` that fails on a missing key, a `codex` that opens a browser-login flow nobody is watching, a `claude` that asks for a token in a pane the user has not switched to yet. The node exists, the agent validates, and the first useful command still fails.

The material that would fix this is sitting on the host — `~/.ssh/id_ed25519`, `~/.gitconfig`, `~/.codex/auth.json`, `~/.claude/.credentials.json` or its macOS Keychain equivalent. Copying it into the guest is not the hard part. The hard part is *when*, and *how many times*: the Codex and Claude caches are not static secrets but the head of a refresh-token chain, and a chain has an owner.

## Decision Drivers

* A newly created node should be able to push and run its agent without a second login.
* The Codex/Claude caches carry refresh tokens. Each copy that gets used rotates independently, and a rotated chain invalidates its predecessor.
* Node creation must not become fragile. The host not having a `~/.gitconfig` is normal, and so is a macOS Keychain prompt that nobody answers.
* Secrets must not leak into the places CodeLima writes freely: structured logs, the node events log, error messages, and the argv of commands the runtime records.
* Ownership and mode have to be right per identity, or ssh refuses the key and the agent cannot read its own cache — and the guest runs work under two identities, not one.
* Some users will not want their credentials in a sandbox at all, and that has to be expressible before the node exists.

## Considered Options

* Import on every start, keeping the guest continuously in sync with the host.
* Import once, at creation.
* Mount or forward instead of copying: an SSH agent socket, a bind-mounted `~/.codex`.
* Do nothing; document a manual `limactl copy` recipe.

## Decision Outcome

Chosen option: **import once, at the first bring-up of a node whose frozen `import_host_auth` choice is on**, defaulting on.

### Why once, and not on every start

`auth.json` is not a password. It is a refresh token plus a short-lived access token, and the CLI rewrites the file in place every time it refreshes. Two copies of that file are two independent claims on the same chain: whichever one refreshes first rotates the refresh token, and the other copy is now holding an invalidated one.

That is fine and expected — the host and each guest simply diverge into their own chains after the copy, which is exactly what the Codex CLI's documented "authenticate locally and copy your auth cache" flow relies on. What is *not* fine is re-copying later. A node that has been running for a week has refreshed its own chain many times; overwriting its `auth.json` with the host's replaces a live chain with one the host may itself have rotated past, and the guest is logged out with no way to recover except a login the agent cannot perform. A sync-on-every-start design turns a working node into a broken one at an arbitrary later time, for no benefit the user asked for.

So the import is a *seeding* operation, in the same family as copy-mode workspace seeding, and it gets the same shape: a one-shot marker on the node record.

### Where in the lifecycle

The user's mental model is "during node creation", but `NodeCreate` renders a Lima template and runs `limactl create`, which leaves the instance **stopped**. There is no guest to copy into at creation time. The first moment a running guest exists is `NodeStart`, after the `sandbox.Start` block.

The import therefore runs in `NodeStart`, immediately after the VM is up and *before* workspace seeding, the bootstrap command loop, and the agent validation probe. Running before validation is the point: the agent that gets validated at the end of that function is an already-authenticated one.

It is gated on the node record, not on this call:

* `Node.ImportHostAuth` — the frozen creation-time choice, beside `WorkspaceMode` and the frozen resources.
* `Node.AuthImportCompleted` — the one-shot marker, in the shape of `WorkspaceSeeded`, persisted immediately after the step and set whether the import ran or was declined.

Persisting the marker before bootstrap means a start that fails halfway through an `npm install` and gets retried re-runs bootstrap without re-copying credentials the guest may already have rotated. That is the whole reason the gate is a durable field rather than a local boolean.

A node record written before these fields existed decodes both as `false`. That is the correct reading: nobody chose an import for it, and a node created under an older build has usually already been started and provisioned by hand.

### Why default on

The alternative — default off, opt in per node — makes the feature invisible. The user who would benefit most is the one who has not yet discovered that a fresh node cannot push, and they discover it as a failure, not as a setting. Default-on with three visible opt-outs (a settings key, a `node create` flag, a field in the TUI create dialog) puts the choice in front of anyone who wants it while making the common case work with no configuration.

The blast radius is bounded by what a node already is. A CodeLima node is a VM the user created to run an agent on their own code, with their own workspace mounted into it. A user who is unwilling to put their git key in that VM is generally also unwilling to mount the repository into it.

### The macOS Keychain fallback

Claude Code stores credentials in `~/.claude/.credentials.json` on Linux and in the macOS login Keychain on darwin, so on the most common CodeLima host the file is simply not there. The collector prefers the file and falls back to `security find-generic-password -s "Claude Code-credentials" -w` behind a small platform seam (`platformClaudeKeychainCredentials`, one implementation per build tag, injected as a struct field so tests can stub it without touching process state).

The service name is the documented one, but it is a string in another product's source tree and it can change. Every failure shape is therefore treated identically and benignly: no keychain on this platform, no such item, a denied prompt, an empty result. All of them are skips.

The prompt is the real hazard. `security` can raise a modal authorization dialog, and a node created from the daemon or a detached TUI has nobody to answer it. The call is bounded by a 20-second timeout and the process is killed when it expires, so the worst case is a node that starts twenty seconds late without Claude credentials — never one that hangs forever.

### Standard identities only

v1 imports exactly `id_ed25519`, `id_ecdsa`, `id_rsa` and their `.pub` halves: the names OpenSSH tries by default. Importing arbitrary key paths needs a configuration surface, and a per-node one, because "which of my keys may enter a sandbox" is a real question with a per-node answer. Shipping a half-answer now would be worse than shipping none.

The extension point is deliberate and narrow: `standardSSHIdentities` is one slice, and the natural growth is a settings-level allowlist of additional identity names resolved against `~/.ssh` — not arbitrary absolute paths, which would let a typo copy something that is not a key at all.

### known_hosts and the accept-new fallback

Pinning github.com's host keys from the host's `known_hosts` is the strict-correct behavior, and it works whenever those lines are plaintext.

It cannot work when they are hashed. `HashKnownHosts` stores an HMAC of the hostname under a per-line salt, so selecting "the github.com lines" out of a hashed file requires the salt-and-compare that only `ssh` itself performs. There is no partial credit available: the lines are unreadable by hostname, full stop.

Importing the whole file instead would move every host the user has ever connected to into the sandbox, which is both more than was asked for and unverifiable. Importing nothing leaves the guest's first `git fetch` blocked on an interactive fingerprint prompt that no agent can answer — a hang, not an error. So when no plaintext line matches, the import writes a guest `~/.ssh/config` scoping `StrictHostKeyChecking accept-new` to `github.com` alone. That is trust-on-first-use for one host, inside a VM the user just created, and it is the narrowest thing that keeps the failure mode an error rather than a hang.

SSH material — pinned keys or the fallback — is written only when at least one identity was imported. Host-key policy for a guest with no key to authenticate with buys nothing.

### Both homes, because the guest has two identities

The first version of this import filled one home — the login user's — and it was wrong in the way that mattered most: the user's own terminal could not use any of it.

A CodeLima guest has two working identities, and which one you get depends on how you arrived:

* **The login user.** Bootstrap, agent installation, and the validation probe all explicitly drop to it (`sudo -u "$guest_user" -H env HOME="$guest_home" …`, see `config.go`). This is the identity the agent's own install and `--version` check run as.
* **root.** Any command that crosses Lima's passwordless sudo boundary runs with `HOME=/root` — CodeLima's own service-issued provisioning commands, and a `sudo -i` or `sudo claude` the user types in their terminal.

A single-home import satisfied the validator and then failed the user. `codex` in the first real node read `/root/.codex/auth.json`, found nothing, and opened a login prompt; the same held for `claude`, the SSH key, `known_hosts`, and `.gitconfig`. The import was working exactly as designed, and the design had asked the wrong question.

> **Amended by [ADR 129](run_managed_terminals_as_the_lima_login_user_129.md).** When this decision was written, `LimaClient.Shell` wrapped *every* guest command in `sudo -H --`, so the managed terminal itself ran as root and the root home was where the user's first command looked. Managed terminals now run as the login user, which makes the login user's home the primary one. The root pass is kept — its justification is simply narrower now: explicit `sudo` sessions the user opens, and the provisioning commands CodeLima issues. Collapsing to a single home is recorded there as deliberately deferred.

So every artifact is installed twice, into both homes, with the same contents and the same modes. Root's home is read from passwd rather than hardcoded, because that is precisely what `sudo -H` reads to set `HOME`, and it reuses the getent idiom already used two lines above for the login user; `/root` is the fallback. There is no third home to consider — nothing in the codebase configures a privileged user other than root.

Two copies of a refresh chain in one guest is the same arrangement as host-and-guest, and it is fine for the same reason: each copy is an independent holder that rotates its own chain from the moment it is written. What would break either copy is a *later* overwrite, and the once-only rule already forbids that.

### Keeping the secrets out of the record

The transfer is file-to-file, and the host is read once. `limactl cp` runs as Lima's unprivileged login user, so copies land in a per-node `/tmp/codelima-auth-<node-id>` staging directory that a prepare command has already created `0700` and chowned to that user (the same chown-then-copy shape the workspace seed prepare command uses). One root command then `install`s each staged file into *both* homes with its final owner and mode and removes the staging directory under a trap, so no copied secret survives a failed placement. `.ssh`, `.codex` and `.claude` are created `0700` in each home; private keys and both credential caches land `0600`; public keys, `known_hosts` and `.gitconfig` land `0644`.

Either install pass failing fails the whole command, which is what lets the `node.auth.imported` event report a single artifact list: the two homes always carry identical sets, so there is nothing per-home to record. Root's home directory is created with `mkdir -p`, never `install -d`, because `install -d` applies a mode to a directory that already exists and nothing here should be loosening the mode of a `/root` the image already shipped.

A secret with no host file of its own — the Keychain blob — is written to a `0600` file in a `0700` host temp directory that is removed when the import returns. Only paths ever appear as arguments. The `node.auth.imported` event carries artifact *names* and skip *reasons*; a test asserts the events log does not contain the generated key material the fixture imported.

### Clone

`NodeClone` inherits both fields from its source. The clone copies the source's disk, so the imported credentials are already inside the image the child boots from; re-importing on the child's first start would overwrite credentials the source had rotated with the host's. Inheriting the completed marker is the only answer consistent with the once-only rule.

### Positive Consequences

* A newly created node can push and run its agent immediately, which is the state users assumed it was already in.
* The refresh-token hazard is structurally impossible rather than merely unlikely: the code path that would clobber a rotated chain does not exist.
* Every skip is recorded by name in a node event, so "why can't this node push" is answerable from `node logs` without guessing.
* The opt-out is expressible at three altitudes and frozen at the one moment it means something.

### Negative Consequences

* Credentials now exist in more places. A node's disk image contains a git key and agent tokens in *two* homes, and `node clone` multiplies that.
* A node imported before dual-home placement shipped has only the login user's copy, so its managed terminal still prompts for a login. There is deliberately no re-import or migration path — that is exactly the overwrite the once-only rule forbids, and it cannot distinguish "never had a root copy" from "root copy has since rotated". The repair is a manual copy inside the guest, or recreating the node.
* The Keychain service name is an undocumented dependency on another product's internals. When it changes, the failure is silent-by-design (a skip), which is the safe direction but not the discoverable one.
* The accept-new fallback is weaker than pinning, and a user with a hashed `known_hosts` gets it without being told why beyond a warning line and an event.
* A guest-side transfer failure fails the start. That is deliberate — it is the same posture as workspace seeding, and a silent half-import is worse — but it means a convenience feature can now fail a `node start` that would otherwise have succeeded.
* Existing nodes get nothing. The fields decode `false`, so a node created before this change never imports, even on a first start that happens afterwards.

## Pros and Cons of the Options

### Import on every start

Re-copy the host's credentials each time the node boots, keeping guest and host in sync.

* Good, because a node that sat stopped for a month comes back with credentials that work.
* Good, because there is no state to get wrong: no marker, no frozen field.
* Bad, because it is precisely the operation that invalidates a rotated refresh chain. A working node breaks at an arbitrary later boot.
* Bad, because "in sync" is not a coherent goal for a token that both sides mutate independently.

### Import once, at creation

Seed the guest at first bring-up and never again.

* Good, because it matches what the credentials actually are: a starting point that each holder then owns.
* Good, because it reuses the seeding pattern already in the codebase (`WorkspaceSeeded`), including its behavior across failed and retried starts.
* Good, because the decision is frozen where every other node decision is frozen.
* Bad, because a node whose imported credentials expire has no supported repair path short of authenticating inside the guest.

### Mount or forward instead of copying

Forward `SSH_AUTH_SOCK` into the guest; bind-mount `~/.codex`.

* Good, because no secret is ever at rest in the guest, and revocation on the host is immediate everywhere.
* Good, because there is exactly one refresh chain, so the rotation hazard disappears.
* Bad, because a shared mounted `auth.json` gives several nodes concurrent write access to one token file, which is the rotation hazard in a worse form.
* Bad, because it only works while the host process is alive and the mount is up: an agent left running overnight loses its credentials when the TUI closes.
* Bad, because agent forwarding into a VM the user does not fully control is a broader grant than a single scoped key.

### Do nothing

Document `limactl copy` and let users run it.

* Good, because it adds no code and no secret handling.
* Bad, because every user rediscovers the same failure and writes the same script, with none of the ownership and mode discipline.
* Bad, because the manual recipe lands files as the wrong user, which fails in a way (`ssh` silently ignoring a key) that is genuinely hard to diagnose.

## Links

* Relates to [ADR 16](support_per_node_workspace_modes_16.md) — the workspace seeding pattern this reuses
* Relates to [ADR 69](use_official_codex_standalone_installer_69.md)
