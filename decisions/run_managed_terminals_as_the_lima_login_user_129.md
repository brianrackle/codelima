# Run managed terminals as the Lima login user

## Context and Problem Statement

Every guest command CodeLima issued — the bootstrap steps, the validation probe, the credential placement, and the shell the user actually typed into — was wrapped in `sudo -H --` by `LimaClient.Shell`. A managed terminal therefore opened as root, with `HOME=/root`, in a VM whose native identity is Lima's unprivileged login user.

Nobody chose that for terminals. It is an artifact. CodeLima's Microsandbox era ran guest commands as root because Microsandbox's guest had no other user; when the runtime was replaced with Lima, the port preserved the observable behavior by prefixing the new transport with sudo, and the comment in `lima.go` said so in as many words: "preserve CodeLima's shell and bootstrap contract." Each later decision then had to work around the terminal being root rather than question it. ADR 116 moved agent installs into the login user's npm prefix and published `/usr/local/bin` symlinks so a root shell could still find them. ADR 118 kept installing for the login user. ADR 128 discovered that a login-user-only credential import "satisfied the validator and then failed the user," because the user's own terminal was root, and answered by installing every credential into *both* homes. Three decisions in a row bent around a contract nobody was defending.

The driver that forced the question is concrete: Claude Code refuses `--dangerously-skip-permissions` when it is running as root. Yolo mode — the reason to put an agent inside a disposable VM in the first place — simply does not work in a CodeLima node.

## Decision Drivers

* Claude Code refuses `--dangerously-skip-permissions` as root, so root terminals break the workflow nodes exist to enable.
* The login user is Lima's native identity: it is who SSH logs in as, who the workspace mount maps host files to, who the agents are installed for, and who the imported credentials belong to.
* A terminal should behave the way a user's mental model says it does — like `ssh` into a machine — so `sudo` is available but not assumed.
* The privileges that provisioning genuinely needs (apt, NodeSource, `/usr/local/bin` links, `/proc/sys/vm/drop_caches`, `install` into `/root`) are real and must not be lost.
* Whatever replaces the blanket wrap must be decidable by reading one call site, not by inspecting command text at runtime.

## Considered Options

* Keep the blanket root wrap and special-case interactive launches inside the runtime.
* Sniff the command at the runtime boundary and elevate only what looks like provisioning.
* Make the identity an explicit argument at the `Shell` seam, chosen by the calling surface.
* Drop root everywhere and rewrite bootstrap to use `sudo` inline.

## Decision Outcome

Chosen option: **make the identity an explicit argument at the `Shell` seam**. `SandboxClient.Shell` takes a `guestIdentity` — `guestLoginUser` or `guestRootUser` — and `LimaClient.Shell` applies the `sudo -H --` prefix only for the latter. The wrap moves from unconditional to service-surface-only; it still exists in exactly one place.

### The split is by surface, not by command

Which identity a command needs is a property of *who issued it*, not of what it spells. `npm install` is provisioning when CodeLima runs it from a frozen bootstrap list and is an ordinary user command when the user types it in their terminal; no inspection of the string can tell those apart. So the argument is supplied by the calling surface and the runtime never guesses.

**User surface — the login user.** `Service.Shell` is the only one. It backs the `codelima shell` verb and, through `TerminalLaunchSpec` re-entering the binary as `codelima shell <nodeID>`, every managed terminal the TUI and daemon spawn. It runs as the login user whether it is interactive or carries an explicit command: `codelima shell node -- cmd` is still something the user typed, and a user who wants root types `sudo`, which Lima leaves passwordless.

**Service surface — root.** These are the commands CodeLima issues on the user's behalf:

| Caller | Why root |
| --- | --- |
| `runGuestCommand` (frozen bootstrap and agent-install steps, `runBootstrapGuestCommand`) | apt and NodeSource installs, and the `ln -sfn … /usr/local/bin/<agent>` link. The built-in definitions drop to the login user themselves for the parts that must be user-owned, reading the name out of `SUDO_USER` — which is exactly what this wrap supplies. |
| `runGuestCommand` (agent validation probe) | Same definitions, same `SUDO_USER` dependency. |
| `seedGuestWorkspace` prepare | Removes a prior tree and creates the seed target's parent anywhere on the guest filesystem, then hands that parent to the login user. |
| `copyHostAuthToGuest` staging + placement | Creates the `0700` staging directory and chowns it to the login user; installs into `/root` as well as the login user's home. |
| `reclaimMountedNodeFilesystemCaches` | Writes `/proc/sys/vm/drop_caches`, which no unprivileged user may open. |

Guest telemetry is not on this list: it reads `/proc/stat` and `/proc/meminfo` over the SSH forwarding peer, has always run as the login user, and never touches the `Shell` seam at all.

### Workspace ownership was already right

The obvious hazard is a copy-mode workspace the login user cannot write. It does not exist, and the reason is worth recording because it looks like it should.

`seedGuestWorkspace` has two halves with two identities. The prepare command is root, and its last act is `chown "$owner:$group" {{target_parent}}` where `owner` is `${SUDO_USER:-$(id -un)}` — the login user. The tree itself is then written by `CopyToGuest`, which resolves to `limactl copy` and is a *host-side* runtime command: it never passes through `Shell`, so it has never been able to acquire root, and it travels over Lima's SSH login, which means every seeded file lands owned by the login user. The prepare command's chown exists precisely so that copy can create its target. Mounted mode needs no argument at all — the Lima mount maps the host user's files to the guest login user, which is why the login user was already the right identity for a mounted workspace.

