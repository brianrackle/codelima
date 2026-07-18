# Use the official Codex standalone installer for built-in environments

Status: Accepted

## Context and Problem Statement

The built-in `codex` environment installed Node, npm, and `@openai/codex` into `$HOME/.local`. A selected environment could therefore depend on shell-profile PATH behavior, and CodeLima did not verify that `codex` was actually resolvable before marking bootstrap complete. OpenAI now documents its standalone installer as the primary macOS/Linux installation path.

## Decision Drivers

* Selecting the built-in `codex` environment must produce a `codex` command in ordinary node shells.
* Bootstrap completion must prove the selected executable exists.
* The default microsandbox guest contract uses root on an apt-based image.
* Customized or deleted built-in environment configs must remain user-controlled.

## Considered Options

* Keep the npm user-prefix installer and add more shell-profile handling.
* Use OpenAI's standalone installer in the default user prefix.
* Use OpenAI's standalone installer with `/usr/local/bin` as the visible command directory.

## Decision Outcome

Chosen option: "use the official standalone installer with `/usr/local/bin`." The built-in installs only its apt prerequisites, pipes the official installer to a non-interactive shell with `CODEX_INSTALL_DIR=/usr/local/bin`, and finishes with `command -v codex`. A missing executable fails node start and leaves bootstrap incomplete.

Untouched historical snap/npm and apt/npm built-in configs upgrade in place during the normal seeding pass. Customized and deleted configs are not changed. Bootstrap state remains frozen per node, so nodes created with an older command list are repaired explicitly or recreated rather than silently mutating their launch contract.

### Positive Consequences

* `codex` is on the normal system PATH without relying on login-profile ordering.
* The installer matches current official Codex guidance and no longer requires Node/npm solely for Codex.
* Bootstrap cannot report success when the selected command is absent.
* Existing user customization semantics remain intact.

### Negative Consequences

* The default root-based guest contract owns the `/usr/local/bin/codex` launcher.
* Existing nodes retain their frozen historical bootstrap and need explicit repair or recreation.
* The installer downloads the current Codex release unless an operator customizes the environment config.

## Links

* Supersedes [ADR 41](install_codex_npm_package_into_user_prefix_41.md)
* Refines [ADR 6](seed_default_environment_configs_6.md)
* Official guidance: [Codex CLI installation](https://developers.openai.com/codex/cli/)
