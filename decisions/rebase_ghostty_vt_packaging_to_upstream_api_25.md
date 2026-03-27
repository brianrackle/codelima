# Rebase Ghostty VT Packaging to the Upstream API Surface

## Context and Problem Statement

CodeLima originally packaged an older `libghostty-vt` commit plus an external `ghostty-web` WASM-oriented patch, then bound a narrow C API slice directly from an inline cgo block. That was enough for basic VT parsing and render-state access, but it left the remaining Ghostling-style improvements blocked behind missing upstream APIs for mouse encoding, viewport scrolling, richer render traversal, and terminal effects. The question was how to unlock those newer Ghostty capabilities without replacing the existing runtime-loaded library model.

## Decision Drivers

* Keep the current packaged `libghostty-vt` runtime-loading model
* Align the packaged Ghostty surface more closely with the upstream APIs Ghostling already uses
* Reduce dependence on the older external `ghostty-web` patch
* Localize upstream API adaptation so later Ghostty rebases stay contained
* Keep `make init` reliable in the sandboxed local toolchain flow

## Considered Options

* Keep the older Ghostty commit and extend the existing inline bridge on top of the patched WASM-style API
* Rebase the packaged library to the newer upstream Ghostty commit and introduce a local compatibility bridge
* Replace runtime loading with direct linking to the newer Ghostty API

## Decision Outcome

Chosen option: "Rebase the packaged library to the newer upstream Ghostty commit and introduce a local compatibility bridge", because it unlocks the Ghostling-era API surface while preserving the existing packaged-library and `dlopen` startup model.

### Positive Consequences

* CodeLima now packages the newer upstream Ghostty VT API surface used by Ghostling-era integrations.
* The old external `ghostty-web` patch is no longer part of the build; only a smaller CodeLima-specific local patch remains.
* Upstream API adaptation moved out of the inline cgo preamble and into dedicated compatibility files, which keeps later bridge work localized.
* The widened bridge exposes the capabilities needed for the next Ghostty follow-ups, such as mouse encoding and viewport-owned scrolling.
* The Ghostty installer now refreshes both the platform-scoped install link and the legacy compatibility link used by the cgo include path.

### Negative Consequences

* The local compatibility bridge is larger and now owns more ABI-sensitive adaptation logic.
* CodeLima still carries a small local Ghostty patch for hyperlink URI access and terminal-query behavior that the upstream C API does not yet expose directly.
* The build script now vendors Ghostty's `uucode` package into the temporary checkout to keep local builds reliable, which adds one more installer step to maintain.

## Pros and Cons of the Options

### Keep the older Ghostty commit and extend the existing inline bridge on top of the patched WASM-style API

Continue using the older packaged library and grow the original inline cgo bridge.

* Good, because it minimizes packaging churn.
* Good, because it avoids rebasing onto a newer upstream Ghostty snapshot.
* Bad, because it keeps CodeLima pinned to an API surface that is much narrower than Ghostling's.
* Bad, because each new Ghostty-backed improvement would first require more one-off patching on top of the older library.
* Bad, because the inline cgo bridge is a poor place to hold increasingly complex compatibility code.

### Rebase the packaged library to the newer upstream Ghostty commit and introduce a local compatibility bridge

Package the newer upstream Ghostty VT library, keep runtime loading, and adapt CodeLima's current bridge contract through dedicated compatibility files.

* Good, because it unlocks the newer upstream Ghostty APIs without forcing a packaging-model rewrite.
* Good, because it removes the large external `ghostty-web` patch from the build.
* Good, because it keeps later Ghostty API rebases and follow-up gaps localized to the compatibility layer.
* Bad, because it introduces more bridge code and ABI maintenance in `internal/codelima/ghostty_bridge_compat.c`.
* Bad, because the installer now owns a small amount of build-system adaptation for local reliability.

### Replace runtime loading with direct linking to the newer Ghostty API

Drop `dlopen` and link directly against Ghostty's exported C API.

* Good, because it would remove dynamic symbol loading and some capability probing.
* Good, because every declared API would be available at compile time.
* Bad, because it changes the packaged-library discovery model that already works for CodeLima's release layout.
* Bad, because it expands the scope beyond the bridge and packaging problem we actually needed to solve first.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Related [adopt_ghostty_0.md](/Users/brianrackle/Projects/codelima/decisions/adopt_ghostty_0.md)
* Related [use_ghostty_key_encoder_for_embedded_terminal_input_24.md](/Users/brianrackle/Projects/codelima/decisions/use_ghostty_key_encoder_for_embedded_terminal_input_24.md)