The residue is real but narrow: a *custom* root bootstrap command that writes into the workspace (`npm install`, a build) leaves root-owned files inside a login-user-owned tree. Seeding is once-only, so this is repaired rather than prevented. We document `sudo chown -R "$(id -un):$(id -gn)" <workspace>`, run from the node's own terminal, instead of adding a recursive chown to `NodeStart`. A start-time repair would walk the entire workspace on every start to fix a state CodeLima's own seed never produces, and it would silently reclaim files a user deliberately created with `sudo` — a destructive default in service of a case the user authored.

### Existing nodes need no migration

Nothing has to be re-provisioned. The agents were already installed into the login user's npm prefix (ADR 116), the frozen bootstrap snapshots already resolve the login user out of `SUDO_USER` and still run as root, the imported credentials were already written into the login user's home as well as root's (ADR 128), and both workspace modes were already login-user-owned. The next terminal a user opens on an existing node simply arrives as a different user with everything it needs already in place.

The one visible change is `PATH` resolution order, and it is benign. A root terminal found the agents through the `/usr/local/bin` symlinks, because `/root/.profile` does not add a user bin directory. A login shell as the login user reads Ubuntu's `~/.profile`, which *prepends* `$HOME/.local/bin` when that directory exists — and it does, because the npm-prefix bootstrap command creates it. So `~/.local/bin/claude` now wins, and the `/usr/local/bin` link that ADR 116 added for profile-independent resolution still points at the same binary and still covers non-login shells. Both paths resolve to one file; no change was needed.

### The dual-home credential import stays

ADR 128's two-home install is kept exactly as it landed. Its *justification* narrows — root's home now serves explicit `sudo` sessions and CodeLima's own provisioning commands rather than the user's default terminal — but the code and its tests are untouched by this change. Collapsing to a single home is the obvious follow-on simplification and is deliberately deferred: it is a separate decision about whether a `sudo claude` should find credentials, and bundling it into a privilege change would put two independent reversals in one commit.

### Positive Consequences

* `claude --dangerously-skip-permissions` works in a managed terminal, which is the workflow nodes exist for.
* A terminal behaves like `ssh` into the machine: unprivileged by default, `sudo` on request.
* Files a user creates in their terminal are owned by the user who owns the workspace, so host and guest tooling agree about the tree.
* Agents read the same home in a terminal that they were installed and validated in, which removes a class of "authenticated for the validator, not for you" bugs.
* The privilege decision is visible at every call site and greppable as one type.

### Negative Consequences

* A user with a muscle-memory root habit now needs `sudo` for guest package installs. Lima leaves it passwordless, so this is a keystroke, not a wall.
* A custom root bootstrap command that writes into the workspace leaves root-owned files there, repaired by a documented `chown` rather than automatically.
* `SandboxClient.Shell` grew a parameter, and every implementation and fake had to be updated.
* The dual-home credential import now carries a home whose justification is thinner than it was, until the deferred simplification is taken.

## Pros and Cons of the Options

### Keep the blanket root wrap and special-case interactive launches inside the runtime

* Good, because it is the smallest diff.
* Bad, because `interactive` describes the transport, not the caller: `codelima shell node -- cmd` is non-interactive and still user-typed, so it would keep getting root.
* Bad, because it puts a policy decision inside the runtime boundary, where the caller's intent is no longer visible.

### Sniff the command at the runtime boundary and elevate only what looks like provisioning

* Good, because no call site changes.
* Bad, because the same command text is provisioning or user work depending only on who issued it, so the classifier is guessing by construction.
* Bad, because it fails silently and asymmetrically — an unrecognized bootstrap command loses privileges it needs, and a user command that matches a pattern quietly gains them.

### Make the identity an explicit argument at the `Shell` seam

* Good, because the caller states its surface and the runtime obeys, so the split is decidable by reading one call site.
* Good, because the `sudo -H --` prefix stays in exactly one place, as PATTERNS requires.
* Good, because a new guest command cannot acquire an identity by accident — it has to name one.
* Bad, because the interface signature grows, and every implementation and test fake changes with it.

### Drop root everywhere and rewrite bootstrap to use `sudo` inline

* Good, because it removes the privileged surface entirely.
* Bad, because it invalidates every frozen bootstrap snapshot on every existing node: those commands resolve the login user from `SUDO_USER`, which only exists because of the wrap.
* Bad, because it scatters privilege changes through bootstrap and workspace code, which is precisely what the single-boundary pattern forbids.

## Links

* Amends [ADR 128](import_host_git_and_agent_credentials_once_at_node_creation_128.md) — the dual-home credential import, whose root pass is kept with a narrower justification
* Relates to [ADR 116](install_built_in_coding_agents_through_user_owned_npm_116.md) — agents installed into the login user's npm prefix, with `/usr/local/bin` links
* Relates to [ADR 118](repair_known_agent_bootstrap_snapshots_as_login_user_118.md) — frozen bootstrap definitions that drop to the login user
* Relates to [ADR 16](support_per_node_workspace_modes_16.md) — the copy/mounted workspace modes whose ownership this depends on
