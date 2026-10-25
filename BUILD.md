# Build And Release

## Fleet-manager frontend checks

`make test-tui-manager` runs the identity/filter/sort/mark projection, responsive
layout, observed byte values, compact capacity, hidden-mark/global allocation
projections, global/detail navigation, palette, key lifecycle, shell payload and
resize regressions. `make test-tui-input` remains available for a selected
`TUI_INPUT_TEST_FILTER`. Run `make verify` before packaging the major-version
redesign, and run every applicable flow in QA.md. Focused checks do not qualify
the incomplete runtime contracts in TODO #57 or the full 26-artboard design.

`make test-tui-palette TUI_PALETTE_TEST_FLAGS=-race` checks grouped matching,
rendered spans, paste/repeat ownership, resize/mouse targets, recent fallback,
captured node confirmations and directory Start/Retry with real service state
and a fake sandbox (ADR 193). It includes shared help, fleet, restart and daemon
control regressions. Run the command-palette subsection of QA.md Flow 7 with
the rebuilt application at both widths; focused checks do not replace full gates.

`make test-tui-help TUI_HELP_TEST_FLAGS=-race` checks contextual group order,
four/two/one-column resizing, search, pasted and inherited key events, mouse
focus/scroll/close, and retained System action descriptions (ADR 192). Run the
key-help subsection of QA.md Flow 7 in wide and compact terminals. The existing
palette tests remain in this target because help no longer shares its action
model; palette behavior must remain intact.

`make test-overview OVERVIEW_TEST_FLAGS=-race` covers Overview focus and
responsive panels, stable session/route selection, captured end confirmation,
daemon-derived session age, coalesced cross-client tab-order refresh, mouse
links and stale/unknown observations
(ADR 191). It includes the shared fleet/manager and bounded recent-evidence
regressions. Ordinary/session/renderer/handoff protocols remain 27/2/6/7.
Run the Overview subsection of QA.md Flow 7 after the complete gates.

`make test-empty-nodes` covers the first-run guide, actual defaults, scoped
creation, host blockers/recovery, read-only re-check, mouse actions and complete
scrollable errors (ADR 184). Use `EMPTY_NODES_TEST_FLAGS=-race` for race checking.
The live first-run checks are in QA.md; run them at both wide and compact sizes.

`make test-ports` covers readiness without suppressing guest probes, published
TCP remapping and half-close, cancellation, bind conflicts and recovery,
confirmed-stop cleanup, legacy ownership and pre-boot migration (ADR 182),
durable pins/discovery order, cached typed projections, process-label loss,
TCP Activity transitions, stable claims during handoff/rollback, compact/wide
Ports rendering and real-socket refresh
cancellation/lost pin replies (ADR 183, ordinary protocol 22).
Use `make test-ports PORTS_TEST_FLAGS=-race` for the focused race run.
For the shared fleet projection (ADR 186), run
`make test-ports PORTS_TEST_FILTER='^Test(TUIFleet|TUIPorts)' PORTS_TEST_FLAGS=-race`;
this checks shared loader navigation, stale-route guards and the fleet action.
Use `make gopls GOPLS_ARGS='check internal/codelima/forwarding_policy.go
internal/codelima/forwarding_ports.go internal/codelima/tui_ports.go'` while
developing. Run the Ports terminal and real-guest flows in QA.md; final
qualification remains tracked in TODO #57.

`make test-fleet-evidence` checks bounded, node/attempt-scoped evidence, recovered
interruption and live claims, independent recent history, response fencing,
cancellation, incomplete sources and explicit terminal activity (ADR 187).
It also checks selected CPU/memory history, runtime configuration, workspace
mount matching and preview response identity (ADR 188).
Per-agent validation tests (ADR 190) cover real service execution records,
failure/retry, pending suffixes, clones, legacy/edited records, configured
profile checks, inert labels, read-only projection and responsive presentation.
Use `FLEET_EVIDENCE_TEST_FLAGS=-race` for race checking. The current ordinary
daemon protocol is **27**; update older daemons explicitly. Session protocol 2
is unchanged; renderer protocol 6 and handoff protocol 7 include retained prompt
idle metadata. The importer still accepts handoff 6 and preserves its Activity
queue and baselines. Run the fleet evidence/session flows in
QA.md after the full verification gates.

`make test-prompt-idle` checks accepted raw/encoded/paste input, protocol-response
exclusion, stale/working/unknown states, clock rollback, retained publication,
handoff compatibility and fleet idle presentation (ADR 189). Use
`PROMPT_IDLE_TEST_FLAGS=-race` for race checking. Live QA must compare two
frontends and a live daemon update with the same integrated terminal.

`make test-host-capacity` checks native capacity parsing, read-only volume
sampling, global allocation across directory scopes, stale-response rejection,
responsive System panels and settings navigation. It also cross-compiles the
sampler for macOS arm64 and amd64; native macOS readings require manual QA.

`make test-metrics` covers bounded history, incremental reads, telemetry parsing,
process CPU deltas/PID reuse, shared probe caching and cancellation, protocol
frame size, navigation fencing, pause/sort/copy controls and responsive plots.
Metrics and inventory trends were
introduced in protocols 9 and 10 (ADRs 155–156).
The focused suite also checks CPU trend retention/epoch reset, stale refreshes
and both process rankings during pause. Live guest qualification
must compare procfs readings and exercise the new Metrics flow in QA.md.

`make test-node-logs` covers bounded local tails, structured severity, terminal
control removal, special-file rejection, private bootstrap output rotation,
failure preservation, search/paste ownership, follow, stale replies, wrapping,
mouse selection, clipboard and pager argument parsing (ADR 157). Run the
Logs procedure in QA.md in a terminal as well as these automated checks.

