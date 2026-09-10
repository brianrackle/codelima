# Preserve printable input without physical key identities

## Context and Problem Statement

Legacy host terminal input reports characters rather than physical keyboard
positions. The Ghostty adapter rejected printable characters absent from its
physical-key mapping, dropping punctuation such as `>` and `%` and non-ASCII
text. Mapping only unshifted keys covered the earlier synthetic key tests but
missed actual decoded input.

## Decision Drivers

* Preserve printable input and international text without guessing a layout.
* Keep modifiers and press/repeat/release behavior under Ghostty's encoder.
* Honor guest keyboard protocols and retain existing function-key handling.

## Considered Options

* Guess US-layout physical keys for shifted characters.
* Write unrecognized text directly to the PTY.
* Use Ghostty's unidentified physical key with the observed text/codepoint.

## Decision Outcome

Chosen option: "Use Ghostty's unidentified physical key with the observed
text/codepoint", because physical identity is optional for printable text.
When the existing physical mapping has no match and the decoded keycode is
printable, the adapter passes `GHOSTTY_KEY_UNIDENTIFIED`. Existing UTF-8,
codepoint, modifier and action fields continue through the native encoder.
Unknown non-printable function keys remain unsupported.

Tests decode printable ASCII, accented text, CJK and emoji through Vaxis,
then check native press/repeat output and legacy release suppression. Modified
input and Kitty associated-text/event reporting also exercise unidentified
keys. No native library patch or protocol change is required.

### Positive Consequences

* Punctuation and non-ASCII input reach the shell intact.
* Guest keyboard modes retain native encoding semantics.
* No assumed US keyboard position is needed for text-only events.

### Negative Consequences

* Legacy protocols still cannot recover physical positions or modifiers that
  the host did not report.

## Pros and Cons of the Options

### Guess physical keys

* Good, because it could cover shifted ASCII.
* Bad, because it invents layout information and misses international text.

### Write text directly

* Good, because plain shell typing would work.
* Bad, because it bypasses guest protocols and key-event semantics.

### Native unidentified key

* Good, because Ghostty already supports separate physical identity and text.
* Good, because one encoding path handles modifiers and event actions.
* Bad, because the frontend must preserve all information actually reported.

## Links

* Refines [Ghostty key encoding](use_ghostty_key_encoder_for_embedded_terminal_input_24.md).
* Complements [Static libghostty-vt adoption](adopt_static_libghostty_vt_with_bounded_worker_contracts_132.md).
