# libghostty-vt adoption implementation

Branch: `feat/libghostty-vt-adoption`.
Source plan: [September review](sep_7_plan.md), recommendations G01–G14 and their
terminal correctness prerequisites. The user authorized implementation of the
adoption recommendations on September 7, 2026.

## Completion ledger

No item is complete merely because its API is exposed. It must be connected to
the production path and covered by the plan's automated and manual gates.

| Area | Status | Required result |
|---|---|---|
| G01 | Implemented; Linux package qualified | Exact static dependency in worker only, header/archive/Go/handshake identity, CGO-free main graph and clean-PATH release smoke. |
| G02 | Implemented | Bulk owned frames, real dirty-row C/Go reuse, coherent immutable cells/text/metadata/graphics and revision fences. |
| G03 | Implemented | Native text/VT formatter, 4 MiB output cap and explicit 2,000-history-row recent-read contract. |
| G04 | Implemented | Public hyperlinks; only characterized XTQMODKEYS compatibility retained, upstream icon/window-title distinction preserved. |
| G05 | Implemented | Required native encoders and one safe UTF-8 paste, 64 KiB maximum, atomic bounded PTY admission. |
| G06 | Implemented | Bounded native writes to physical seat owner only; no reads, false Kitty acknowledgement or replay effects. |
| G07 | Implemented | Bounded native logs/effects, metadata badges/messages, no process stderr redirection. |
| G08 | Implemented; native visual QA pending | Cancellable host defaults/palette/theme, hidden-tab and recovery propagation, pixel resize; existing SIGWINCH retained pending native reflow qualification. |
| G09 | Implemented; native visual QA pending | Native drag/word/line/output selection, host bypass, copy and stale receiver/epoch fences. |
| G10 | Implemented; native visual QA pending | F7 native incremental literal search, bounded worker/ticks, query cancellation and queue-full clear retry. |
| G11 | Implemented; Linux host QA passed | Strict same-build core envelopes, bounded decoded allocations, ordered tails, raw fallback, quotas and handoff v5. |
| G12 | Implemented; cross-platform measurements pending | Bounded idle steps, 64 MiB/10k history policy, paired Linux RSS/wake evidence and disable switch. |
| G13 | Static path implemented; native visual QA pending | Bounded worker→daemon→frontend assets, clipping/pixels/z/upload/deletion tests; animation/glyph/placeholder limitations explicit. |
| G14 | Implemented; full upstream suite blocked | Explicit features, owned lifetimes/errors, reproducible builds, schema/C/native conformance; Debug compilation killed by guest. |
| Prerequisites | Implemented | Readiness/generation fences, complete replay suppression, coherent publication and actual-dispatch hard deadlines distinct from caller timeouts. |
| Qualification | Partially complete | Go/local/Linux gates below passed; full upstream and unavailable native/manual matrix remain TODO #41. |
| Documentation | Updated | ADR 132, README/BUILD/PATTERNS/QA, TODO #40–42 and ROADMAP reflect delivered behavior and limitations. |

“Implemented” is not a claim that all release gates have passed. Full native
qualification remains explicitly incomplete; unsupported extensions are in
TODO #42. Non-adoption R recommendations are outside this implementation and
remain in TODO #40.

### Local verification

- `make verify`: passed (gofmt check, golangci-lint, whole Go suite, complete
  retained Vaxis suite and both executable builds).
- `make test-race`: passed across all root-module packages.
- Final `make test-integration`: passed, 17.167 seconds.
- Final `make test-package`: passed with four-patch identity, empty PATH and
  unavailable legacy shared-library paths; disposable package output removed.
- Additional frontend presenter tests passed five normal and three race runs:
  actual capability/pixel negotiation, async preparation, wire-byte upload
  budget, multipart serialization, cropping/z, asset reuse/deletion and fallback.
- Targeted gopls checks, native adapter normal/race, ABI schema, strict C bridge
  and adapter AddressSanitizer checks passed. See the native record below.
- [Per-flow manual report](libghostty_vt_adoption_qa.md): host PTYs, >1 MiB
  live handoff, renderer containment and read-only diagnostics passed. Native
  Lima/macOS and physical two-window/visual flows were unavailable, not passed.
- Manual QA found and fixed terminal-ID/tab-ID handoff confusion; adversarial
  checks fixed graphics integer overflow, stale UI effects and queued short
  deadlines incorrectly replacing a long-running renderer operation.
- All task-created manual/package/scratch artifacts and disposable processes
  were removed. Existing user scratch and managed build/toolchain caches remain.

### Native qualification record — 2026-09-07

Four-patch native source `82232ecde55405559dec29c5466cb9e39938cb41`, Zig
0.16.0, installed static ReleaseSmall identity
`2e205255ce6d39bbfdf6f314824a31f12c8de3bbf32fac535a3d01e13289626c`:

- `make test-ghostty-vt-schema`: passed, aarch64-linux ABI manifest, 159 types.
- `make test-ghostty-bridge`: passed, including decoded allocation limits and
  immutable dirty-row reuse; the C adapter also passed AddressSanitizer checks
  against the prebuilt, uninstrumented Zig archive.
- `make test-ghostty-adapter GHOSTTY_TEST_FILTER='Ghostty|CloneColors|ValidateColors|ValidateInteraction'`:
  passed both normally and with `GOFLAGS=-race` across all three packages.
- Final unfiltered `make test-ghostty-vt GHOSTTY_VT_TEST_JOBS=1`: **not passed**.
  With parent Go gates held, Debug native compilation terminated with signal
  KILL; build summary was 40/45 steps succeeded, two failed. The approximately
  3.8 GiB Linux guest remains insufficiently qualified for the full upstream
  suite. No further retry was made; TODO item 41 retains the adequately sized
  native-host rerun and complete interactive QA requirements.

## Work ownership

- Main agent: Go terminal adapter, actor integration, semantic input, package
  separation, product integration, documentation, and final verification.
- Build agent: exact Ghostty/Zig source and compiler installation, make recipes.
- Native adapter agent: public C integration, native semantics, bounded callbacks.
- Renderer agent: readiness/publication/replay/deadline prerequisites and tests.

The pre-existing untracked `libghostty_plan.md` is preserved. The review and
tracking edits from the prior task are retained on this branch.