`make test-node-spec` covers frozen/current recipe drift, environment and
profile edits, deleted templates, bounded YAML inspection, special files,
responsive panels, navigation fencing and Spec actions (ADR 159). Run the
Spec terminal procedure in QA.md before qualifying this surface.

`make test-bootstrap-steps` covers frozen provenance, durable attempts,
failure/cancellation/interruption, explicit suffix retry, legacy metadata,
clone inheritance, JSON/YAML round trips, CLI/TUI retry and failed-VM stopping
(ADR 160). Run the real guest failure/retry procedure in QA.md as well.

`make test-node-restart` covers stop/start ownership, confirmed shutdown,
cancellation, preserved bootstrap, both output streams, durable phases and
caller-disconnect behavior, plus TUI single/bulk confirmation and detail scope
(ADR 185). Use `NODE_RESTART_TEST_FLAGS=-race` for the focused race run. The
restart contract was introduced in ordinary protocol 23. Run the real-guest
restart procedure in QA.md after the full verification gates.

`make test-node-operations` checks independent execution lifetime, per-node
conflicts, duplicate admission, cancel-and-stop, restart recovery, private
history, bounded pagination/output and the daemon update barrier (ADR 161).
It includes real socket disconnect/reconnect, Activity cancellation and
responsive rendering, filtering, copy and stable selection. CLI coverage adds
directory resolution, stderr progress, receipt inspection/cancellation and
protection of responsive daemons during startup recovery (ADR 162). Run the shared
operation procedure in QA.md for live TUI exit and actual VM cleanup as well.
Lifecycle coverage includes node-scoped latest-start inspection, compact/wide
Starting and Failed views, cancellation through a real daemon socket, late
response fences and queued attachment readiness/navigation gates (ADR 164).
In a constrained development VM, stop nested QEMU guests before full lint/race
gates. Focused iteration can use `GOMEMLIMIT=384MiB GOMAXPROCS=1 GOFLAGS=-p=1`
with the existing make targets. Keep QA socket-bearing Lima metadata on a
project-rooted Linux filesystem; macOS VirtioFS can retain socket entries that
the guest cannot later look up or unlink.
Stop regressions cover stale cached observations in both directions and reject
an unconfirmed shutdown. History ordering uses observed completion times.
Phase tests cover failed-command boundaries, skipped work, retry and later
validation, conservative restart recovery, storage failure before execution,
and both boot output streams without duplication (ADR 163).

`make test-activity` checks the private bounded journal, restart/idempotency,
retention, concurrent writes, mixed receipt/message pagination, degraded reads,
replacement admission, two independent socket clients, lost acknowledgements,
snapshot watermarks and footer expiration. It also checks Activity's aligned
history columns, request/status glyphs, full-row ANSI/truecolor selection,
compact date/range context, rendered mouse controls, typed row actions and
header running counts (ADR 172). Inspect both 150×38 and 80×24 live views.
Protocol 14 adds `activity.append`
and `activity.list`, and optional operation origin; keep the exact handshake
and update-only compatibility path. Verify two real TUIs and a daemon restart
using QA.md before qualifying the shared Activity surface.
Protocol 15 adds a bounded observed-progress summary to operation lists. The
same focused checks cover completed/skipped phase counts, legacy missing data,
compact two-line selection geometry, wide columns and observed durations.
The Activity suite also drives a failed handoff after a successor journal write
and checks cache invalidation, retry identity, sequence continuity and reopening
the resulting journal (ADR 167).
Protocol 16 adds committed native Activity events. The focused Activity checks
cover route transitions and identity, localhost owners, observed allocation,
unknown recovery, bounded retries/overflow, ownership fencing, shutdown and
frontend footer deduplication. The full daemon suite checks the Ready callback
against competing startup ownership. Follow QA.md for real guest forwarding
and capacity qualification before marking this surface complete.
The Activity suite also exercises committed live imports with retained sessions,
unauthorized commits and failed commit-acknowledgment writes (ADR 169). Successful
successors record the version transition and adopted count through the existing
queue; neither a failed import nor a restart fallback claims in-place success.
Protocol 17 and renderer protocol 5 add counted terminal notices and typed
Activity navigation/outcomes. `make test-activity` covers fresh/stale baselines,
adopted publishers, repeated notices, counter resets, closed producers, legacy
journals, exact session resolution and canceled navigation. The Ghostty adapter
suite checks native counters through checkpoint restore. Run the terminal-notice
QA flow with both packaged executables from the same build.
Handoff version 6 transfers the bounded pending native queue, omission count
and observation baselines. The Activity recipe exercises both real handoff
halves with blocked persistence, rollback before/after ownership release,
unobserved terminal output, unchanged route/capacity observations and malformed
state; its daemon transport checks cover the 8 MiB payload bound and chunking.

`make test-create-node` covers create-only defaults and create-and-start under
one receipt, caller disconnect through real sockets, persisted identity before
boot, slug/UUID admission conflicts, failure/cancellation and TUI readiness gates
(ADR 173). Daemon protocol 18 adds the optional startup intent to create input.
Renderer protocol 5 and handoff version 6 remain unchanged. Run the new
create-and-start QA flow with an isolated home and disposable guest.

The same target covers the create form and nested choices (ADR 174): async
validation fencing, symlink boundaries, compact rendering, launch controls,
admission identity and template/directory navigation. Run `make verify` and
`make test-race` after changes to its completion events. Its real guest QA must
use a Lima volume with sufficient image space and working Unix sockets.

