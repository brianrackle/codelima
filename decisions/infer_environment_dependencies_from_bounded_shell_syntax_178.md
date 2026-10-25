# Infer environment dependencies from bounded shell syntax

## Context and Problem Statement

The Environments reference warns when a command uses curl without an earlier
install, identifies environments that can provide it, and offers to add curl
to an earlier install command. Text matching confuses comments and quoted data
with execution. Running scripts to inspect them would introduce side effects
while browsing a catalog.

## Considered Options

* Match curl and install strings with regular expressions.
* Execute a guest probe whenever the catalog is inspected.
* Parse shell syntax and conservatively report recognized source evidence.

## Decision Outcome

Use the syntax package from mvdan.cc/sh/v3 v3.12.0. It supports the project's
Go 1.24.1 toolchain. Its module requirements also select creack/pty v1.1.24
instead of v1.1.18; terminal and real-PTY regressions are required for that change.
Only the parser is used. No shell interpreter, expansion evaluator, command
subprocess, guest probe, or environment-variable lookup runs during analysis.

Compute reports with the asynchronous recipe catalog snapshot, using the
current saved environment definitions. Renderers and provider hints consume
that same snapshot. Edited built-ins cannot retain provider evidence from their
shipped defaults. The snapshot and reports are frontend-local; no daemon
protocol or stored configuration format changes.

Recognize literal curl execution, including common command/exec/sudo wrappers,
quoted command names and command substitutions. Ignore comments, literal
here-document data and uncalled function bodies. Recognized apt/apt-get install
commands use an explicit option allowlist; simulation, download-only, unknown
options and expanded arguments cannot establish an install. Recognized removal
clears that evidence. Other package managers, function calls, eval, sourced
files and nested shell strings are not resolved.

Track successful and failed paths separately. Semicolon-separated statements
merge both paths, while a following saved command block receives only the
preceding block's successful path because bootstrap stops on failure. AND/OR
and if/else preserve their execution dependencies; pipelines and background
work cannot impose install ordering. Loops and other unsupported compounds
may expose uses, but cannot prove a preceding installation. This is an advisory
heuristic, not proof that a guest lacks curl or that a script will succeed.
The UI says "static check" and explains that the image or earlier scripts can
already provide the command.

Bound parsing to 64 KiB per block, 256 KiB and 256 blocks per environment.
Invalid syntax and exceeded limits produce an explicit unavailable hint above
the command content, so long blocks cannot bury it.
These hints never block editing, saving, node creation or execution.

Offer an edit only when a recognized earlier install is common to every
uncovered curl use summarized by that warning. Retain the exact command and
byte offset from the parser, recheck the source before applying it, and insert
only the literal " curl". Preserve comments, quoting, Unicode and line breaks.
Enter on the warned command, or clicking the suggestion, opens the existing
command editor within an unsaved environment draft. Applying the command and
saving the environment remain explicit actions; e retains ordinary editing.

### Positive Consequences

* The reference example has an actionable warning without executing code.
* Conditional or simulated installs do not create false provider evidence.
* Proposed edits preserve source formatting and existing save/discard behavior.
* Refreshes use current definitions and do not parse scripts during drawing.

### Negative Consequences

* Dynamic shell behavior and unrecognized package managers remain unknown.
* Conservative merging can warn about scripts that work on a particular image.
* Parsing adds a dependency and requires regression coverage for its PTY module
  version selection.

## Validation

The focused target is make test-dependency-hints. It checks source-versus-data
distinctions, ordering/control flow, package flags, bounded work, absence of
execution, exact source edits, stale offsets, current providers and keyboard/
mouse draft save/discard. Full verify, race, CLI integration and terminal
attachment checks, plus live wide/compact inspection, are recorded in the
design delivery ledger.

## Links

* [Environments reference](../plans/tui-vnext/reference/Environments.dc.html)
* [Shared environment drafts](inspect_recipes_from_global_metadata_and_edit_command_blocks_176.md)
* [Pinned parser module](https://github.com/mvdan/sh/blob/v3.12.0/go.mod)
