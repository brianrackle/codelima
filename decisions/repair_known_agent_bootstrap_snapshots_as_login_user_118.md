# Repair known agent bootstrap snapshots as the Lima login user

Status: Accepted

## Context and Problem Statement

CodeLima's root guest-command boundary allowed `command -v codex` to validate a
root-owned `/usr/local/bin/codex` symlink whose target lived below `/root`.
That check succeeded during bootstrap even though Lima's ordinary login user
received `Permission denied` when launching Codex. Earlier nodes also retained
frozen built-in Codex and Claude Code installer commands after the environment
definitions moved to user-owned npm in ADR 116.

## Decision Drivers

* Bootstrap must prove that the ordinary Lima login user can execute each
  installed coding agent.
* Nodes with untouched shipped installer snapshots must recover without being
  deleted and recreated.
* Customized environment, profile, and node bootstrap definitions must remain
  authoritative.
* Migration must be deterministic, observable, and safe to retry.

## Considered Options

* Keep every frozen node snapshot unchanged and require node recreation.
* Rewrite every node that references a built-in environment or profile.
* Migrate only exact known built-in definitions and validate executables after
  dropping back to the Lima login user.

## Decision Outcome

Chosen option: "migrate only exact known built-in definitions and validate
executables after dropping back to the Lima login user", because it repairs the
shipped failure without interpreting a slug as permission to overwrite custom
commands.

Seed revision 6 updates untouched built-in environment records and agent
profiles from their exact prior definitions. Codex and Claude Code validation
resolves the login user from `SUDO_USER`, restores that user's home and
`~/.local/bin` path, and executes the agent's `--version` command under that
identity.

`NodeStart` also scans a frozen bootstrap command list for contiguous sequences
that exactly equal a known former built-in definition. It replaces only those
sequences with the current user-owned npm definition, updates a known former
built-in profile validator, clears completion only when installer commands
changed, persists the repaired snapshot, records `node.bootstrap.migrated`, and
runs bootstrap normally. Unrecognized commands and customized profiles are not
changed.

### Positive Consequences

* Validation catches executable targets that root can resolve but the login
  user cannot traverse or execute.
* Untouched legacy nodes repair themselves on their next start.
* Current nodes and environment definitions upgrade through one exact-match
  migration rule.
* Customized bootstrap state retains the snapshot-at-creation guarantee.

### Negative Consequences

* A repaired node reruns the replacement built-in installation once even if an
  older agent installation partly succeeded.
* Known historical definitions remain in source as explicit migration inputs.
* Frozen node state now has a narrow exception for defective, exact shipped
  built-in definitions.

## Pros and Cons of the Options

### Keep every frozen node snapshot unchanged

* Good, because snapshot immutability stays absolute.
* Bad, because users must delete otherwise valid VMs and recreate their work.
* Bad, because root-side lookup continues to report a false success.

### Rewrite every node referencing a built-in slug

* Good, because migration logic is simple.
* Bad, because built-in slugs can contain user-customized commands.
* Bad, because a metadata label is insufficient evidence that replacement is
  safe.

### Exact-match migration plus login-user execution validation

* Good, because only source-controlled historical definitions are replaced.
* Good, because validation exercises the same user boundary as an interactive
  terminal.
* Good, because the migration is idempotent and covered by regression tests.
* Bad, because every new historical definition requires an explicit migration
  entry when it is defective.

## Links

* Refines [ADR 116](install_built_in_coding_agents_through_user_owned_npm_116.md).
* Preserves the reusable-configuration boundary from
  [ADR 6](seed_default_environment_configs_6.md).
* OpenAI guidance: [Codex CLI installation](https://developers.openai.com/codex/cli/).
* Anthropic guidance: [Claude Code setup](https://docs.anthropic.com/en/docs/claude-code/getting-started).
