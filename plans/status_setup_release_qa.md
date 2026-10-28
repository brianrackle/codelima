# Status and setup release qualification

The v1.0.2 candidate starts from v1.0.1 (`d0c2817`) and adds ADR 208's
independent VM Status and Setup presentation. The existing daemon, session,
renderer and handoff protocols and node metadata format are unchanged.

## Native lifecycle evidence

On October 28, 2026, a disposable macOS VZ node used one vCPU, 1 GiB of memory,
a 10 GiB disk and a mounted disposable workspace. Its CodeLima home, Lima home,
scratch directory and no-auth-import profile were isolated under the project.
The test profile validated the shell without installing coding agents.

Two frozen bootstrap commands wrote a first-step marker and then required a
host-created permission marker before writing the last-step marker. The
initial missing marker deliberately failed the second command.

| Observation | VM Status | Setup |
| --- | --- | --- |
| Initial bootstrap failure | running | failed |
| External `limactl stop` | stopped | failed |
| External `limactl start` | running | failed |
| Permission marker created, failed-step retry succeeds | running | ready |
| Normal CodeLima stop after successful setup | stopped | ready |

The successful first step retained one attempt. The failed second step reached
two attempts. Each marker was written once. The disposable VM and daemon were
removed, and the isolated Lima inventory was empty afterward. The user's
existing `codelima` and `work` VMs remained running.

## Presentation and automated evidence

Live PTY inspection covered 80×24, 100×30, 120×30 and 150×38. Status and Setup
were visible together. Combined filtering, Setup sorting, selected preview and
failure detail were exercised. The rebuilt CLI reported running/failed for the
existing codelima VM and running/ready for work without changing the failed
node's stored metadata checksum.

`make verify PACKAGE_VERSION=1.0.1`, `make test-race`, the version-matched CLI
integration suite, the focused status/lifecycle regressions and the native
package smoke test passed. The exact locally built v1.0.2 Darwin archive also
passed `make test-package-artifact`. The parallel full suite exposed a
two-second filesystem-completion
budget in an existing dropped-completion recovery test; five seconds passed
with all recovery assertions retained.

Both executable payloads in that archive had zero matches against the complete
active XProtect 5368 YARA rules, using yara-python 4.5.4. The rules SHA-256
matches the prior v1.0.1 qualification's rules. Exact executable/archive hashes
are in [the scan record](status_setup_xprotect_v1.0.2.json). This local archive
was built on the existing macOS host; the hosted three-platform release matrix
still verifies and packages the tagged source on its native runners.

## Scope and limits

This change does not alter CPU/memory chart bucketing or repair a failed setup
automatically. The chart-gap issue and a headless-PTY shutdown observation are
tracked in TODO.md. Existing broad physical-terminal, clean-host, signing and
notarization qualification limits from v1.0.1 remain in that file and the
installation/terminal QA record. This record does not claim those flows passed.
