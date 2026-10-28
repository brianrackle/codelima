# Preserve isolated paths when granting process-local KVM access

## Context and Problem Statement

The Linux development session can see `/dev/kvm` but lacks its supplementary
group. The existing helper grants that group through sudo and setpriv without
changing system permissions. Sudo reset the caller's isolated CodeLima and
Lima homes, so a command could unexpectedly use the default metadata home.

## Decision Drivers

* Preserve isolated command configuration and the original user identity.
* Keep device permissions and persistent group membership unchanged.
* Support sudo-rs, which requires an explicit environment-name list and still
  removes `TMPDIR` from the privileged environment.

## Considered Options

* Preserve exported names and restore scratch paths after dropping privileges.
* Require every caller to repeat its environment as command arguments.
* Modify device permissions or persistent user group membership.

## Decision Outcome

Chosen option: pass exported variable names to sudo's explicit preserve list,
then restore HOME, PATH, and an existing TMPDIR after setpriv drops root.
The command inherits process-local KVM access and runs as the original UID.
Sudo continues to enforce its own environment exclusions before that point.

### Positive Consequences

* Isolated CodeLima and Lima homes survive the group-access helper.
* Environment values are not copied wholesale into command-line arguments.
* `make test-qemu-guest` tests real device access, UID, and command paths.

### Negative Consequences

* The privileged path requires passwordless sudo and environment preservation.
* KVM initialization still does not prove that a nested guest reaches SSH.

## Pros and Cons of the Options

Preserving names keeps ordinary invocation consistent while retaining sudo's
environment policy. Repeating variables at every call site is easy to forget.
Changing device permissions or persistent membership would outlive the test
and would not repair environment loss at the helper boundary.

## Links

* [Local build and KVM checks](../BUILD.md#local-build)
