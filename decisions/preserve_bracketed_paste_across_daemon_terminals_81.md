# Preserve Bracketed Paste Across Daemon Terminals

Status: Accepted

## Context and Problem Statement

Vaxis reports a bracketed paste as `PasteStartEvent`, one or more `EventPaste` keys, and `PasteEndEvent`. After terminal runtimes moved into the daemon, the TUI daemon adapter accepted only key and mouse events: it discarded both paste boundaries, sent one synchronous RPC per decoded character, and converted pasted line feeds into carriage returns. Multiline text therefore appeared slowly and each newline could submit a shell command instead of remaining inside the paste buffer.

## Decision Drivers

* Preserve terminal-native bracketed-paste behavior without duplicating Ghostty mode state in the client.
* Send ordinary pastes as one bounded semantic operation instead of one RPC per character.
* Keep multiline paste as text until the user explicitly submits it.
* Preserve input ordering and the daemon terminal actor's single-mutator boundary.
* Keep large UTF-8 pastes below the protocol's 1 MiB frame limit without splitting code points.

## Considered Options

* Forward paste start, every key, and paste end as individual daemon requests.
* Have the client unconditionally add bracketed-paste escape sequences to raw input.
* Buffer paste keys in the daemon TUI adapter and send bounded semantic paste requests that the daemon expands into start, payload, and end events.

## Decision Outcome

Chosen option: "Buffer paste keys in the daemon TUI adapter and send bounded semantic paste requests that the daemon expands into start, payload, and end events", because the client can batch transport without needing to know whether the terminal application enabled bracketed-paste mode. The daemon-owned Ghostty actor remains responsible for deciding whether the boundary markers should reach the PTY.

The TUI treats every `EventPaste` key as terminal data before shortcut matching. The daemon adapter accumulates decoded paste bytes until `PasteEndEvent`, normalizes CRLF to LF while preserving LF, and sends `terminal.send_event` with type `paste`. Payloads larger than 64 KiB are split only at UTF-8 boundaries; each chunk is a complete semantic paste. The daemon expands each request synchronously into `PasteStartEvent`, one batched `EventPaste` key, and `PasteEndEvent` on the terminal actor.

Because protocol-2 daemons do not understand the semantic `paste` event, this request contract is daemon protocol 3. Exact-version clients must reject older daemons; `daemon update` is the only cross-protocol path.

### Positive Consequences

* Normal pastes cross the daemon boundary in one request and appear without character-by-character RPC latency.
* Bash, editors, and other bracketed-paste-aware applications receive the boundaries they requested.
* Multiline text is inserted with LF newlines and is not submitted merely because it was pasted.
* Pasted characters cannot accidentally activate TUI shortcuts.
* Large and non-ASCII pastes remain bounded and valid UTF-8 on the JSON-lines protocol.

### Negative Consequences

* Paste text is held in client memory until Vaxis emits the matching end event.
* A paste larger than 64 KiB uses multiple semantic requests and therefore multiple bracket pairs.
* A malformed input stream that never emits `PasteEndEvent` is committed only when the next non-paste key arrives.

## Pros and Cons of the Options

### Forward every paste event individually

* Good, because it mirrors the Vaxis event stream directly.
* Bad, because it retains one synchronous local RPC per decoded character.
* Bad, because a partial transport failure can leave an unmatched paste boundary.

### Add raw bracket markers in the client

* Good, because the whole payload can use one raw-input request.
* Bad, because the client does not own terminal mode state and would send escape sequences even when bracketed paste is disabled.
* Bad, because it bypasses the daemon terminal actor's semantic input path.

### Send a batched semantic paste

* Good, because one daemon handler invocation owns each boundary/payload/boundary sequence.
* Good, because Ghostty continues to decide whether bracket markers are appropriate.
* Good, because transport batching and protocol-size limits remain client concerns.
* Bad, because this adds a private `terminal.send_event` type.

## Links

* Refines [Move terminal runtime ownership into a per-home daemon](daemon_owned_terminal_runtimes_64.md)
* Preserves [Use an exact-version JSON-lines local daemon protocol](exact_version_json_lines_daemon_protocol_65.md)
* Preserves [Serialize terminal runtime mutation through actors](terminal_runtime_actor_model_63.md)
* Refined by [Use the caller binary for cross-protocol daemon update](use_the_caller_binary_for_cross_protocol_daemon_update_84.md)
