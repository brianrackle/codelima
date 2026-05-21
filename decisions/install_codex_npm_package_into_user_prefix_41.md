# Install Codex npm package into a user-owned prefix

## Context and Problem Statement

The built-in `codex` environment config installed Node with `sudo snap install node --classic` and then installed Codex with `sudo npm install -g @openai/codex`. That made the `codex` binary root-owned in the VM's global npm prefix, so routine upgrades such as `npm update -g @openai/codex` required `sudo`.

## Decision Drivers

* Codex updates inside the VM should not require `sudo npm`.
* The `codex` executable should remain available from normal interactive VM shells.
* Existing customized or deleted built-in environment configs must not be overwritten.
* Untouched legacy built-ins should get the safer default automatically.

## Considered Options

* Keep installing Codex with `sudo npm install -g`.
* Install Codex into the VM user's npm prefix under `~/.local`.
* Replace the npm install with a different package manager or installer.

## Decision Outcome

Chosen option: "Install Codex into the VM user's npm prefix under `~/.local`", because it preserves the npm-based installer while making future `npm update -g @openai/codex` operations run as the VM user.

### Positive Consequences

* Fresh Codex nodes install the npm package without `sudo npm`.
* Future Codex npm updates can run as the VM user.
* Untouched legacy `codex` environment configs are migrated forward during store layout.
* Customized and deleted `codex` environment configs remain user-controlled.

### Negative Consequences

* Bootstrap now writes a PATH export to the VM user's shell profiles.
* The bootstrap still uses `sudo snap install node --classic` when Node is absent because installing the Node runtime remains a system-level operation.

## Pros and Cons of the Options

### Keep installing Codex with `sudo npm install -g`

Continue installing the Codex npm package into the VM's global npm prefix as root.

* Good, because it is the smallest command list.
* Good, because it keeps the executable in the system global npm bin path.
* Bad, because package updates keep requiring `sudo npm`.
* Bad, because root-owned npm package state is harder for users to maintain from normal VM shells.

### Install Codex into the VM user's npm prefix under `~/.local`

Configure npm's global prefix to `~/.local`, ensure `~/.local/bin` exists, add that bin path to shell startup files, and install Codex with a user-owned `npm install -g`.

* Good, because Codex and later updates are owned by the VM user.
* Good, because it keeps the documented npm package and `codex` binary.
* Good, because the profile update makes the binary discoverable in normal shell sessions.
* Bad, because the bootstrap command list is longer.

### Replace the npm install with a different package manager or installer

Use an alternate Codex installer instead of npm.

* Good, because it might avoid npm global prefix behavior.
* Bad, because CodeLima already documents and tests the `@openai/codex` npm package path.
* Bad, because switching installers would add new compatibility and support questions outside this problem.

## Links

* Refines [Seed Default Environment Configs During Store Bootstrap](seed_default_environment_configs_6.md)