`make test-template-editor` covers the shared template editor and multiline
command blocks (ADR 175), including preview/frozen-node parity, ordered and empty
selections, resource input, nested return, held-lock saves, Unicode paste and
read-only Lima catalog cancellation. Completion-event changes also require
`make verify` and `make test-race`; run the template editor flows in `QA.md`.

`make test-recipes` covers the global recipe catalog, shared Spec drift,
responsive list/detail navigation, environment drafts and external-editor
scratch cleanup (ADR 176). Run full verification and the recipe catalog flows
in `QA.md` when changing the metadata snapshot or completion events.

`make test-environment-runs` covers disposable environment execution, cancellation,
requester disconnect, retained output and cleanup recovery (ADR 177). Changes
require full verification and race tests, plus the real VM flow in `QA.md`.
Ordinary daemon protocol 19 adds test admission/receipts and Activity output
navigation. Session 2, handoff 6 and renderer 5 are unchanged. Inspect Go APIs
with `make go-doc GO_DOC_ARGS='context.WithCancelCause'` and symbols/diagnostics
with `make gopls GOPLS_ARGS='check internal/codelima/environment_test_run.go'`.

`make test-system-doctor` covers retained Doctor snapshots, asynchronous checks,
repair/re-check outcomes, current settings, header counts and details controls
(ADR 179). Use `make go-doc GO_DOC_ARGS='context.WithTimeout'` and
`make gopls GOPLS_ARGS='check internal/codelima/tui_doctor.go'` while developing.
Run full verify/race/integration and the live System Doctor flow in `QA.md`.

`make test-system-daemon` covers cached runtime summaries, unique subscribers,
real-socket deadlines, strict live update, guarded stop, asynchronous System
actions, bounded diagnostic evidence and the build-free capture wrapper
(ADRs 180–181). Ordinary protocol 21 adds the optional expected-epoch stop
guard; protocol 20 introduced overview/subscriber counts. Session 2, handoff 6
and renderer 5 are unchanged. Use `make gopls GOPLS_ARGS='check
internal/codelima/daemon_lifecycle.go internal/codelima/daemon_diagnostics.go
internal/codelima/tui_daemon_controls.go'`, then full verify/race/integration
and the live System overview/control flows in `QA.md`. Integration checks
include restart/respawn and installed-command diagnostic capture; macOS native
sampling still requires the native QA flow. The capture wrapper requires a
binary supporting `daemon diagnose` and never builds it during an incident.

`make test-dependency-hints` covers bounded shell analysis and the environment
warning's keyboard/mouse draft flow (ADR 178). Run full verify/race/integration
and `make test-terminal-attach` when changing its parser dependency. The pinned
syntax package is mvdan.cc/sh/v3 v3.12.0; its module requirements select
creack/pty v1.1.24. Go remains 1.24.1. Dependency edits use
`make go-get GO_GET_ARGS='module@version'` followed by `make tidy`, keeping the
project toolchain and module cache. The live dependency-hint flow is in `QA.md`.

`make test-vaxis-fork` exercises the vendored terminal renderer, including
fallback grapheme widths (ADR 158). `make test-vaxis-upstream` runs the fork's
required `mise run check`; it installs pinned mise and the upstream tool
versions below `.tooling`, keeping temporary builds under `tmp`. This is an
additional maintainer check when editing `third_party/vaxis`.

The exact reference sources are retained in `plans/tui-vnext/reference/` with
provenance and hashes in `SOURCE.md`; they are implementation references and
are not runtime assets or inputs to the native release package.

This document is for maintainers.
User-facing setup and usage stay in `README.md`.

## Local Build

