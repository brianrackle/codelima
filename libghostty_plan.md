# libghostty-vt adoption and surface-reduction plan

- Status: proposed
- Audit date: 2026-08-15
- Current CodeLima pin: [`ae52f97dcac558735cfa916ea3965f247e5c6e9e`](https://github.com/ghostty-org/ghostty/tree/ae52f97dcac558735cfa916ea3965f247e5c6e9e) (2026-05-25)
- Audited upstream revision: [`cecf81678e47f967b0354acada67e69d229f436b`](https://github.com/ghostty-org/ghostty/tree/cecf81678e47f967b0354acada67e69d229f436b) (2026-08-15)
- Upstream comparison: [`ae52f97d...cecf8167`](https://github.com/ghostty-org/ghostty/compare/ae52f97dcac558735cfa916ea3965f247e5c6e9e...cecf81678e47f967b0354acada67e69d229f436b)

## Decision summary

CodeLima should treat libghostty-vt as the owner of terminal semantics and keep
only the application-specific process, transport, policy, and Vaxis rendering
layers. The recommended end state is:

```text
Vaxis TUI
  └── CodeLima screen/input DTOs
        └── daemon: shell, PTY, backpressure, journal, policy
              └── framed renderer protocol
                    └── one renderer worker per terminal
                          ├── thin Go/C batching adapter
                          └── statically linked, exactly pinned libghostty-vt
```

The renderer-process boundary from ADR 108 remains. Go cannot preempt a hung
cgo call, and replacing a complete terminal process would kill the user's shell.
The daemon must therefore continue to own the shell and PTY while each worker
owns one Ghostty terminal. This audit does not support moving session lifecycle,
PTY flow control, renderer supervision, response deduplication, host security
policy, or Vaxis drawing into libghostty-vt.

The highest-value transfers are:

1. Replace CodeLima's text/ANSI extraction with `ghostty_formatter_*`.
2. Replace the OSC 52 scanner with `GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE`.
3. Make Ghostty's key, mouse, focus, and paste encoders mandatory and delete
   the local fallback encoders.
4. Remove the downstream hyperlink API patch and use
   `ghostty_grid_ref_hyperlink_uri`, which is already exported by the current
   pin.
5. Remove process-wide stderr redirection and use `GHOSTTY_SYS_OPT_LOG`.
6. Adopt upstream resize reflow, terminal colors, scrollback limits, and idle
   compression.
7. Use same-build Ghostty snapshots as renderer checkpoints so a replacement
   worker restores exact state and replays only a journal tail.
8. Move the cgo package into the renderer worker and statically link the exact
   Ghostty build, deleting the runtime symbol loader and ABI fallbacks.

By the end of Phase 3, CodeLima should have no local VT parser, OSC clipboard
parser, ANSI screen formatter, terminal input encoder, runtime Ghostty symbol
resolver, or downstream Ghostty patch. The remaining C adapter should perform
only callback adaptation, allocation/lifetime safety, and bulk conversion of a
Ghostty render row into CodeLima's screen DTO.

## Goals

- Reduce the amount of terminal-protocol code maintained and tested by
  CodeLima.
- Improve standards fidelity by using the same parser, formatter, input
  encoders, selection model, and effect model as Ghostty.
- Make the native dependency deterministic: one exact source revision, one
  exact feature set, and no silent runtime ABI fallback.
- Preserve terminal fault containment, shell/PTY continuity, bounded
  backpressure, and daemon responsiveness.
- Make renderer recovery exact for same-build worker replacement even after
  the current raw journal has evicted early output.
- Lower active rendering cost and idle memory without adding work to the PTY
  drain path.
- Create a supported path to host-theme fidelity, semantic prompt awareness,
  selection, and Kitty graphics.

## Non-goals

- Moving shell startup, PTY ownership, process groups, session persistence,
  daemon live update, or renderer supervision into Ghostty.
- Removing the per-terminal renderer process or calling Ghostty from daemon
  read RPCs.
- Treating snapshot format version 1 as a durable on-disk or cross-version
  persistence format.
- Letting a guest terminal directly decide host clipboard, hyperlink-opening,
  notification, image-file, or shared-memory policy.
- Replacing Vaxis as the outer TUI renderer.
- Calling the C API once per visible cell. A small bulk adapter is intentional
  until upstream exposes a bulk resolved-viewport representation suitable for
  cgo.
- Floating on Ghostty `main`. Every CodeLima build remains pinned and
  reproducible.

## Audit method and baseline

The audit compared the pinned and audited Ghostty revisions across the public
VT headers, C bindings, terminal build features, packaging instructions, and
the commits that introduced relevant behavior. That range contains 976 commits.
Across `include/ghostty/vt*`, the C binding, VT feature/build files, and
packaging files, it changes 50 files with 12,076 insertions and 1,155 deletions.

The local production integration baseline is 7,928 physical lines across the
following broad ownership boundary:

| Area | File | Lines |
|---|---|---:|
| Ghostty terminal plus direct PTY lifecycle | `internal/codelima/tui_terminal_ghostty_cgo.go` | 2,761 |
| Dynamic C compatibility/batching layer | `internal/codelima/ghostty_bridge_compat.c` | 1,262 |
| C bridge header | `internal/codelima/ghostty_bridge_compat.h` | 120 |
| Local key, mouse, and paste encoding | `internal/codelima/tui_terminal_input.go` | 285 |
| OSC 52 parsing and host clipboard adapter | `internal/codelima/tui_clipboard.go` | 135 |
| Worker/server/protocol/supervision/journal | six renderer and daemon files | 3,034 |
| Ghostty installer | `scripts/install_ghostty_vt.sh` | 123 |
| Downstream Ghostty patch | `scripts/patches/ghostty-vt-codelima.patch` | 208 |

This is a scope baseline, not a claim that all 7,928 lines are removable. The
daemon/session and renderer-containment portions are CodeLima responsibilities.
The measurable target is a net deletion of at least 1,500 production lines by
the end of Phase 3, excluding tests and generated files. Moving the legacy
in-process Ghostty session out of the main binary should make a reduction of
2,500 lines attainable, but that is a stretch target rather than a gate.

The upstream library itself is production-proven, but its public C API remains
explicitly unstable: the [Ghostty README](https://github.com/ghostty-org/ghostty/blob/cecf81678e47f967b0354acada67e69d229f436b/README.md#libghostty) says functionality is stable while signatures are still in flux, and the [umbrella VT header](https://github.com/ghostty-org/ghostty/blob/cecf81678e47f967b0354acada67e69d229f436b/include/ghostty/vt.h) warns that breaking changes are expected. This makes exact pinning and compile-time linkage more important, not less.

## Upstream findings

### Capabilities already present at CodeLima's current pin

Several local implementations can be removed before, or independently from,
the larger rebase:

| Local responsibility | Existing upstream API | Proposed change |
|---|---|---|
| Visible/recent plain-text and ANSI reads, including manual SGR construction | [`ghostty_formatter_*`](https://github.com/ghostty-org/ghostty/blob/cecf81678e47f967b0354acada67e69d229f436b/include/ghostty/vt/formatter.h) with plain or VT output, selections, trim, and unwrap | Build history/viewport selections and return formatter output. Delete manual scrollback cells, grapheme assembly, and SGR formatting. |
| Paste bracketing and sanitization | [`ghostty_paste_encode`](https://github.com/ghostty-org/ghostty/blob/cecf81678e47f967b0354acada67e69d229f436b/include/ghostty/vt/paste.h) | Pass one semantic paste payload and the terminal's bracketed-paste mode to Ghostty. Delete local delimiter and unsafe-control handling. |
| Native diagnostic capture | [`GHOSTTY_SYS_OPT_LOG`](https://github.com/ghostty-org/ghostty/blob/cecf81678e47f967b0354acada67e69d229f436b/include/ghostty/vt/sys.h) | Route the callback into structured renderer diagnostics, or leave it unset in normal release operation. Delete `dup`/pipe/stderr capture and its process-global mutex. |
| Hyperlink lookup | [`ghostty_grid_ref_hyperlink_uri`](https://github.com/ghostty-org/ghostty/blob/cecf81678e47f967b0354acada67e69d229f436b/include/ghostty/vt/grid_ref.h) | Resolve a viewport/history grid reference and query the URI. Delete both custom exported functions from the downstream patch. |
| Key, mouse, and focus byte sequences | [`input_encode` APIs](https://github.com/ghostty-org/ghostty/blob/cecf81678e47f967b0354acada67e69d229f436b/src/terminal/build_options.zig) | Link the required APIs and delete `has_*_api` branches and the handwritten fallback maps. Keep only Vaxis-to-Ghostty event translation. |

The custom hyperlink exports duplicate an API that is both declared and
exported at the current pin. They should be the first patch hunk removed.

### Capabilities added or materially improved since the current pin

| Upstream improvement | CodeLima opportunity |
|---|---|
| [Preserve shell prompts on resize by default](https://github.com/ghostty-org/ghostty/commit/90175950d5004382abd3b0b9528e7be81b0b52ec) | After differential PTY tests, remove CodeLima's width-growth heuristic and supplemental `SIGWINCH`; supersede/refine ADR 95. |
| [Clipboard effects for OSC 52](https://github.com/ghostty-org/ghostty/commit/d4ac93a0395d321b043ee0116dc8a1a384f0fb83) | Receive normalized clipboard destinations, MIME type, and decoded content from Ghostty. Keep host authorization and write policy in CodeLima. This also covers fragmented OSC input without a second parser. |
| [Complete terminal snapshot C API](https://github.com/ghostty-org/ghostty/commit/d7bb4b8639614c7d6eeac403bf92d64066b2c73f) | Checkpoint screen, scrollback, cursor, modes, styles, and unfinished parser state in a worker. Restore a same-build replacement from a checkpoint plus journal tail. |
| [VT ground and continuation APIs](https://github.com/ghostty-org/ghostty/commit/a69a591af11370453d707aa9c0b2ec6ff17ce3c3) | Make checkpointing correct across partial UTF-8, CSI, OSC, DCS, and APC input; use bounded continuation tracking or wait for ground. |
| [2.7x–11x shorter render-state lock hold](https://github.com/ghostty-org/ghostty/commit/446f80f4edd16d217e8ec928664d86a529b3a223) and [faster render updates/C reads](https://github.com/ghostty-org/ghostty/commit/e3056658d05bdd54db9fd37a02c196bfe81cfe39) | Rebase the screen path onto `begin_update`/`end_update`, iterator `next`, `get_multi`, and UTF-8 grapheme reads. Retain one bulk C call per frame rather than crossing cgo per cell. |
| [Caller-driven scrollback compression](https://github.com/ghostty-org/ghostty/commit/172f15da3b904633f798d99cb6e43acd78cbdc79) | Incrementally compress inactive scrollback inside the renderer worker and measure RSS for 1, 10, and the supported maximum terminal count. |
| [Semantic prompt state](https://github.com/ghostty-org/ghostty/commit/bdb566068ead29409eb5d410249f02f51d274d3d) | Add `cursor_at_prompt` to terminal status/snapshots and future wait primitives without screen-text heuristics. Treat absent shell markers as “unknown,” not “not at prompt.” |
| [Color-scheme report encoder](https://github.com/ghostty-org/ghostty/commit/f00e906949bbe46904ff7a13eeff9e8d4a292d09) plus foreground/background/cursor/palette options | Delete CodeLima's report-string formatting and pass outer-terminal defaults into Ghostty. The upstream dependency in TODO #1 is now available; host color discovery and live theme updates remain CodeLima work. |
| [`-Dvt-features`](https://github.com/ghostty-org/ghostty/commit/1fdbb8c912231bdbe039614a70f10772a3e50d23) | Compile only the APIs CodeLima uses. Start without glyph or Kitty graphics handling; enable Kitty only with its renderer transport. |
| Expanded terminal effects for title, working directory, notifications, progress, unknown sequences, and processing errors | Let Ghostty parse and normalize sequences. CodeLima may update tab metadata and badges, while retaining UX and security policy. |
| Selection gestures and selection formatting | Let Ghostty own word/line/output selection and soft-wrap-aware copy when native embedded selection is enabled. Preserve host selection bypass and mouse-capture rules. |
| Kitty graphics image and placement APIs | Let Ghostty own APC parsing, transfer assembly, storage, and placement math. CodeLima transports safe decoded assets/placement deltas and draws them with Vaxis. |

### Downstream patch disposition

The existing 208-line patch does not apply to the audited revision. Its three
concerns must be separated instead of rebasing the patch wholesale:

1. **Hyperlink exports:** delete them. The supported grid-reference API already
   provides this behavior.
2. **XTQMODKEYS query (`CSI ? 4 m`):** submit the parser action, response, and
   tests upstream. The audited parser still treats this form as an unknown CSI
   `m` intermediate. If a CodeLima characterization test proves it is required
   before upstream accepts it, carry only this minimal hunk with an owner,
   upstream issue/PR link, expiry condition, and `TODO.md` entry. It may not be
   folded back into a general compatibility patch.
3. **Icon-title push/pop (`CSI 22;1t` and `CSI 23;1t`):** default to upstream
   behavior and remove CodeLima's icon-title-to-window-title translation. The
   audited upstream tests intentionally ignore explicit icon-title operations.
   Retain this only if a user-visible regression fixture demonstrates a need,
   then seek an upstream option or fix.

The upgrade may not ship with a silently failing patch application or an
untracked local source edit.

## Target ownership contract

| Concern | Target owner | Boundary contract |
|---|---|---|
| VT parsing, modes, screen state, scrollback, graphemes, styles, hyperlinks | libghostty-vt | The worker sends raw PTY bytes in order and reads immutable render/read outputs. |
| Key, mouse, focus, and paste encoding | libghostty-vt | CodeLima maps Vaxis events to typed Ghostty events; Ghostty returns PTY bytes. |
| Clipboard/title/PWD/bell/notification/progress parsing | libghostty-vt | Ghostty emits typed effects; CodeLima applies host policy and updates product state. |
| Plain/VT/HTML formatting and selection math | libghostty-vt | CodeLima supplies a source range and requested format. |
| Terminal snapshot codec and scrollback compression | libghostty-vt | CodeLima chooses checkpoint cadence, resource limits, compatibility tags, and storage lifetime. |
| Shell, PTY, process group, nonblocking write queue | CodeLima daemon | Never block the PTY drain on renderer work. |
| Renderer process health, restart budget, generation fencing | CodeLima daemon | One worker and one Ghostty instance per terminal. |
| Raw journal, checkpoint watermark, response IDs/deduplication | CodeLima daemon | Restore a checkpoint and replay only later events with responses suppressed. |
| Clipboard/link/image/notification authorization | CodeLima | Guest-provided effects are untrusted data. |
| Vaxis screen and image drawing | CodeLima TUI | Consume versioned, library-independent DTOs; never expose C pointers across IPC. |
| Cross-cgo batching and memory ownership | Thin CodeLima C adapter | No VT semantics, fallback encoding, dynamic symbol lookup, or policy. |

## Detailed design

### 1. Reproducible dependency and ABI boundary

Use the audited revision as the initial upgrade candidate. Do not replace it
with a moving branch during implementation. If a later commit is required, pin
that full hash and repeat the comparison and qualification.

The audited revision requires Zig 0.16.0, replacing CodeLima's current 0.15.2
toolchain pin. The Ghostty installer must use an official source archive or an
exact Git archive plus a checked-in checksum/cache manifest. It must not edit
`build.zig.zon` or discover dependency URLs with ad hoc text extraction.
Network population, offline reuse, build, test, and cleanup must remain exposed
as Make recipes under the project-rooted tooling and `tmp/` directories.

Build the initial terminal surface with:

```text
-Dvt-features=-all,+snapshot,+formatter,+selection,+render-state,+input-encode,+color,+grid-introspection
```

Do not enable `glyph-protocol` or `kitty-graphics` until CodeLima can render
them. Disabled sequences are still safely consumed. Add `+kitty-graphics` in
Phase 5. Record the full feature string, Ghostty revision, Zig version, target,
and optimization mode in a generated build manifest.

Phase 1 may retain the shared library briefly to isolate rebase regressions,
but Phase 3 must statically link `libghostty-vt.a` into only
`codelima-renderer-worker`. Split the cgo implementation into a package that
the main `codelima` command cannot import. The production build then removes:

- `dlopen`/`dlsym` and the required/optional symbol table;
- `CODELIMA_GHOSTTY_VT_LIB` as a production override;
- shared-library lookup paths and packaged `.dylib`/`.so` artifacts;
- “feature unavailable, use handwritten fallback” behavior; and
- the possibility that a renderer starts with headers and a library from
  different revisions.

At worker startup, query `ghostty_build_info` and include its version/build
metadata plus CodeLima's compiled Ghostty revision and feature manifest in the
renderer handshake and diagnostics. Static linkage is the hard compatibility
guarantee; build info makes failures and support reports intelligible. Any
development-only dynamic override must be explicitly unsafe, off by default,
and reject a mismatched build before creating a terminal.

All size-versioned C structs must set their `size` field. Every allocation
returned by Ghostty must be released with the matching Ghostty allocator API.
No C-owned pointer may outlive its documented borrow or cross the worker
protocol.

### 2. Thin renderer adapter

Replace `ghostty_bridge_compat` with a renderer adapter whose allowed duties
are narrowly defined:

- terminal/render-state construction and destruction;
- Go-safe callbacks for PTY responses, log records, and typed effects;
- one bulk visible-grid extraction call per publish, using render-state row and
  cell iterators, `get_multi`, and UTF-8 grapheme access;
- formatter and snapshot buffer ownership helpers; and
- stable translation into CodeLima-owned scalar/byte DTOs.

It must not parse escape sequences, synthesize responses, resolve symbols,
choose host policy, format ANSI, encode input as a fallback, walk scrollback to
recreate a formatter, or own a PTY. A target of at most 450 non-generated C
lines is a useful review budget, but semantic responsibilities are the hard
gate.

Calling cgo for every cell would trade line-count reduction for latency. Keep
the bulk adapter until an upstream bulk resolved-viewport API benchmarks at
parity. Propose such an API upstream with CodeLima's DTO and cgo measurements;
do not add it as another downstream Ghostty patch.

The renderer object itself becomes emulator-only. `Start`, the PTY read pump,
PTY writer, child wait, and handoff methods remain in the daemon session actor
or are deleted if they are legacy duplicates. Production terminal tabs must
always use the daemon-owned terminal path. Retain a pure-Go/Vaxis fallback only
for an explicitly supported non-cgo or test build; do not retain a second
in-process Ghostty session path in the main binary.

### 3. Read formatting

Preserve the public `terminal read` contract while changing its implementation:

- `source=visible` selects viewport top-left through viewport bottom-right.
- `source=recent` selects at most the latest 2,000 history-plus-active rows,
  preserving the current bound.
- Plain reads use `GHOSTTY_FORMATTER_FORMAT_PLAIN` with trailing-space trim.
- ANSI reads use `GHOSTTY_FORMATTER_FORMAT_VT`, initially without unrelated
  terminal extras. Enable style and hyperlink output only where that matches
  the existing CLI contract; palette/mode/cursor restoration belongs to an
  explicit replay/export format, not an incidental read.
- Preserve current soft-wrap behavior until a product decision changes it.
  Formatter `unwrap` must therefore be chosen from characterization fixtures,
  not assumed.

Construct selections from official viewport/history grid references and let
Ghostty handle wide cells, combining graphemes, soft wraps, default versus
explicit colors, underline variants, overline, palette overrides, and
hyperlinks. Delete `visibleTextLockedRaw`, `recentTextLockedRaw`, the manual
scrollback readers, and `ghosttyANSISGRForCell` after differential tests pass.

### 4. Input, paste, and effects

Make the Ghostty key, mouse, and focus encoders required symbols. CodeLima still
maps Vaxis keys/buttons/modifiers and decides whether a TUI shortcut bypasses
the terminal. Ghostty owns protocol mode lookup and byte encoding. Migrate the
removed `ghostty_terminal_mode_get` use to `GHOSTTY_TERMINAL_DATA_MODE` with a
size-initialized `GhosttyTerminalModeConfig`.

Add a semantic paste operation to the renderer protocol. The existing TUI may
continue normalizing Vaxis paste events and splitting a large payload into
bounded UTF-8-safe daemon RPCs. For each logical daemon paste request, the
renderer reads bracketed-paste mode and calls `ghostty_paste_encode` once,
instead of receiving synthetic start/key/end events. This deliberately adopts
Ghostty's removal of unsafe controls such as ESC and NUL. Tests and release
notes must call out any input that is now sanitized.

Install `GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE` and delete
`osc52ClipboardScanner`. The callback delivers normalized, decoded data; the
CodeLima side still enforces payload limits, supported MIME/destinations,
foreground-client rules, and host clipboard authorization. Malformed or
oversized guest data must never reach a host clipboard command.

Use typed title and PWD effects to update tab metadata. Bell, desktop
notification, and progress effects may initially be recorded in the renderer
DTO without UI behavior, then drive badges/notifications in a separate product
change. Unknown-sequence and VT-processing-error data belong in bounded debug
diagnostics, not normal per-operation logs.

### 5. Resize, color, and memory

The upstream resize fix was written specifically to preserve shell prompts by
default. Characterize the old pin plus CodeLima's supplemental signal against
the new pin with no workaround. When the new behavior passes wrapped readline,
canonical-mode, alternate-screen, scrollback, and mouse-tracking cases, delete
`shouldRequestPrimaryScreenRedrawLocked` and the additional process-group
`SIGWINCH`. Normal PTY size updates still produce the operating system's one
standard resize notification.

Set `GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES` to 10,000 to preserve the
current explicit policy; do not rely on an old constructor field or a byte-only
default. Expose effective line and byte limits in diagnostics.

At terminal construction, set Ghostty foreground, background, cursor, and
palette defaults from the outer Vaxis theme when available. Use the official
color-scheme report encoder/effect path rather than formatting CSI responses in
the bridge. Theme changes must be serialized through the renderer actor.

After the functional rebase, schedule caller-driven compression only within
the renderer worker. Compression work must be incremental, cancelable by new
activity, and lower priority than PTY output, input, health probes, and frame
publication. Enable it by default only if the Phase 4 benchmark shows lower
idle RSS without violating render latency or health deadlines.

### 6. Exact same-build renderer recovery

Ghostty snapshot version 1 is CRC-protected and can restore a renderable READY
prefix before older history. It is also explicitly a work in progress with no
binary compatibility guarantee. Use it as an in-memory, same-build checkpoint,
never as durable terminal persistence.

Each accepted checkpoint is an atomic record:

```text
renderer protocol version
CodeLima build version
full Ghostty revision and feature manifest
terminal ID and renderer generation
last applied journal event ID (watermark)
snapshot byte length and digest
snapshot bytes
```

The worker creates a checkpoint after applying an event and before acknowledging
its checkpoint request. Snapshot chunks travel over bounded worker frames; the
daemon publishes a new checkpoint only after validating metadata, total length,
digest, and the Ghostty snapshot's own decode/CRC checks in tests. An incomplete
transfer never replaces the last complete checkpoint.

Enable bounded continuation tracking before the first PTY write. If its limit
is exceeded, skip checkpoint creation until
`GHOSTTY_TERMINAL_DATA_VT_GROUND` is true; do not fabricate a continuation.
Benchmark first, then set cadence and maximum size as named constants. The
initial policy should trigger by bytes/events since the last checkpoint and a
maximum active-time interval, coalesce concurrent requests, and never block the
daemon's PTY drain while encoding or transferring a snapshot.

On same-build renderer replacement:

1. Reject a checkpoint whose protocol, CodeLima build, Ghostty revision,
   feature manifest, terminal ID, or integrity metadata does not match.
2. Decode through READY and publish the active screen as soon as it is safe.
3. Restore remaining history incrementally when the implementation is proven;
   the first implementation may decode the complete snapshot synchronously in
   the isolated worker.
4. Replay only journal events after the checkpoint watermark, with terminal
   responses suppressed and existing response IDs deduplicated.
5. Resume live events only after replay crosses the supervisor's start fence.
6. Fall back to the current bounded raw replay and explicit partial-recovery
   state if there is no valid checkpoint.

The checkpoint does not replace the journal, supervisor, generation fencing,
or response deduplication. It makes the journal a short tail and preserves
exact terminal state after early raw bytes are evicted. A live update to a new
Ghostty build must reject the old in-memory checkpoint and use the existing raw
replay fallback. Persisting or translating snapshots across builds requires a
future upstream compatibility guarantee and a separate ADR.

### 7. Selection, prompt state, and Kitty graphics

These improvements follow the surface-reduction phases so they do not obscure
the rebase.

For selection, route cell/word/line/output drag gestures to Ghostty and format
the resulting selection through its formatter. CodeLima owns the decision to
bypass to the host emulator and must honor guest mouse capture. Add selection
state to the renderer DTO only when it affects drawing.

Expose `cursor_at_prompt` as a tri-state semantic field in cached terminal
status: true, false, or unavailable. Future agent wait primitives may use it as
a strong signal, but must not treat shells without semantic prompt markers as
idle.

For Kitty graphics, enable the feature only after the complete safe path
exists:

- accept inline payloads only at first; disable file, temporary-file, and
  shared-memory media;
- configure a bounded Ghostty image-storage limit and APC limit;
- install the PNG decoder callback inside the disposable renderer process;
- extract deduplicated RGBA assets plus placement/source/destination geometry
  from Ghostty;
- add bounded asset and placement messages to worker, daemon, and client
  protocols;
- materialize and destroy Vaxis images on the TUI side as Ghostty placements
  appear and are evicted; and
- test malformed images, decompression bombs, replacement/deletion, scrolling,
  clipping, resize, renderer restart, and clients that do not support images.

Ghostty owns protocol parsing, image identity/storage, and placement math.
CodeLima owns transport limits, security policy, client capability negotiation,
and Vaxis resources. This delivers Roadmap priority 4 without implementing a
second Kitty protocol stack.

## Phased implementation plan

Each phase should be split into small test-driven changes. Do not keep old and
new emulators active on the same PTY: both could emit query responses. A
temporary comparison harness must feed recorded bytes offline.

### Phase 0 — Characterize and create upgrade gates

1. Add a differential test corpus from current behavior: plain/ANSI reads,
   scrollback, Unicode/graphemes, widths, colors, hyperlinks, key/mouse/focus,
   paste, fragmented OSC 52, title stack, XTQMODKEYS, resize, and response
   bytes.
2. Feed identical recordings to the current and candidate libraries with
   every meaningful chunk boundary, plus randomized chunking. Compare
   canonical CodeLima screen/read DTOs rather than private structs.
3. Record baseline publish latency, worker CPU/RSS, daemon CPU/RSS, IPC bytes,
   archive size, and renderer recovery time for 80x24 and 160x50 terminals,
   `cmatrix`, and 1/10/maximum terminal counts.
4. Add Make recipes for upstream VT tests, the differential suite, benchmarks,
   packaged smoke tests, and verification cleanup.
5. Open or link the upstream XTQMODKEYS work. Decide the icon-title behavior
   from the fixture, with upstream behavior as the default.

Exit: the corpus fails when each existing compatibility behavior is removed,
the audited build can be reproduced offline from its populated cache, and all
baseline artifacts live under project `tmp/` and are cleaned by their recipe.

### Phase 1 — Rebase and remove the downstream source patch

1. Pin Zig 0.16.0 and Ghostty `cecf8167...` (or a separately audited successor).
2. Update headers and migrate constructor/options, modes, callbacks, and render
   iterators to the new C API.
3. Set explicit scrollback and continuation limits and query build metadata.
4. Remove the hyperlink and icon-title patch hunks; land XTQMODKEYS upstream or
   isolate the one temporary tracked hunk.
5. Run Ghostty's `test-lib-vt` build/tests for every supported native target in
   CI before CodeLima tests.

Exit: no broad downstream patch exists, shared-library startup rejects any
mismatch, Linux and macOS packages pass the existing terminal suite, and no
characterization difference is unexplained.

### Phase 2 — Transfer terminal semantics to libghostty-vt

Implement and delete one concern at a time in this order:

1. official hyperlink grid-reference lookup;
2. log callback instead of stderr capture;
3. formatter-backed visible/recent plain and ANSI reads;
4. required key/mouse/focus encoders and mode query;
5. semantic Ghostty paste encoding;
6. Ghostty clipboard effect instead of the OSC scanner;
7. official color options/report encoding; and
8. upstream resize behavior instead of supplemental `SIGWINCH`.

Exit: searches find no local OSC 52 state machine, ANSI SGR screen formatter,
handwritten terminal-input fallback, native stderr redirection, custom
hyperlink export, color-report string, or resize-redraw heuristic. All public
terminal behavior is covered by automated fixtures and relevant real-terminal
QA.

### Phase 3 — Make Ghostty worker-only and statically linked

1. Extract an emulator-only cgo package imported only by
   `codelima-renderer-worker`.
2. Remove the legacy direct Ghostty PTY/session lifecycle from the main
   package. Ensure the production TUI establishes or reconnects to the daemon
   before opening a terminal.
3. Replace `ghostty_bridge_compat` with the thin adapter and direct required
   symbols.
4. Link the feature-trimmed static archive; remove dynamic lookup, library
   environment overrides, and shared-library packaging.
5. Update build, Homebrew, release, license/attribution, diagnostics, and symbol
   inspection checks.

Exit: `go list -deps` for the main command contains no cgo Ghostty package;
only the worker binary exposes/contains the expected Ghostty symbols; running a
packaged build needs no adjacent Ghostty library; and production integration
lines are at least 1,500 below the recorded baseline.

### Phase 4 — Add checkpoints and idle compression

1. Bump the renderer protocol and add chunked checkpoint metadata/data frames.
2. Store the last complete checkpoint and post-watermark journal tail per
   terminal.
3. Restore same-build renderer failures from snapshot plus tail, preserving
   response suppression, generation fences, and shell PID.
4. Add READY-first history restoration if it materially improves recovery
   publication latency.
5. Benchmark and enable bounded idle scrollback compression.

Exit: a renderer killed after journal eviction restores exact screen,
scrollback, cursor, modes, and unfinished parser state without duplicate PTY
responses; corrupt/mismatched snapshots fall back safely; the daemon and other
terminals stay responsive; and compression meets the measured latency/RSS gate.

### Phase 5 — Product improvements on the upstream model

1. Wire host color defaults and theme changes; close or rewrite TODO #1.
2. Expose title, PWD, progress, prompt, and diagnostic effects where useful.
3. Add Ghostty-native embedded selection and formatted copy.
4. Implement the safe Kitty graphics transport and Vaxis renderer, then enable
   `+kitty-graphics`.

Exit: Roadmap priority 4 is complete only after graphics passes protocol,
security, restart, and real-terminal QA. Roadmap priority 5 is complete when
Phases 1–3 and their native qualification are complete; snapshots and later
product features should remain separately visible work.

## Verification strategy

### Automated test matrix

| Contract | Required automated coverage |
|---|---|
| Candidate library | Upstream `test-lib-vt`, C header compile/link smoke, static symbol check, build-info/feature manifest check, and both supported OS/architecture packages. |
| Parser/render parity | Recorded VT corpus, random chunk boundaries, vttest-derived cases, primary/alternate screens, scrollback, resize/reflow, tabs, wide/combining/invalid UTF-8, cursor, default/explicit colors, hyperlinks, and mouse modes. |
| Read API | Golden visible/recent × plain/ANSI outputs, 2,000-row bound, soft wraps across pages, blank lines, styles, palette overrides, hyperlinks, and malformed graphemes. |
| Input | Table-driven key/modifier/Kitty-keyboard, application cursor/keypad, mouse tracking/drag/motion, focus-report gating, output-buffer retry, and unsupported Vaxis events. |
| Paste | Bracketed on/off, LF/CR behavior, UTF-8 chunk boundaries, ESC/NUL/control sanitization, empty/large paste, ordering with normal input, and no command execution before the user's Enter in QA. |
| Clipboard/effects | OSC 52 and OSC 1337 fragmented at every byte, BEL/ST termination, destinations, MIME, invalid base64, oversize limits, denial policy, title/PWD/progress, and no duplicate callbacks. |
| Resize | Wrapped shell prompt, canonical-mode `cat`, no injected `^L`, one normal resize notification, alternate screen, mouse capture, scrollback preservation, and rapid grow/shrink. |
| Renderer isolation | Non-returning native call, stopped/crashed worker, bounded kill/reap, unchanged shell PID, other-terminal and daemon responsiveness, and restart-budget behavior. |
| Snapshot recovery | Split UTF-8/CSI/OSC/DCS/APC at every byte, journal eviction, READY/FINISH, corrupt/truncated/oversized input, wrong build/features/protocol, checkpoint transfer interruption, replay fence, and zero duplicate responses. |
| Performance/memory | 80x24 and 160x50, `cmatrix`, burst output, long scrollback, 1/10/maximum terminals, active/idle compression, publish latency, health latency, CPU/RSS, IPC bytes, recovery time, and archive size. |
| Kitty graphics | Raw/RGB/RGBA/PNG, chunked transfers, placements, scroll/resize/delete, storage/APC limits, disabled media, malformed/decompression-bomb input, restart, unsupported client, and resource destruction. |

Every implementation change must pass `make verify` and `make test-race`.
Protocol, package, or daemon changes also pass `make test-integration`,
`make package`, and the packaged smoke recipe. New Ghostty-specific commands
must be Make recipes rather than undocumented shell invocations. Use `gopls`
diagnostics and references while changing package boundaries, `go doc` for Go
and Vaxis APIs, `gofmt`, and `golangci-lint`.

Before enabling a phase by default, establish its baseline and reject an
unexplained regression greater than 10% in p95 screen-publication latency or
renderer recovery time under the same workload. The PTY drain's nonblocking
invariant and the 20 FPS publication ceiling are absolute gates. Record package
size and RSS deltas even when they improve; a regression may be accepted only
with an explicit documented tradeoff.

### Manual verification

Run every flow in `QA.md` before declaring the complete migration done, using
its setup and cleanup. At minimum, each phase touching terminal behavior must
repeat:

- Flow 5, including renderer containment, shell-PID continuity, large-journal
  handoff, and daemon responsiveness;
- Flow 5b for shared input and geometry seat behavior;
- Flow 7 for keys, multiline paste, focus, resize, `cmatrix`, tab close,
  restart/handoff restoration, OSC 52, hyperlinks, theme, and real rendering;
  and
- Flow 9 after changing native logging, worker identity, or failure evidence.

Kitty graphics needs a new Flow 7 subsection using a real Kitty-capable sample
and an unsupported-client fallback. Selection, notifications, progress, and
prompt-state user surfaces likewise require explicit QA steps when added.
All generated terminals, images, logs copied for comparison, test homes,
checkpoints, packages, and temporary source trees must be removed by the QA
cleanup before the phase is complete.

## Rollout and rollback

- Land the characterization harness first and keep its fixtures after the old
  implementation is deleted.
- Make each semantic transfer independently reviewable and revertible.
- Do not publish a package containing a candidate Ghostty build until Linux and
  macOS CI, race tests, packaged smoke tests, and native terminal QA pass.
- Keep snapshot checkpointing behind a default-off runtime gate for one
  qualification cycle. Emit bounded counters for checkpoint success, size,
  duration, rejection reason, restored watermark, replay-tail bytes, and
  fallback recovery; do not log snapshot contents.
- A rollback uses the previous complete CodeLima worker build. Because
  checkpoints are in-memory and build-tagged, it discards incompatible
  snapshots and uses raw replay. No migration or cleanup of user data is
  required.
- Remove the old implementation and flag after one qualified release. Do not
  leave an indefinite dual stack.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Upstream C API changes again | Exact revision and headers, static link, generated manifest, compile tests, small adapter, and deliberate future rebases. |
| Snapshot v1 changes or corrupts state | Same-build in-memory use only, strict metadata/integrity validation, tail journal, fallback replay, and no durable files. |
| Formatter output changes the CLI contract | Characterization/golden tests and explicit trim/unwrap/extras settings; version an intentional contract change. |
| Ghostty paste sanitization surprises users | Golden unsafe-control tests and release notes; favor safe upstream behavior. |
| Static link increases package size or complicates targets | Feature trimming, per-target static smoke/link inspection, and recorded archive-size gate. |
| Removing stderr capture hides evidence | Structured Ghostty log callback in the worker and existing worker failure evidence; process isolation remains for hangs without logs. |
| Snapshot/compression work delays live output | Run only in the worker actor, coalesce, bound and benchmark work, prioritize writes/health, and retain supervisor deadlines. |
| A bulk adapter preserves too much custom C | Enforce the allowed-duty contract and line budget; propose a measured bulk API upstream rather than grow a compatibility layer. |
| Guest effects cross host trust boundaries | Keep all authorization and resource limits in CodeLima; treat callback bytes as untrusted. |
| Removing the local Ghostty session breaks an implicit fallback | Add a production-path test before deletion; either establish/reconnect the daemon or present an explicit error. Keep only a separately named non-cgo/test fallback if it is a supported contract. |

## Documentation and decision records

Implementation must keep repository contracts synchronized:

- Add an ADR for the worker-only static Ghostty boundary and upstream ownership
  contract; it refines ADRs 25 and 108.
- Supersede or refine ADR 95 when the supplemental resize signal is removed.
- Add a separate ADR for same-build renderer checkpoints and snapshot/tail
  recovery.
- Update `BUILD.md`, the Makefile, release packaging, and troubleshooting when
  the Zig pin, source acquisition, feature set, static artifacts, or runtime
  diagnostics change.
- Update `README.md` for user-visible read, clipboard, selection, prompt,
  notification, or graphics behavior.
- Record the thin native-adapter pattern and differential terminal-fixture
  pattern in `PATTERNS.MD` once implemented and reused.
- Mark Roadmap priority 5 complete only at its Phase 3 exit gate and priority 4
  complete only at the Kitty graphics exit gate.
- Rewrite TODO #1 because the upstream color-configuration dependency is
  resolved; retain the host-theme discovery work until delivered.
- Put every scoped-out or partially completed phase item in `TODO.md` with its
  problem, proposed solution, and tradeoffs before switching work.

## Completion criteria

This plan is complete when all of the following are true:

- CodeLima uses an exactly pinned, feature-trimmed libghostty-vt built and
  tested reproducibly with no broad downstream source patch.
- Ghostty is statically linked only into the renderer worker; the main binary
  has no Ghostty cgo dependency or runtime library loader.
- Terminal formatting, clipboard parsing, input/paste encoding, hyperlink
  lookup, resize reflow, colors, and native logging use supported upstream APIs.
- The remaining C bridge is a thin batching/lifetime adapter with no terminal
  semantics or compatibility fallbacks.
- Same-build renderer replacement restores a valid checkpoint plus journal
  tail exactly, retains the shell PID, emits no duplicate terminal responses,
  and falls back safely when a checkpoint is unusable.
- Production integration code is at least 1,500 lines below the recorded
  baseline, with tests allowed and expected to grow.
- Automated, race, integration, package, performance, and all applicable manual
  QA gates pass on supported native platforms.
- ADRs, `README.md`, `BUILD.md`, `PATTERNS.MD`, `ROADMAP.md`, `TODO.md`, the
  Makefile, and `.gitignore` accurately describe the delivered state.
