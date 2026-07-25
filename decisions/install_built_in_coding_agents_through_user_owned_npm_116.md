# Install built-in coding agents through user-owned npm

Status: Accepted

## Context and Problem Statement

CodeLima runs noninteractive Lima commands through `sudo -H` to preserve its
root guest-bootstrap contract. The built-in Claude Code environment piped
Anthropic's home-directory installer through that boundary, so the installer
detected sudo and refused to run. Codex used a separate root-owned standalone
installation even though both vendors support npm packages.

## Decision Drivers

* Fresh default nodes must install both Codex and Claude Code successfully.
* Either built-in environment must remain independently usable.
* Anthropic's npm package requires Node.js 22 or newer.
* Agent package state and future npm updates should be owned by Lima's login
  user, not root.
* Agent commands must resolve in ordinary guest shells without depending on
  shell-profile ordering.
* Customized, deleted, and already-frozen node bootstrap state must remain
  user-controlled.

## Considered Options

* Keep both native installers and add installer-specific privilege handling.
* Install both supported npm packages into the Lima user's prefix.
* Install both npm packages globally as root.

## Decision Outcome

Chosen option: "install both supported npm packages into the Lima user's
prefix", because it fixes the sudo-sensitive Claude installer while giving the
two built-ins one maintainable installation pattern.

Each environment independently ensures the system has Node.js 22, resolves the
login user from `SUDO_USER`, configures that user's npm prefix as `~/.local`,
and runs its global npm install after dropping privileges. A root-owned
`/usr/local/bin` symlink points to the user-owned executable, and a final
`command -v` check prevents bootstrap from completing without the selected
agent.

Seed revision 5 migrates environment records that exactly match older built-in
Codex or Claude Code installers. Customized and deleted environment records
are not changed. Nodes keep the bootstrap list frozen when they were created,
so an already-created node with the failed installer must be recreated rather
than silently changing its launch contract.

### Positive Consequences

* Claude Code no longer invokes its sudo-rejecting native installer.
* Codex and Claude Code use one documented npm-prefix and command-publication
  pattern.
* npm package files and later user-initiated updates are owned by the guest
  login user.
* Both public commands are available through the ordinary system path and are
  explicitly validated.
* Untouched environment definitions upgrade automatically on the next
  seed-and-repair pass.

### Negative Consequences

* Agent bootstrap now installs and maintains a shared Node.js runtime.
* Node.js 22 comes from the NodeSource apt setup path when the Ubuntu image does
  not already provide a compatible Node and npm.
* Selecting both environments repeats an idempotent prerequisite and npm-prefix
  check.
* Already-created nodes retain their old frozen installer commands and need
  recreation.

## Pros and Cons of the Options

### Keep both native installers and add installer-specific privilege handling

Run each native installer under the user and separately publish its executable.

* Good, because each vendor's preferred native path remains available.
* Bad, because installer flags, update behavior, and filesystem layouts differ.
* Bad, because the root bootstrap boundary must special-case every installer.

### Install both supported npm packages into the Lima user's prefix

Use `@openai/codex` and `@anthropic-ai/claude-code` with a shared user-owned
global npm prefix.

* Good, because both vendors document these packages.
* Good, because one ownership and PATH pattern covers both agents.
* Good, because future npm upgrades do not need root.
* Bad, because the guest must include a compatible Node.js runtime.

### Install both npm packages globally as root

Use the system npm prefix from the existing root bootstrap boundary.

* Good, because the commands naturally land on the system path.
* Good, because the bootstrap commands are shorter.
* Bad, because Anthropic warns against sudo npm installs.
* Bad, because package updates and state remain root-owned.

## Links

* Supersedes [ADR 69](use_official_codex_standalone_installer_69.md)
* Revisits [ADR 41](install_codex_npm_package_into_user_prefix_41.md)
* OpenAI guidance: [Codex CLI installation](https://developers.openai.com/codex/cli/)
* Anthropic guidance: [Claude Code advanced setup](https://code.claude.com/docs/en/setup)