For nested Linux arm64 VM qualification, `make init-qemu-guest` downloads and
extracts Debian/Ubuntu QEMU runtime packages into the project tool directory.
The installation records package versions and SHA-256 hashes, excludes
documentation links to absent system packages, and publishes only after both
QEMU executables pass version checks. `make test-qemu-guest` verifies real KVM
initialization and QMP shutdown. `make run-kvm KVM_ARGS='...'` keeps the current
UID and adds only process-local KVM group access using passwordless sudo when
needed. No system package, device permission or persistent group is changed.
All disposable Lima homes, images, instances and sockets still require cleanup.
Lima discovers the local executable via PATH and its firmware beside the
installation ([Lima configuration](https://lima-vm.io/docs/config/environment-variables/)).

CodeLima bootstraps its own local toolchain under `.tooling/<os>-<arch>`.
Use the make targets from the repository root:

```sh
make init
make build
make verify
make test-race
make test-integration
make diagnose-terminal-freeze
```

What each target does:

- `make init`
  - installs Go, `gopls`, `golangci-lint`, Zig, `pkgconf` (as `pkg-config`), and a locally patched upstream `libghostty-vt` build
  - downloads Go modules
- `make build`
  - builds `./bin/<os>-<arch>/codelima` and its `codelima-renderer-worker` companion
  - refreshes `./bin/codelima` as a compatibility symlink to the current platform's binary
- `make verify`
  - runs `fmt-check`, `lint`, `test`, `test-vaxis-fork`, and `build`
- `make test-race`
  - runs every Go package with the race detector serially by default
- `make test-tui-input`
  - runs decoded tab, focus, search, dialog and selector shortcut regressions,
    including followups across overlays and terminal payload preservation;
    use `GOFLAGS=-race` for the race detector or
    `TUI_INPUT_TEST_FILTER='^TestTUIShortcut'` for the application shortcut set
- `make test-integration`
  - builds the real CLI and exercises daemon lifecycle, isolated renderer spawning, stale recovery, PTY continuity across framed-stream live update, rollback after an injected import failure, delayed legacy-macOS restart fallback, and startup recovery while the previous daemon still owns its shutdown lock
  - uses the deliberately short `./tmp/i` root so derived Unix handoff socket paths remain within platform limits; override it with `INTEGRATION_TMP` only with an equally short path
- `make test-tui-resize`
  - exercises window growth/shrinkage, minimum-size recovery, pixel-only changes,
    overlays, daemon geometry propagation and idle PTY handoff after resizing;
    use `GOFLAGS=-race` to check races
- `make diagnose-terminal-freeze`
  - runs the repository `diagnose-codelima-terminal-freezes` skill's read-only capture script without rebuilding or restarting CodeLima
  - writes incident evidence under `./tmp/terminal-freeze-*`; pass `DIAG_ARGS='--home PATH --binary PATH --terminal-id ID'` to override discovery

Ordinary Go tests use `GO_TEST_PARALLEL=4`; race tests default to
`GO_RACE_TEST_PARALLEL=1`. Override these only after qualifying the host.

The Ghostty worker requires cgo and `pkg-config`. Make enables cgo and exports
`PKG_CONFIG` as the exact platform-local `.tooling/<os>-<arch>/bin/pkg-config`
path for cgo, native bridge checks, and benchmarks. This is upstream
`pkgconf` `2.5.1`, built from pinned, checksum-verified source using
managed Zig, not a script that special-cases Ghostty flags. No system
`pkg-config`, Homebrew package, or host C compiler is needed to provision it.
Make uses a host `cc` for cgo when present and otherwise uses managed Zig.
Zig and `pkgconf` are installed before Go-based development tools. The
CLI/daemon package graph contains no Ghostty cgo. Linux release packaging builds
the main executable with `CGO_ENABLED=0`; macOS enables cgo only for its existing
Virtualization.framework host-capability adapter (ADR 136). The renderer worker
uses cgo on every platform.

`make pkg-config` provisions just Go, Zig, and the metadata resolver;
`make test-pkgconf` adds its focused installer and resolver tests. Both
`make init` and `make ghostty-vt` share this prerequisite so a native-only
recipe works from a checkout without preinstalled Go or `pkg-config`.

Vaxis is pinned to `v0.17.1` with a local replacement in `third_party/vaxis`.
The complete upstream module, tests, and Apache-2.0 license are retained;
`UPSTREAM.md` records the verified module checksums, encoded-image API and
opt-in literal-tab input mode. `make test-vaxis-fork` runs its full module suite. Keep dependency
changes in this reviewed copy, never in the Go module cache. The added API
uploads already-encoded PNGs synchronously within a wire-byte budget, keeps
multipart Kitty transfers serialized, and exposes exact-pixel/z-order placement
without asynchronous resizing workers.

Vaxis resize events require an explicit `Vaxis.Resize` call on the UI event
loop before drawing or calculating pane geometry. Handle them before overlay
input routing so dialogs cannot leave the screen buffers at their old size
(ADR 140).

`make build` produces both `codelima` and the private
`codelima-renderer-worker` helper beside it. Release archives package both
executables. The helper is not a user-facing command: a daemon-owned terminal
starts it only through an inherited Unix socket descriptor, generation-fences
the link, and kills that process when a native operation exceeds its deadline.
Applied renderer mutations publish immutable screen/read bundles through a
capacity-one dirty edge at no more than 20 FPS; initialization and explicit
recovery snapshots remain immediate. Normal renderer calls do not write
start/completion info records, while failures and calls exceeding 250
milliseconds remain logged. Bulk output and ordered input share a bounded
mutation lane that backpressures only the owning PTY. Tracked health and
lifecycle calls use a separate control lane, and fire-and-forget mutations keep
unique event IDs without generating response frames.
Keeping the worker as a separate executable makes the native-code boundary
visible in the package graph and prevents the daemon from entering Ghostty
through a hidden mode of its own executable.

### Native Renderer Dependency

The audited dependency is Ghostty commit
`82232ecde55405559dec29c5466cb9e39938cb41`, compiled with Zig `0.16.0` as a
static archive. The selected features are formatter, selection, render-state,
input-encode, color, grid-introspection, snapshot, search, and kitty-graphics;
all other optional features are disabled. The default profile is
`ReleaseSmall`, target `native`, CPU `baseline`.

Four separately checksummed patches retain the characterized XTQMODKEYS
reply, expose clipboard acknowledgement requirements, and enforce the bounded
static-graphics policy/checkpoint guard and allocation-bounded snapshot decode.
Their digests and the native inputs
are reviewed in `internal/rendererbuild/profile.go`. The installer checks
these inputs before cache reuse, verifies the official Zig archive checksum,
and retains upstream Zig dependency hashes. It does not replace dependency
manifests with unverified local paths.

Builds are isolated under `.tooling/<os>-<arch>/ghostty-vt`. The exact upstream
checkout lives in `sources/<commit>`; `current/source` is the matching patched
build tree. A completed immutable install is published through the platform's
`current` link only after successful compilation. A kernel lock serializes
installers, and failed builds leave the previous current install available.
There is no cross-platform `.tooling/ghostty-vt/current` include-path alias.

The managed `pkg-config` resolves `libghostty-vt-static` through
`current/share/pkgconfig`, using `current/include` and
`current/lib/libghostty-vt.a`. The native fingerprint covers source, Zig,
features, patch digests, width policy, platform and compiler options. The same
fingerprint is embedded in the C header, static archive, CLI and worker, and
checked at native initialization and the worker handshake. Runtime loading,
library-path search, and `CODELIMA_GHOSTTY_VT_LIB` overrides are not supported.
The worker still uses the platform's normal system C libraries.

Use these qualification targets when changing the native boundary:

```sh
make test-installers
make test-renderer-boundary
make test-ghostty-vt-schema
make test-ghostty-vt-build
make test-ghostty-vt
make test-ghostty-bridge
make test-ghostty-adapter GHOSTTY_TEST_FILTER='Ghostty|CloneColors|ValidateColors'
make benchmark-ghostty-compression
make test-package
```

Upstream unit execution and compile-only unit validation use `Debug`, since
the upstream page-list tests require tracked-pin safety instrumentation.
They default to `GHOSTTY_VT_TEST_JOBS=1` and can require substantial memory;
`GHOSTTY_VT_UNIT_FILTER=clipboard` is a focused diagnostic, not a substitute for
the full suite. ABI schema and adapter checks use the selected production
profile. The compression benchmark runs fresh off/on processes and reports
current RSS on Linux, bounded-step latency and resumed-input timings; RSS is
reported as unavailable on other platforms.

Changing the source or a patch requires reviewing its profile constant and
rebasing the narrow patches before rebuilding. Compiler-profile overrides
(`GHOSTTY_VT_TARGET`, `GHOSTTY_VT_CPU`, `GHOSTTY_VT_OPTIMIZE`, or
`GHOSTTY_VT_FEATURES`) require rebuilding both executables with Make so their
embedded fingerprint follows the installed dependency. Qualify release
packages on each native target rather than reusing another platform's cache.

Handoff version 6 keeps terminal identities in the manifest and transfers raw
replay and renderer recovery envelopes through ordered 512 KiB chunks. JSON
base64 expansion keeps every encoded chunk below the 1 MiB frame limit. Raw
replay is capped at 1 MiB per terminal and 64 MiB per handoff. Recovery envelopes
are separately capped at 24 MiB per terminal and 64 MiB per handoff before
allocation; retained native checkpoints also share a 64 MiB daemon memory quota.
Native Activity transfers in a separate lane capped at 8 MiB, after recovery
and before descriptors. Validate its declared size before allocation and its
queue/baseline contents before terminal adoption. Version 5 imports remain
supported with renderer recovery but without pending Activity transfer.
An envelope binds terminal identity, native build identity, integrity, application
color policy, and ordered journal watermarks. Same-build recovery uses an eligible
checkpoint plus a complete tail; cross-build recovery keeps the bounded ordered
raw fallback. Native snapshots exclude image state, so live or in-progress images
prevent new checkpoints. A truncated raw fallback reports partial recovery.
The importer also accepts version-4 chunked replay and version-3 stream manifests
when the old daemon can encode them below its compiled limit. An already-oversized
version-3 sender requires closing high-history tabs or one terminal-restarting
stop/start.

Useful supporting targets:

```sh
make test
make test-race
make test-integration
make lint
make fmt
make smoke
make test-lima-native
make gopls GOPLS_ARGS="check internal/codelima/tui_test.go"
```

The source checkout intentionally namespaces development binaries by the same platform tag used for `.tooling`, such as `linux-aarch64` or `darwin-arm64`. This prevents a host build and a guest build in the same shared checkout from overwriting each other's executable. Use `make run` or `make tui` when possible; both invoke the platform-scoped binary directly.

Runtime-backed manual checks require Lima 2.1.0 or a compatible newer Lima
2.x release. Use VZ on macOS arm64 and QEMU/KVM on Linux amd64/arm64. Keep
`LIMA_HOME` short, private, and on a local filesystem that supports Unix
sockets. Release qualification must include a warm guest-image cache and a
clean cache so template resolution/download failures are visible. The gated
`make test-lima-native` recipe resolves the Ubuntu template and validates the
CodeLima-rendered YAML with the installed `limactl`.

The built-in `codex` and `claude-code` environments use the official native
installers. Noninteractive Lima commands still cross the root boundary in
`LimaClient.Shell` for system prerequisites. The shared installer wrapper resolves
`SUDO_USER`, rejects UID 0, and drops to that login user before downloading or
executing the vendor script. A failed download must never execute a partial
script; temporary scripts are removed on exit. Only the compatibility links in
`/usr/local/bin` are published by root after installation succeeds. Agent
execution and update state belong to the login user.

Run `make test-native-agents` for executable installer failure/identity checks
and exact legacy migration coverage. Run `make verify test-race test-integration
test-package` for release checks. Keep both environments independently usable.
Installer definition changes require a seed-revision bump plus exact legacy
specs: revision 9 also migrates the revision-8 native Codex definition to
install the system `bubblewrap` package and check `bwrap --version` before
agent installation. Older npm definitions migrate to native installers while
preserving customized and deleted records. `NodeStart` repairs exact legacy
bootstrap sequences and reruns installation. It does not remove old npm package
files or credentials (ADRs 146–147).

macOS release qualification must exercise nested virtualization on an Apple
silicon host where Virtualization.framework reports it supported and an
unsupported macOS case. Confirm `doctor`, newly rendered YAML, and `/dev/kvm`
inside both a new node and a pre-existing restarted node agree with the host
capability. Linux qualification confirms the rendered macOS-specific setting
remains false while the ordinary QEMU/KVM host checks still pass.

Dynamic forwarding uses the pinned `golang.org/x/crypto/ssh` module and a
persistent client per running node. Connection data comes only from Lima's
generated instance `ssh.config`; no hidden runtime helper or host OpenSSH
process is launched per node. The config path comes from Lima's machine output,
while its ownership and containment trust root comes from CodeLima's resolved
`LIMA_HOME`; do not require a `LimaHome` field in `limactl list --json`. The
daemon also owns one `limactl watch --json` observation process. Release
qualification must verify generic `localhost` and `127.0.0.1` claimant
selection, one-second bind retry and claimant transfer, the port-1455 Codex
login exception that transfers generic routing to the newest listener, and
`{node}.localhost` HTTP and Upgrade traffic on both native platforms, including
two nodes sharing one guest port and a guest-loopback-only service.

## Self-Hosted Development Metadata

The repository includes a sanitized reusable configuration example at `examples/self-host/configuration.yaml`. Configurations are directory-independent in schema v4. Import or reproduce its fields in a live configuration, then create a node with that configuration while the node directory points at the local checkout.

Review the bootstrap commands before use; they intentionally install development tools and may need distro-specific adjustments.

## Release Artifacts

Release packaging is native per platform.
The packaging script builds from the platform-scoped source binary, but the archive layout remains stable for end users.
Each packaged archive contains:

- `bin/codelima`
  - the compiled CLI/daemon executable, with no Ghostty dependency
- `bin/codelima-renderer-worker`
  - the private renderer executable, statically linked to the audited Ghostty archive

The adjacent `<asset>.json` manifest records version, target platform, asset
name, archive SHA-256, and `renderer_build_id`. It is not an entry inside the
tar archive. There is no wrapper script, `codelima-real`, or packaged Ghostty
shared library. Keep both executables together: the daemon resolves the
worker beside its own executable, not through `PATH`.

Artifact size varies by target, Ghostty features, and Go toolchain and must be
recorded during release qualification.

Build a release archive for the current platform:

```sh
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
```

That target uses:

- `scripts/package_release.sh`
- `cmd/codelima-release`
- `internal/release`

`make package` validates the selected native profile and rebuilds both
platform-scoped executables with the same `PACKAGE_VERSION` and native
fingerprint before archiving them. Stop or live-update any daemon running from
those paths first: the daemon protocol requires an exact binary version, so a
newly packaged CLI correctly rejects an older development daemon. Run
`make build` afterward to restore the normal development version.

Direct `scripts/package_release.sh` invocation still requires the verified
static renderer install. After checking its identity, the script provisions
the pinned Zig/`pkgconf` tools and exports the same absolute `PKG_CONFIG` path
as Make; it does not depend on a developer's shell finding `pkg-config`.
Neither build-time tool is included in the release archive.

`make test-package` performs a self-cleaning release smoke test under `./tmp`
without replacing development binaries. It verifies the two-entry archive,
checksum, executable modes, embedded build identities, CLI help/version, and
native worker initialization/output/read with an empty `PATH` and unavailable
legacy shared-library paths. Packaging is deliberately passed an invalid
inherited `PKG_CONFIG` to verify that direct packaging selects the managed
resolver itself. Run it on every release platform.

To verify an already downloaded native archive without rebuilding that archive,
place it and its single matching manifest in a directory and run
`make test-package-artifact DIST_DIR=./tmp/downloaded-native-release`. This uses
the same checksum, build-provenance, executable and real renderer smoke checks.
Remove downloaded verification artifacts afterward.

## Homebrew Formula Generation

The Homebrew formula is generated from the release manifests rather than maintained by hand.

Render the formula locally:

```sh
make package-formula \
  PACKAGE_VERSION=1.2.3 \
  RELEASE_TAG=v1.2.3 \
  RELEASE_REPO=brianrackle/codelima \
  DIST_DIR=./tmp/dist \
  FORMULA_OUTPUT=./tmp/dist/Formula/codelima.rb
```

The generated formula:

- installs `git` and Lima as runtime dependencies
- installs both executables together in `libexec/bin`
- links `bin/codelima` to the installed CLI; no library cache or launcher is required

Tags must use `vMAJOR.MINOR.PATCH` and generate `Codelima` in `codelima.rb`.
The formula generator rejects prerelease tags and tag/manifest version
mismatches. Without `FORMULA_OUTPUT`, Make writes `$(DIST_DIR)/codelima.rb`.

`make package-formula`, `make release-metadata`, and `make test-release` need
only the managed Go toolchain; they do not build the native renderer. Inspect
metadata with `make --silent release-metadata RELEASE_TAG=v0.3.0`.
The Go metadata parser is shared by formula generation and GitHub Actions.

## GitHub Actions

### CI

`.github/workflows/ci.yml` runs:

```sh
make verify
```

on Ubuntu and macOS for pushes to `main` and pull requests.

### Release

`.github/workflows/release.yml` runs on:

- pushed tags matching `v*`
- manual dispatch with a `tag` input

The release workflow does this:

1. Requires an existing `vMAJOR.MINOR.PATCH` tag and resolves its version.
   Manual dispatch checks out the requested tag.
2. Runs `make verify test-race test-integration test-package`, then builds
   release archives from that exact tag on:
   - `linux-amd64`
   - `linux-arm64`
   - `darwin-arm64`
3. Creates the GitHub release with all `.tar.gz` archives and `.json` manifests.
   Requires nonempty `.github/release-notes/<tag>.md` and prepends its contents
   to generated notes. The release is regular and explicitly becomes Latest.
   Existing releases are not overwritten; if only the tap update failed, rerun
   that failed job.
4. Generates `Formula/codelima.rb`.
5. Updates the Homebrew tap if configured, removing the retired
   `Formula/codelima-beta.rb` in the same commit. Repeating this update is a no-op.

## Homebrew Tap Automation

The tap repo is separate from the main source repo:

- source repo: `brianrackle/codelima`
- tap repo: `brianrackle/homebrew-codelima`

The release workflow expects these GitHub Actions settings on `brianrackle/codelima`:

- variable `HOMEBREW_TAP_REPO=brianrackle/homebrew-codelima`
- variable `HOMEBREW_TAP_BRANCH=main`
- secret `HOMEBREW_TAP_TOKEN`

`HOMEBREW_TAP_TOKEN` should be a GitHub token with push access to the tap repo only.
For a fine-grained PAT, grant:

- repository access: `brianrackle/homebrew-codelima`
- permission: `Contents: Read and write`

The token does not need write access to `brianrackle/codelima`.

## Releasing

Standard release flow:

1. Ensure `make verify` passes locally.
2. Ensure `make test-race` and `make test-integration` pass locally.
3. Complete every flow in `QA.md`, including native macOS VZ, Linux QEMU/KVM, Lima observation/forwarding, and interactive TUI checks.
4. Leave an attached TUI idle beyond the request timeout, then verify its next
   terminal action uses the existing connection without a `tui refresh failed`
   timeout. Generate sustained terminal output and verify rendering remains
   responsive, renderer snapshots stay within the 20 FPS ceiling, and the
   daemon log contains neither normal per-call renderer records nor repeated
   stale/fresh dirty pairs. Run `cmatrix -u 0` for at least 15 seconds and
   verify the renderer PID/generation stays ready, the prompt returns after
   `Ctrl+c`, and no `queue-full` connection closure or repeated daemon
   reconnection appears. Start it once more in a terminal with an adjacent tab,
   press `Option+w`, and verify the busy tab disappears immediately without
   freezing cursor or tab selection while daemon cleanup completes.
5. Verify both no-argument `daemon update` (which must select the invoking candidate binary) and `daemon update /explicit/candidate/path` while a long-running terminal command is active. For a protocol-changing release, start the old release first and verify the new candidate's update-only compatibility handshake preserves that terminal.
   Fill at least one renderer journal above 900 KiB before one update and
   verify handoff version 6 preserves its terminal ID, shell PID, final replay
   marker, and responsive daemon. Exercise both eligible same-build checkpoint
   recovery and cross-build bounded raw fallback; verify a truncated fallback is
   marked partial rather than claiming full terminal-state preservation.
6. Ensure the tap repo settings and token are configured.
7. Create and push the release tag:

```sh
git tag v1.2.3
git push origin v1.2.3
```

The release workflow then:

- publishes the native archives and manifests to the GitHub release
- updates `Formula/codelima.rb` in `brianrackle/homebrew-codelima`

End users upgrade with:

```sh
brew update
brew upgrade codelima
```

The maintainer authorized `v0.3.2` after the resize/input fixes and remaining
native/manual qualification gaps were reported. Its release notes retain
those limits; publication still requires the three-platform automated release
matrix. Local and published evidence belongs in
[the resize release report](plans/resize_release_qa.md), with outstanding
manual work in TODO #41/#44/#46.

The maintainer authorized `v0.3.3` after the tab status and bell fixes and the
remaining native/manual qualification gaps were reported. Daemon protocol 7
adds metadata to dirty events; upgrade verification must cover a protocol-6
daemon from `v0.3.2`, preserving live terminals through the candidate's
update-only compatibility handshake. The frontend must be reopened to use the
new labels and acknowledgement behavior. Release evidence belongs in
[the tab status release report](plans/tab_status_release_qa.md), with remaining
manual work in TODO #41/#44/#50.

The maintainer authorized `v0.3.4` after the compact tab defaults and remaining
native/manual QA gaps were reported. This frontend presentation change keeps
daemon protocol 7 and the renderer dependency unchanged. Reopen the TUI after
upgrading. Release evidence belongs in
[the compact tab release report](plans/compact_tab_release_qa.md), with remaining
manual work in TODO #41/#44/#51. Publication requires the normal three-platform
automated release matrix.

The maintainer authorized `v0.3.5` after reporting the clipboard issue fixed.
It also includes the authorized native agent installers and Codex bubblewrap
provisioning. Update the daemon and reopen attached TUIs after upgrading;
untouched built-in agent environments migrate on their next node start.
Daemon protocol 7, renderer protocol 3 and the native dependency are unchanged.
Evidence belongs in [the clipboard and native agents release report](plans/clipboard_native_agents_release_qa.md).
Remaining manual qualification is tracked in TODO #41/#44/#54/#55, and
publication requires the normal three-platform automated release matrix.

`v0.3.5` was published as regular Latest on 2026-09-11 from `948ccea` after
all three native release jobs passed. Linux arm64 required one retry after a
Go module download failure during setup. The public archives/manifests,
Homebrew formula and downloaded Linux arm64 renderer smoke test were verified;
the release report records the evidence and remaining manual qualification.

### Libghostty Promotion To The Regular Release

On 2026-09-08 the maintainer explicitly requested promoting the published
`v0.3.0-beta.3` to a regular release and removing the beta path. Version
`v0.3.0` retains that candidate's application code and native packaging fixes;
only release tooling and documentation change. Historical beta tags/releases
remain available as records. ADR 137 supersedes the separate channel policy.

The beta passed automated verification, race, integration and package tests on
macOS arm64 and Linux amd64/arm64. The native/manual and interactive checks in
TODO #41/#43/#44 remain unverified. The maintainer authorized publication with
these limitations and then explicitly requested this promotion. This exception
does not mark those checks complete or waive qualification for future releases.
The regular release notes carry these limitations forward.

After qualifying and committing the candidate on `feat/libghostty-vt-adoption`:

```sh
make --silent release-metadata RELEASE_TAG=v0.3.0
git tag -a v0.3.0 -m 'CodeLima 0.3.0: libghostty-vt adoption'
git push --atomic origin feat/libghostty-vt-adoption refs/tags/v0.3.0
```

Verify the tag resolves to the candidate commit, GitHub marks it regular and
Latest, and the standard tap formula's URLs/checksums match all three uploaded
manifests. Confirm the beta formula is absent. Run the Homebrew upgrade flow in
`QA.md` on native targets. Workflow artifacts and tap scratch checkouts stay
under `./tmp/release` in the disposable runner.

## Manual Release Dry Run

Before the first real release, do a local dry run:

```sh
make verify
make test-race
make test-integration
make test-package
make package PACKAGE_VERSION=0.0.0 DIST_DIR=./tmp/dist
make package-formula \
  PACKAGE_VERSION=0.0.0 \
  RELEASE_TAG=v0.0.0 \
  RELEASE_REPO=brianrackle/codelima \
  DIST_DIR=./tmp/dist \
  FORMULA_OUTPUT=./tmp/dist/Formula/codelima.rb
```

Check:

- the archive layout with `tar -tzf`
- the manifest JSON contents
- the rendered formula URLs and SHA-256 values

## Troubleshooting

### Every daemon-backed terminal freezes

Do not rebuild, stop, update, or send `SIGQUIT` before capturing the live
process. From the host that owns the daemon, run:

```sh
make diagnose-terminal-freeze
```

The target intentionally has no `build` or `init` prerequisite so incident
capture cannot replace a binary or contend with the affected daemon. Its output
includes independent control-plane probes, at most one read-only terminal actor
probe, daemon metadata and logs, process state, and a non-terminating macOS
`sample` stack capture when available. It uses `CODELIMA_HOME` when set and
otherwise defaults to `~/.codelima`. Interpret the bundle with
`.agents/skills/diagnose-codelima-terminal-freezes/references/interpretation.md`.

### Native build identity or header/archive mismatch

Run `make init` and then `make build` using the same selected compiler profile.
Do not copy headers or archives between installs, or mix a release worker with
a different CLI. If an audited patch changed, update its reviewed profile
digest only after reviewing the patch. Cache reuse deliberately rejects
unreviewed inputs; bypassing that check can invalidate checkpoint compatibility.

### `pkg-config` executable not found

Use `make build` or `make init` to provision the managed binary; installing a
Homebrew package is not necessary. Make and the release script select it
explicitly even when the host has no `pkg-config`. For ad-hoc `go` commands,
export `PKG_CONFIG` to `.tooling/<os>-<arch>/bin/pkg-config` using an absolute
path, and set `PKG_CONFIG_PATH` to the matching Ghostty install's
`current/share/pkgconfig`. Prefer the Make targets so these paths and the
embedded native build identity remain consistent.

### `make init` stalls while building Ghostty

The first build downloads the pinned upstream source and content-addressed Zig
dependencies; a cold cache therefore needs network access. Subsequent builds
reuse the verified caches in `.tooling/<os>-<arch>/cache`. Reduce
`GHOSTTY_VT_BUILD_JOBS` if the compiler exhausts host memory. The installer
passes `-Demit-xcframework=false` on all platforms: CodeLima consumes a static C
archive, not an xcframework or shared library. A failed build does not publish
its staging tree or replace the previous working install.

### Release publishes assets but does not update Homebrew

Check:

- `HOMEBREW_TAP_REPO` is set
- `HOMEBREW_TAP_BRANCH` matches the tap default branch
- `HOMEBREW_TAP_TOKEN` exists and can push to the tap repo

### Homebrew formula changes are not pushed

The workflow skips the tap commit when the generated `Formula/codelima.rb` is identical to the existing file.

Clipboard changes should run `make test-clipboard` and the regular release
checks. The TUI delegates host clipboard handling to the outer terminal through
OSC 52, even when a native desktop utility is available. Tests must not modify
the developer's clipboard. Bridge tests cover fragmented responses through the
existing 64 KiB limit. The socket regression test uses the TUI's separate request
and event connections and checks clipboard delivery before and after event
reconnect; daemon tests cover seat takeover and superseded subscriptions.
`make test-integration` also sends OSC 52 from a real shell through the built
renderer and daemon to the owning frontend's event connection, then reconnects
and repeats with distinct Unicode text.
Native interaction tests also check that releasing without a selection is a
quiet no-op and that word-selection copying still works.
Real macOS and SSH clipboard qualification remains in
QA.md; offline tests do not establish outer-terminal permission behavior.

### Raw terminal attachment (major-version work)

`make test-terminal-attach` runs the native ANSI export, VT boundary, lease,
input parser and real-PTY/socket regressions. `make verify` additionally checks
the production binaries, all Go packages, lint and formatting. Ship current
daemon protocol 22 and renderer protocol 5 together; attachment was introduced
in protocols 8/4. Checkpoint envelope 2 adds the
VT boundary state, and older envelopes use journal recovery. Native Ghostty
remains pinned to the existing revision. Rebuild both binaries with `make build`
before manual attachment checks. The new TUI and runtime scope remains tracked
in `plans/tui-vnext/README.md` and TODO #57.

`make test-manager-settings` covers comment-preserving edits, concurrent
writers, rejected values, new-node defaults, launcher arguments, long menu
navigation and TUI settings. Preferences remain in the schema-v4 settings file;
no metadata migration or new runtime dependency is required. External window
mode uses the configured terminal executable, and tmux mode uses an installed
tmux. Qualify both on the intended host before release.
