# Installation and terminal repair qualification

The goal is a usable local KVM environment, a macOS package without the
reproduced XProtect false positive, and continued terminal emulation support.
These repairs are implemented and exercised on real Linux KVM and macOS VZ
guests. Release qualification remains partial.

The tested source started at `b4e55a5206780b57911f1f0228ab64335915b783`
with the working-tree changes in this repair. The native package was a local
1.0.1 candidate. It was not published or installed over the user's regular
CodeLima keg. Its XProtect evidence is in [the scan record](xprotect-results.json).
The [decision trail](decisions.tsv) records the run. Its earlier temporary log
pointers resolve through [the archival map](evidence-map.json).
Package-mirror URLs in the archived logs are redacted. Line endings and trailing
whitespace are normalized for storage in Git.

## Results

| Check | Result |
| --- | --- |
| Linux unit, race, CLI integration, package, native Lima template, lint and formatting | Passed |
| Native macOS unit, race, CLI integration, package, native Lima template, lint and formatting | Passed, with `GO_TEST_PARALLEL=1` for the full suite |
| Default and remapped terminal-tab reorder, repeat/release, stable active tab, guest-key forwarding | Behavioral regressions passed |
| Actual KVM access, original UID, isolated CodeLima/Lima homes and TMPDIR through sudo-rs | Passed |
| Linux and macOS native ABI manifests | Passed, 159 types each |
| Native upstream Debug compilation | Passed |
| Full upstream native VT unit run | 6353 passed, 47 skipped, two glyph-feature fixture failures; tracked in TODO #41 |
| Native Mac complete active XProtect 5368 rules, yara-python 4.5.4 | Old v1.0.0 CLI matches FRIENDLYFERRET_SECD; both candidate executables have zero matches |
| Native candidate signatures and CLI launch | Ad hoc signatures verify; CLI launches and reports 1.0.1 |
| Real Linux KVM and native Mac VZ runtime checks | Passed for the detailed subsets below |
| Disposable keg-only Homebrew install and brew test | Passed with exact packaged executable hashes and installed-pair terminal continuity |
| Gatekeeper assessment and quarantined download launch | Rejected ad hoc signing; quarantined probe did not finish within 60 seconds |
| Developer ID, notarization, public regular release, clean-host install and real-name upgrade | Not qualified |

The first native suite exposed two existing tests with a two-second completion
budget for background lifecycle writes. Their immediate navigation and focus
assertions remain unchanged. The bounded completion wait is now ten seconds,
and the focused tests pass on macOS. Go's Darwin Sync implementation uses
F_FULLFSYNC; the record does not claim a product latency change.

The port-row expression was introduced with the Overview renderer in
[63ef0c2](https://github.com/brianrackle/codelima/commit/63ef0c2), on October 25.
It is absent from v0.3.5. Older versions already had the terminal renderer,
daemon, and CLI. The same-rule old/new scan establishes the binary-content
collision. Historical Apple-rule installation timing was not established.

The ABI verifier initially failed because jsonschema was absent on the Mac.
`make init-ghostty-verifier` now provisions jsonschema 4.25.1 in a platform-local
venv under `.tooling`, under the existing installer lock. Both native schema
checks pass without global Python packages or Nix.

## Runtime evidence

[Linux runtime qualification](linux-runtime.md) records schema, frozen values,
directory/clone relationships, mounted/copy KVM boot, shell identity, writable
workspaces, forwarding, styled cell data, input editing, live handoff, renderer
containment, diagnostics, tab ordering, and cleanup.

[Native macOS qualification](macos-runtime.md) records VZ boot, host/guest cell
attributes, Unicode, OSC links, overline, resize geometry, live updates with an
attached frontend, renderer replacement, settings preservation, diagnostics,
VirtioFS ticks, local Homebrew installation, and cleanup.

The Linux boot hang is resolved locally with the complete debugging override
`-machine virt,accel=kvm,its=off`. Disabling ACPI alone did not help. This
establishes a working local configuration, without identifying the underlying
kernel, QEMU, or outer-hypervisor interrupt defect. BUILD.md records the exact
setup. No permanent device permissions, groups, or ordinary runtime defaults
changed.

The macOS package was built on 26.6.2. Its linker warned about a native object
built for 26.6.2 while linking for 26.0. These runs do not establish compatibility
with older macOS versions; the native release matrix remains required.

## Cleanup and side effects

All owned Linux and Mac VMs, daemons, terminals, renderers, SSH masters,
frontends, mounts, loop devices, disposable taps, formulae, and test downloads
were removed. The Homebrew formula/version baseline matches the original.
The coordinator subsequently removed the local candidate archive/binaries,
temporary YARA environment, and the failed Mac test stores after archiving
their evidence. The source development toolchain and application binaries
remain available for ordinary development.

Before the KVM helper fix, one Linux probe lost its isolated home variables
through sudo and ran doctor repair against the default Linux home. It reported
zero quarantined records and zero recovered nodes, plus seeded/stale metadata
repairs. No default VM or daemon was created. There was no before-image to
reconstruct the exact repaired bytes. The helper now has a native environment
and UID regression test.

Homebrew autoremove removed the pre-existing docker-completion 29.9.2 during
disposable-keg cleanup. That exact version and its prior autoremove eligibility
were restored, and the complete formula/version list matches the baseline.
Reinstallation changed receipt timestamps. QA.md now explicitly disables
autoremove before package verification.

The user-provided `.env` remains local and ignored. Credential values were
checked against the generated evidence files and were absent.

## Remaining qualification

The slice reports mark each QA flow independently. Outstanding physical-terminal,
two-window, broad manager, multi-node forwarding, built-in agent, full-resource
VM, clean-host trust, regular upgrade, beta migration, and public-release checks
remain in TODO.md. No current Gatekeeper, signing, notarization, or complete
manual QA claim follows from passing the XProtect scan.

## Review decisions

Fix Root Causes separated KVM access from successful guest boot and removed
the benign string collision at its source. Attack the Premise caught sudo-rs
ignoring whole-environment preservation and separately removing TMPDIR.
Model the Domain kept reorder commands in the existing contextual action
registry. Laziness Protocol retained the renderer, session store, and archive
API. Test Behavior, Not Implementation drove real key-handler and archive
rejection tests. Prove It Works drove the old-release positive control, real
native guests, cell attributes, exact hashes, and live shell PID checks.
Sequence Work into Verifiable Units drove separate runtime/package cleanup
and package-baseline comparisons. Independent review found no proven code
correctness issue and retained the qualification and side-effect limits above.
