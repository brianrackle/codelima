# Agent Monitoring Plan

Status: Draft

## Goal

Surface per-node Codex CLI and Claude Code activity inside CodeLima so the user can see, from the project tree and related UIs, when an agent is:

- `running`
- `waiting_input`
- `done`
- `error`
- `interrupted`

This plan intentionally keeps Lima as the source of truth for VM runtime state and introduces a separate runtime source of truth for agent activity.

## Current CodeLima Baseline

- `internal/codelima/service.go` already builds `ProjectTree` from persisted project metadata plus reconciled node runtime state from Lima.
- `internal/codelima/tui_vaxis.go` already launches managed terminals by spawning `codelima shell <node>`, which guarantees the existing TUI is entering the VM through CodeLima rather than bypassing it.
- Node metadata no longer persists live runtime state in `node.yaml`, which is the right direction for agent activity as well.

## What To Reuse From opensessions

Useful patterns to reuse:

- agent-specific watcher adapters
- a small shared agent event contract
- a tracker that reduces raw watcher events into UI-friendly node state
- unseen/notified state derived on the host side

Patterns not to copy directly in the first pass:

- generic mux-provider abstractions
- cwd-to-session resolution as the primary identity mechanism
- a Bun/WebSocket server as the required backbone

CodeLima already has a stronger runtime identity model than `opensessions`: node ID is the stable unit of ownership.

Relevant references:

- https://github.com/Ataraxy-Labs/opensessions/blob/main/README.md
- https://github.com/Ataraxy-Labs/opensessions/blob/main/CONTRACTS.md
- https://github.com/Ataraxy-Labs/opensessions/blob/main/docs/explanation/architecture.md

## Recommended Architecture

### 1. In-Guest Monitor Per Running Node

Run a small guest-side `codelima-agent-monitor` whenever a node is running.

Responsibilities:

- watch Codex and Claude runtime artifacts inside the guest
- correlate events to the current node
- combine transcript semantics with process liveness
- publish compact runtime snapshots or append-only events to a host-visible location

This should not depend on the TUI being open.

### 2. Host-Visible Runtime Channel

Expose a per-node runtime directory from the host into the guest, for example:

`$CODELIMA_HOME/nodes/<node-id>/runtime/`

The guest monitor writes:

- `agent-status.json` for the latest per-node reduced state
- optionally `agent-events.jsonl` for debugging and replay

Important constraints:

- use atomic rename for snapshot writes
- keep the data ephemeral
- do not write agent runtime state back into `node.yaml`

### 3. Host-Side Aggregation

Extend the read path that already merges Lima runtime state so it also merges agent runtime state.

That merge should happen in memory when servicing:

- `project tree`
- `node list`
- `node show`
- the full-screen TUI
- the future tmux sidebar

The store remains responsible for persisted metadata; the runtime aggregator remains responsible for live agent status.

## Agent Status Model

Recommended host-side shape:

```go
type AgentRuntimeStatus struct {
    Agent      string    `json:"agent"`
    ThreadID   string    `json:"thread_id,omitempty"`
    ThreadName string    `json:"thread_name,omitempty"`
    Status     string    `json:"status"`
    UpdatedAt  time.Time `json:"updated_at"`
    Source     string    `json:"source,omitempty"`
}
```

Recommended reduced statuses:

- `running`
- `waiting_input`
- `done`
- `error`
- `interrupted`
- `idle`

Recommended priority for collapsed node badges:

- `error`
- `waiting_input`
- `running`
- `interrupted`
- `done`
- `idle`

The project tree should show a collapsed per-node badge, while the details pane can show all active threads.

## Detection Strategy

### Codex

Read guest-local Codex artifacts such as:

- `$CODEX_HOME/sessions/**/*.jsonl`
- `$CODEX_HOME/session_index.jsonl` when available

Use transcript semantics for thread naming and task boundaries, similar to `opensessions`.

Suggested mapping:

- user message, tool activity, reasoning, assistant commentary -> `running`
- assistant final answer or task complete while process is still alive -> `waiting_input`
- assistant final answer or task complete after process exit -> `done`
- aborted turn -> `interrupted`
- explicit error event -> `error`

### Claude Code

Read guest-local Claude artifacts such as:

- `~/.claude/projects/<encoded-path>/*.jsonl`
- optional session-to-process mapping artifacts if needed

Suggested mapping:

- user message -> `running`
- assistant message with tool use -> `running`
- assistant message without tool use while process is still alive -> `waiting_input`
- assistant message without tool use after process exit -> `done`
- process failure or explicit error -> `error`

## Why Process Liveness Must Be Included

Transcript-only reduction is not enough for the user-facing state CodeLima wants.

For both Codex and Claude, a transcript may indicate that an assistant response is complete while the CLI process is still alive and waiting for the next user message. In CodeLima that should be represented as `waiting_input`, not `done`.

The monitor therefore needs both:

- semantic signals from transcript/journal files
- process liveness and foreground ownership signals from inside the guest

## UI Integration

### Project Tree

Show node rows as a combination of:

- VM runtime status from Lima
- collapsed agent badge from the runtime tracker

Example shapes:

- `running | codex:waiting`
- `running | claude:running`
- `stopped`

Project rows should also surface a descendant notification marker when any child node has unseen `waiting_input`, `done`, `error`, or `interrupted` activity.

### TUI

The existing TUI already has a model for entry status text and background operation summaries. Agent runtime badges should be integrated as additional runtime state, not as synthetic long-running operations.

### CLI / JSON

JSON read surfaces should include agent runtime state in output, but those fields must remain read-only and omitted from persisted YAML metadata.

## Implementation Phases

### Phase 1

- define guest/host runtime file contract
- add host-side reader and reducer
- surface agent status in `node show` JSON only

### Phase 2

- add `project tree` and `node list` badges
- add unseen-state tracking on the host side

### Phase 3

- add detail views in the existing TUI
- add logs or debugging surfaces for raw agent events

### Phase 4

- refine CLI-specific adapters as Codex and Claude formats evolve
- add additional providers if CodeLima expands beyond Codex and Claude

## Open Questions

- Should the guest monitor run as a background process started during `node start`, or as a user service installed during bootstrap?
- Should the host keep only the reduced snapshot, or also retain a bounded event log for diagnostics?
- How should multiple simultaneous agent threads on one node be collapsed when different providers are active at once?
- Do we want node-level notifications only, or project-level unread counts as well?

## Recommendation

Start with a guest monitor plus host-side runtime merge. That approach matches CodeLima's existing separation between persisted metadata and live runtime state, and it avoids pretending Lima can answer agent-semantic questions that only the guest can observe.
