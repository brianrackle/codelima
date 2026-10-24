# Position fallback graphemes without cursor drift

## Context and Problem Statement

Live Logs verification exposed stale characters after wrapping and resizing
joined emoji. Vaxis conservatively reserved six cells for `👩🏽‍💻` and four for
`👩‍🚀`, while tmux 3.6 reported four and two respectively through cursor
position queries. Contiguous output advanced by the host width rather than
the model width, leaving old content inside reserved cells and shifting text.

## Considered Options

* Clear the reserved span and explicitly position output following fallback
  clusters with multiple code points and more than one cell.
* Guess tmux-specific Unicode widths.
* Replace compound emoji with escaped text in log views.

## Decision Outcome

In Vaxis's alternate-screen renderer, when neither negotiated Unicode-core
width nor explicit-width output is available, clear a multi-codepoint
cluster's reserved span using its current style. Return to the cluster's
origin, render the intact grapheme, and explicitly position the next changed
cell. The clear stays within the row. Preserve the current conservative width
model and continuation-cell accounting.

Negotiated Unicode and explicit-width output keep their existing paths, as do
ASCII and ordinary single-codepoint wide characters. This corrects manager
text without changing raw shell attachment or guest terminal storage.

### Positive Consequences

* A host shaping fewer cells cannot leave stale content or shift later text.
* Joined emoji and combining sequences stay intact rather than being escaped.
* The correction applies to all manager surfaces, not just Logs.

### Negative Consequences

* Ambiguous fallback clusters require extra space writes and cursor moves.
* Conservative widths can still leave padding where the host shapes fewer
  cells; exact shaping remains the terminal's responsibility.

## Pros and Cons of the Options

Terminal-name guesses would depend on Unicode version and tmux build options.
Escaping log text would avoid ambiguity but hide ordinary user text and leave
the same renderer defect elsewhere. Explicit positioning preserves the model
and bounds the extra work to ambiguous fallback clusters.

## Links

* Found while verifying [ADR 157](bounded_local_node_logs_and_bootstrap_capture_157.md).
