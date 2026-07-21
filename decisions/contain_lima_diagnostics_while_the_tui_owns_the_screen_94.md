# Contain Lima diagnostics while the TUI owns the screen

## Context and Problem Statement

CodeLima switched its structured service logger and libghostty capture to a
file before starting the full-screen TUI, but the long-lived `LimaClient`
retained the stderr writer installed during CLI service construction. A
successful `limactl list` can report an unhealthy instance as a warning on
stderr, so initial load or the recurring refresh wrote that warning directly
over Vaxis chrome even though the runtime command returned successfully.

## Decision Drivers

* No background subprocess may write to terminal stderr while Vaxis owns the screen.
* Successful-command diagnostics must remain inspectable because Lima uses them for warnings.
* CLI commands must retain their existing direct stderr behavior.
* TUI background-operation output must remain visible in the operation surface.
* The solution must use the existing rotating TUI log and logging level.

## Considered Options

* Discard Lima stderr in TUI mode.
* Redraw the TUI after every direct stderr write.
* Replace the TUI service's Lima diagnostic writer with a structured file-log adapter.

## Decision Outcome

Chosen option: "Replace the TUI service's Lima diagnostic writer with a
structured file-log adapter", because it contains the subprocess at the mode
boundary without losing diagnostics or changing CLI behavior.

`Service.enableFileLogging` now updates the concrete `LimaClient.Stderr` after
creating the rotating file logger and before the initial TUI data load. The
adapter records every non-empty subprocess stderr chunk at warning level with
`source=limactl`. It does not wait for a newline because `os/exec` does not
promise line-aligned writes and a final unterminated diagnostic must not be
lost. Runtime command failure mapping continues to use its separate bounded
stderr buffer.

Background TUI mutations still use `Service.withIO`, which clones the Lima
client and replaces its writers with the operation progress writer. CLI mode
does not call `enableFileLogging`, so it continues forwarding Lima diagnostics
to caller stderr.

### Positive Consequences

* Lima warnings cannot overwrite borders, terminal cells, or footer hints.
* Diagnostics from successful commands remain available in the normal TUI log.
* Existing command-error detail and background-operation progress are unchanged.
* The routing rule is covered by a regression test using a successful fake Lima list.

### Negative Consequences

* A diagnostic split across subprocess writes can produce more than one structured record.
* All Lima stderr chunks use CodeLima warning severity even when their embedded text names another level.

## Pros and Cons of the Options

### Discard Lima stderr in TUI mode

* Good, because it prevents screen corruption with minimal plumbing.
* Bad, because a successful Lima command can carry the only evidence of an unhealthy instance on stderr.

### Redraw after direct stderr writes

* Good, because the terminal would eventually look correct again.
* Bad, because users still see flicker and displaced content, and concurrent writes can race redraws.

### Route Lima diagnostics to the TUI file logger

* Good, because it enforces one sink for all TUI diagnostics.
* Good, because it preserves successful-command warnings for inspection.
* Bad, because the adapter must define chunk and severity semantics for unstructured subprocess output.

## Links

* Refines [Structured logging and a TUI message surface](structured_logging_and_tui_message_surface_59.md).
* Applies to the CLI boundary from [Return to Lima as the sole runtime with schema v4](return_to_lima_as_the_sole_runtime_92.md).
