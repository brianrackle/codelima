# Install system bubblewrap with the Codex environment

## Context and Problem Statement

The native Codex environment does not explicitly install the system `bwrap`
command. Developers need bubblewrap available alongside Codex inside their Lima
guest, including when the node was already bootstrapped with native installers.

## Decision Drivers

* Install bubblewrap automatically for Codex environments.
* Preserve native, unprivileged agent installation.
* Upgrade known built-ins without overwriting custom provisioning.

## Considered Options

* Require developers to install bubblewrap manually.
* Install Ubuntu's bubblewrap package as a Codex system prerequisite.

## Decision Outcome

Choose the system package. The Codex prerequisite command installs `bubblewrap`
with apt alongside curl, certificates and Git, then checks `bwrap --version`.
Failure prevents successful bootstrap. The existing root system-provisioning
boundary handles package installation; native agent installers still run as the
non-root Lima login user. No privilege escalation is added to agent launches.

Seed revision 9 upgrades exact prior Codex definitions, including revision 8's
native installer sequence. Existing node-start migration reruns these repaired
bootstrap snapshots. Claude-only environments and customized definitions retain
their existing prerequisites.

### Positive Consequences

* Fresh and migrated Codex nodes provide the system `bwrap` command.
* Package or executable failures are reported during bootstrap.

### Negative Consequences

* Codex provisioning adds an Ubuntu package dependency.
* Checking the version confirms installation, not whether a particular host's
  kernel and security policy permit every requested namespace operation.

## Pros and Cons of the Options

### Manual installation

Avoids a dependency but requires repetitive setup and leaves existing nodes
inconsistent.

### System package

Uses the guest distribution's package and dependency management and integrates
with existing provisioning. Requires access to the guest's package repositories.

## Links

* [Native agent installation](install_native_agents_as_the_guest_login_user_146.md)
