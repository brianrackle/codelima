# Own disposable environment runs and recover cleanup

## Context and Problem Statement

The Environments artboard's `t` action executes a saved command bundle on a
throwaway xsmall VM, streams output into Activity and deletes the VM afterward.
A frontend-owned command cannot guarantee cleanup after the user quits, loses
the connection or restarts the daemon. Using a real node or its host workspace
would violate the design's isolation requirement.

## Considered Options

* Create a temporary configuration and run a foreground frontend operation.
* Run the commands on the selected real node.
* Admit a frozen test plan into the daemon's durable operation manager.

## Decision Outcome

Use `operation.submit` with kind `test-environment` and an environment selector.
The daemon resolves and freezes the saved environment commands and current
xsmall image/resources before admitting side effects. It allocates the node
UUID and runtime name itself. Clients cannot supply a node or cleanup path.
The receipt reserves the environment, node UUID and slug until completion.

The disposable node has no host credential import, published ports or agent
installation/validation. Its workspace is an empty private directory within
its own node metadata, copied into the guest rather than mounted. This is the
only internal exception to public workspace validation's metadata-directory
exclusion. Normal creation and test creation share reserved-node runtime
creation and rollback. Test cleanup skips configuration hydration so deleting
or changing the source recipe cannot prevent teardown.

The receipt durably owns cleanup before creation begins. Command cancellation
does not cancel cleanup. Failed teardown retries with bounded contexts and
retains its error and intent; retention never expires pending cleanup. Daemon
shutdown leaves that intent for recovery. Recovery starts after process-lock
ownership (and after commit for a live importer), without requiring a frontend
request. It executes cleanup only, never the saved commands. The known command
outcome survives recovery; interrupted execution is reported as interrupted.
Successful cleanup and the final result are persisted together. If the process
dies before that write, idempotent cleanup completes the old pending receipt.
Recovery appends cleanup diagnostics to the retained output and preserves its
line offsets, including when the runtime remains unavailable after restart.

Cleanup checks the exact UUID, generated name, private workspace and instance
index ownership. An incomplete create is cleaned using its reserved instance
reference. A deleted node's old runtime name is never deleted again, since
another node may now own that name. VM and workspace are removed; node and
bootstrap evidence remain available for history and recipe last-run display.

The TUI submits asynchronously, retaining the same request ID after an
ambiguous acknowledgment. Completion cannot steal a different navigation or
overlay. Accepted tests open an Activity output viewer whose polling is bound
to that viewer instance. Output remains accessible after node deletion; closing
the viewer does not cancel execution. `K` requests cancellation through the
existing daemon operation API. Ordinary protocol 19 adds the test request,
receipt state and operation-output Activity action; session 2, handoff 6 and
renderer 5 are unchanged.

### Positive Consequences

* Leaving the TUI or losing a connection cannot orphan an admitted test.
* Real nodes, mounted workspaces and host authentication are unaffected.
* A failed or canceled test retains output and execution provenance.
* Recovery cannot rerun commands with unknown partial effects.

### Negative Consequences

* A test consumes a real VM's resources until teardown succeeds.
* Persistent runtime/metadata errors keep cleanup pending and block another
  test of that environment until the problem is repaired.
* Test metadata remains as historical evidence, while its VM and private
  workspace are removed.

## Validation

`make test-environment-runs` covers success, command failure, actual requester
socket closure, cancellation, frozen command provenance, crash recovery before
node publication and after VM deletion, repeated cleanup failure after restart,
retained output/line offsets, ownership conflicts, admission identity,
navigation fencing and compact output interaction. Full gates and actual VM
qualification are recorded in the design delivery ledger; unit fakes do not
establish native runtime qualification.

## Links

* [Environments reference](../plans/tui-vnext/reference/Environments.dc.html)
* [Recipe catalogs](inspect_recipes_from_global_metadata_and_edit_command_blocks_176.md)
