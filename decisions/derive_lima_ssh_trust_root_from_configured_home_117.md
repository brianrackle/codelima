# Derive the Lima SSH trust root from configured LIMA_HOME

## Context and Problem Statement

Dynamic forwarding reads each running instance's Lima-generated `ssh.config`
and validates that config plus its identity file beneath `LIMA_HOME`.
`limactl list --json` supplies the instance SSH config path, but Lima 2.1 does
not supply a `LimaHome` JSON field. Requiring both fields prevented every
forwarding peer from connecting even though CodeLima already resolved the same
`LIMA_HOME` used by `limactl`.

## Decision Drivers

* Keep the SSH config and identity constrained to the active Lima home.
* Use Lima's supported machine-readable SSH config path.
* Avoid an extra `limactl` process per node or per forwarding poll.
* Preserve custom `LIMA_HOME` and symlink resolution behavior.

## Considered Options

* Combine the listed SSH config path with CodeLima's resolved `LIMA_HOME`.
* Infer `LIMA_HOME` by taking the parent of the listed instance directory.
* Run another formatted `limactl list` or `show-ssh` command for every peer.
* Stop validating SSH config and identity containment under `LIMA_HOME`.

## Decision Outcome

Chosen option: "combine the listed SSH config path with CodeLima's resolved
`LIMA_HOME`", because those values come from Lima's machine output and the same
configured environment respectively, while retaining the existing ownership,
permission, containment, and loopback checks.

`ForwardingSSHConfig` still requires a running observation with a non-empty
`SSHConfigFile`. It resolves `LIMA_HOME` through `LimaClient.resolvedLimaHome`
and passes that path as the trust root to `parseLimaSSHConfig`. It does not
require a `LimaHome` field in `limactl list --json`.

### Positive Consequences

* Dynamic forwarding works with Lima 2.1's actual JSON output.
* Custom and symlinked Lima homes retain one shared resolution path.
* A forged or misplaced SSH config still fails closed outside `LIMA_HOME`.
* Forwarding peer creation adds no subprocesses.

### Negative Consequences

* Changing `LIMA_HOME` independently of the running daemon makes existing
  listed config paths fail containment until the daemon is restarted with the
  matching environment.

## Pros and Cons of the Options

### Combine listed SSH config path with resolved LIMA_HOME

* Good, because each value comes from its authoritative owner.
* Good, because existing path and file-security validation remains unchanged.
* Good, because no additional runtime process is needed.
* Bad, because the daemon environment and the observed Lima instance must
  refer to the same home.

### Infer LIMA_HOME from the instance directory

* Good, because the instance directory is present in list output.
* Bad, because a child path should not define its own security trust root.
* Bad, because it bypasses CodeLima's configured-home resolution.

### Run another Lima command for every peer

* Good, because formatted output could request only selected values.
* Bad, because peer retries would spawn recurring runtime processes.
* Bad, because CodeLima already has the required values.

### Remove containment validation

* Good, because any readable SSH config could be used.
* Bad, because untrusted list output could redirect identity-file reads outside
  Lima-owned state.

## Links

* Refines [Return to Lima as the sole runtime with schema v4](return_to_lima_as_the_sole_runtime_92.md).
* Preserves [Route dynamically discovered node HTTP ports through node.localhost](daemon_dynamic_node_hostname_forwarding_70.md).
* Upstream contract: [Lima SSH documentation](https://lima-vm.io/docs/usage/ssh/).
