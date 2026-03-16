# Repair Guest `stty` For Interactive Shells

## Context and Problem Statement

Interactive node shells were leaving the session in a broken terminal state after scripts used the common `stty raw -echo` and `stty "$(stty -g)"` pattern. Investigation showed that the guest image exposed `/bin/stty` and `/usr/bin/stty` as `uutils coreutils`, and that implementation did not round-trip its own `-g` output correctly on CodeLima's interactive node shells.

## Decision Drivers

* Interactive shells should remain compatible with common shell installers and prompts that rely on `stty -g` round-trips.
* The fix should work for existing nodes without forcing users to recreate them.
* Normal one-off non-interactive shell commands should stay unchanged.

## Considered Options

* Leave the guest `stty` implementation unchanged and document the limitation.
* Repair `/bin/stty` and `/usr/bin/stty` to GNU `stty` before starting interactive shells.
* Replace the interactive shell transport to avoid the guest `stty` behavior entirely.

## Decision Outcome

Chosen option: "Repair `/bin/stty` and `/usr/bin/stty` to GNU `stty` before starting interactive shells", because it fixes the actual failing tool in the guest, applies to existing nodes on the next shell open, and preserves the current shell transport design.

### Positive Consequences

* Interactive shells recover compatibility with installers and tools that use `stty raw -echo` followed by `stty "$(stty -g)"`.
* Existing nodes heal themselves automatically when `codelima shell <node>` starts an interactive shell.
* Non-interactive command execution is unaffected.

### Negative Consequences

* Interactive shell startup now performs a guest-side repair step before launching the login shell.
* The repair relies on passwordless `sudo` being available when the guest exposes the broken `uutils` symlink.

## Pros and Cons of the Options

### Leave the guest `stty` implementation unchanged and document the limitation

Accept the broken `uutils` `stty` behavior and tell users to avoid tools that depend on it.

* Good, because it avoids modifying the guest filesystem.
* Good, because it keeps shell startup minimal.
* Bad, because common tools such as the Homebrew installer still break interactive shells.
* Bad, because users would keep hitting the same failure on existing nodes.

### Repair `/bin/stty` and `/usr/bin/stty` to GNU `stty` before starting interactive shells

Detect the broken `uutils` `stty`, and if GNU `stty` is present, repoint the standard paths before the login shell starts.

* Good, because it fixes the failing binary rather than masking terminal symptoms.
* Good, because it applies immediately to existing nodes.
* Good, because the change is limited to interactive shell startup.
* Bad, because it mutates guest system symlinks.
* Bad, because it depends on the guest allowing non-interactive `sudo`.

### Replace the interactive shell transport to avoid the guest `stty` behavior entirely

Use a different remote-shell path and try to avoid triggering the broken guest tty toolchain.

* Good, because it could isolate CodeLima from guest terminal quirks.
* Good, because it might reduce transport-specific complexity later.
* Bad, because it does not address the broken `/bin/stty` that guest-side tools invoke directly.
* Bad, because it is a much larger change than the actual defect requires.

## Links

* Related implementation: [service.go](/Users/brianrackle/Projects/codelima/internal/codelima/service.go)
