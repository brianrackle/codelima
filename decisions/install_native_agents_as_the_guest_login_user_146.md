# Install native coding agents as the guest login user

## Context and Problem Statement

CodeLima installs Codex and Claude Code through npm, requiring Node.js even when
only native agents are needed. Installing or running Claude as root interferes
with `--dangerously-skip-permissions`. Bootstrap itself enters a root shell for
system provisioning, so native installers must not inherit that identity.

## Decision Drivers

* Use supported native installers without root-owned agent state.
* Preserve customized environments and existing credentials.
* Fail bootstrap when download, installation, or executable validation fails.

## Considered Options

* Continue user-owned npm installations.
* Execute native installers directly in the root bootstrap shell.
* Drop to the guest login user before downloading and running native installers.

## Decision Outcome

Choose the third option. Install curl, certificates and Git at the system
boundary; resolve the Lima login account through `SUDO_USER` and passwd; reject
UID 0; then use `sudo -u` solely to drop privileges. Download each script to a
user-owned temporary file, check download success, execute it as that user, and
remove it on exit. Use Codex's noninteractive installer option. Publish the
existing `/usr/local/bin` compatibility links only after a user-local executable
exists. Continue validating each agent's `--version` as the login user.

Seed revision 8 recognizes the exact prior npm definitions as legacy, alongside
older built-ins. The existing node-start migration mechanism reruns repaired
bootstrap snapshots. Customized/deleted definitions remain authoritative. Leave
old npm files and credentials untouched; the visible commands use native
installations. This supersedes the npm installation choice while retaining the
login-user validation and repair behavior of ADR 118.

### Positive Consequences

* Native updates and agent execution operate on user-owned state.
* Fresh environments no longer require Node.js/npm to install agents.
* Partial downloads and root-only bootstraps fail before running vendor code.

### Negative Consequences

* Native installers and their download services must be available at bootstrap.
* Old npm package files may remain on migrated nodes, consuming disk space.
* Root still owns compatibility symlinks; their targets belong to the guest user.

## Pros and Cons of the Options

### User-owned npm

Supports existing setups but couples agent installation to Node.js and npm.

### Root native installation

Simple at the bootstrap boundary but puts agent state in the wrong home and
conflicts with normal unprivileged execution. Rejected.

### Guest-user native installation

Matches the requested ownership and native update behavior. Requires explicit
privilege dropping and account validation because system bootstrap is root.

## Links

* https://learn.chatgpt.com/docs/codex/cli
* https://code.claude.com/docs/en/quickstart
