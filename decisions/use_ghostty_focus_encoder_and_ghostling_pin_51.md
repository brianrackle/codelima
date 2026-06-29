# Use Ghostty Focus Encoder and Ghostling-Pinned VT Library

## Context and Problem Statement

CodeLima already delegates most embedded-terminal semantics to `libghostty-vt`, but focus gained/lost reports for DECSET 1004 were still hand-encoded in Go. The Ghostling demo also pins a newer Ghostty VT API surface than CodeLima's previous packaged commit, so the question was how to keep CodeLima aligned with that demonstrated surface while preserving the existing runtime-loaded bridge model.

## Decision Drivers

* Keep CodeLima's packaged `libghostty-vt` aligned with the Ghostling-era C API surface
* Prefer Ghostty-owned terminal encoders over local escape-sequence formatting
* Preserve fallback behavior when older runtime libraries do not expose the optional focus encoder
* Keep CodeLima's small local Ghostty patch rebased with the source commit it targets

## Considered Options

* Keep the older Ghostty commit and local Go focus reports
* Rebase the packaged Ghostty commit to the Ghostling pin and use `ghostty_focus_encode`
* Adopt the full Ghostling renderer model, including Kitty graphics rendering, in this change

## Decision Outcome

Chosen option: "Rebase the packaged Ghostty commit to the Ghostling pin and use `ghostty_focus_encode`", because it advances the library/API alignment and removes another locally encoded terminal report without mixing in the larger Kitty graphics renderer work tracked separately.

### Positive Consequences

* The default packaged Ghostty commit now matches the Ghostty commit used by the Ghostling demo snapshot reviewed for roadmap item 0.11.
* DECSET 1004 focus gained/lost reports now use Ghostty's focus encoder when the runtime library exposes it.
* Older or partially compatible `libghostty-vt` builds still fall back to the previous CSI I / CSI O bytes.
* The local `ghostty-vt-codelima.patch` has been rebased to the newer Ghostty source layout.
* The installer disables Ghostty's lib-vt xcframework emission on macOS because CodeLima packages and runtime-loads the direct `libghostty-vt.dylib` artifact.

### Negative Consequences

* The local patch still carries CodeLima-specific API additions for hyperlink URI lookup and terminal query behavior.
* The Ghostty source rebase increases the chance of future patch churn because `libghostty-vt` is still an unstable API surface.
* macOS installer behavior now depends on CodeLima explicitly passing the Ghostty build flag that skips the optional xcframework install step.
* Kitty graphics rendering remains out of scope for this decision and still belongs to the separate roadmap item.

## Pros and Cons of the Options

### Keep the older Ghostty commit and local Go focus reports

Continue packaging the previous Ghostty snapshot and keep emitting focus-report bytes directly from Go.

* Good, because it avoids rebasing the local Ghostty patch.
* Good, because focus reporting behavior stays unchanged.
* Bad, because it leaves CodeLima behind the Ghostling API surface.
* Bad, because it keeps duplicating terminal encoding logic that Ghostty already owns.

### Rebase the packaged Ghostty commit to the Ghostling pin and use `ghostty_focus_encode`

Update the default Ghostty source commit, rebase the local patch, and route focus reports through an optional bridge function.

* Good, because it keeps CodeLima aligned with the current Ghostling demonstration surface.
* Good, because it follows the same ownership pattern as the key and mouse encoders.
* Good, because fallback bytes preserve compatibility with older runtime libraries.
* Bad, because the rebased local patch needs ongoing maintenance.

### Adopt the full Ghostling renderer model, including Kitty graphics rendering, in this change

Port Ghostling's Kitty graphics storage, PNG decode callback, placement iteration, and image rendering path into CodeLima's terminal renderer now.

* Good, because it would close more of the visible Ghostling feature gap.
* Good, because it would directly support richer terminal media use cases.
* Bad, because it is a larger renderer and asset-lifecycle change than focus encoding.
* Bad, because roadmap item 0.10 already tracks Kitty graphics as a separate feature.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/projects/codelima/ADR_TEMPLATE.md)
* Related [rebase_ghostty_vt_packaging_to_upstream_api_25.md](/Users/brianrackle/projects/codelima/decisions/rebase_ghostty_vt_packaging_to_upstream_api_25.md)
* Related [use_ghostty_key_encoder_for_embedded_terminal_input_24.md](/Users/brianrackle/projects/codelima/decisions/use_ghostty_key_encoder_for_embedded_terminal_input_24.md)
* Related [use_ghostty_mouse_encoder_for_embedded_terminal_input_26.md](/Users/brianrackle/projects/codelima/decisions/use_ghostty_mouse_encoder_for_embedded_terminal_input_26.md)
