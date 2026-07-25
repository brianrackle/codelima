
# Implementation status

Implemented locally on July 24, 2026:

- connection close-cause correlation, first-cause preservation, heartbeats,
  stable logical client identities, automatic reconnect, authoritative
  epoch/sequence synchronization, and non-replayed uncertain mutations
- bounded per-client outbound pumps, one socket writer, concurrent query
  dispatch, an ordered mutation lane, and an ID-multiplexed client response
  reader
- asynchronous immutable terminal snapshots and compact reconnect state
- token-independent Ghostty stderr draining and bounded daemon/terminal close
- one pure-Go PTY/session actor plus one separately packaged, supervised
  Ghostty renderer process per terminal, with generation fencing, bounded
  replay journal, renderer-response deduplication, partial-recovery reporting,
  and terminal-local restart budgets
- deterministic tests for a real non-returning C renderer call, cross-terminal
  and daemon responsiveness, shell-PID preservation, slow clients, blocked
  requests, reconnect, delivery uncertainty, oversized frames, and stderr
  records larger than Scanner limits

ADRs 107 and 108 record the implemented decisions. A separate pure-Go session
worker, PTY escrow, and unexpected-daemon worker adoption remain deliberately
deferred: the observed native liveness boundary is isolated without them, and
cooperative live update already preserves the PTY and shell. Native macOS
sleep/resume and process-budget qualification remain in `TODO.md`.

---

The plan addresses the **failure outcome** very well:

- The TUI automatically reconnects.
- It resynchronizes with daemon-owned terminals.
- A broken client connection no longer looks like every terminal froze.
- A slow or broken TUI cannot destabilize the daemon or other clients.

But the diagnostics only prove:

> The TUI’s Unix-socket connection was already broken when it attempted to write.

They do **not** prove why the connection broke or which side initiated it. Technically, the terminals did not disconnect—the TUI disconnected from a healthy daemon and healthy terminals.

## What is still missing

Add a specific root-cause investigation phase before or alongside reconnect:

### 1. Correlate both ends of every connection

Assign a daemon-generated `ConnectionID` during the handshake and log it on both sides.

Every connection termination should produce one authoritative record:

```go
type ConnectionCloseRecord struct {
	ConnectionID     uint64
	ClientInstanceID string
	DaemonEpoch      string

	Initiator   string // client, daemon, kernel/peer, live-update
	Phase       string // dial, handshake, ready, shutdown
	Reason      string // write-timeout, EOF, queue-full, protocol-error, etc.
	Underlying  string

	LastReadAt  time.Time
	LastWriteAt time.Time

	InboundQueueDepth  int
	OutboundQueueDepth int
}
```

The first component that decides the connection is unusable records the close reason. Later `EOF` and `broken pipe` errors are secondary consequences.

### 2. Instrument the likely causes

Record enough information to distinguish:

- Daemon write deadline expired because the TUI stopped reading.
- TUI reader exited because of an unexpected error.
- TUI or daemon context was canceled accidentally.
- Daemon live update replaced `daemon.sock`.
- A protocol/framing error closed the connection.
- A client outbound or inbound queue overflowed.
- The TUI event loop blocked the socket reader.
- Some code path closed or replaced the socket unexpectedly.
- The daemon process or epoch changed.

For every connection, track:

```text
last successful read and write
last complete frame read and written
reader and writer goroutine exit causes
queue depth and oldest queued-frame age
read/write deadline values
daemon epoch
daemon.sock inode or identity
live-update state
bytes received and sent
```

### 3. Preserve the original error

A common bug is:

```text
reader detects the real error
    → closes connection
writer later gets broken pipe
    → broken pipe becomes the visible error
```

Use `context.WithCancelCause` or a close-once object that preserves the **first failure cause**:

```go
type connectionFailure struct {
	once  sync.Once
	cause atomic.Pointer[error]
}

func (f *connectionFailure) Fail(
	cancel context.CancelCauseFunc,
	err error,
) {
	f.once.Do(func() {
		f.cause.Store(&err)
		cancel(err)
	})
}
```

The user-facing and diagnostic error should come from that first cause—not whichever goroutine reports last.

### 4. Test each disconnection mechanism

Add deterministic fault tests for:

- TUI stops reading from the socket.
- Daemon stops reading.
- Daemon response write blocks.
- Client event consumer blocks.
- Client reader exits unexpectedly.
- Client writer gets `EPIPE`.
- Socket is replaced during live update.
- Malformed or oversized frame.
- Accidental context cancellation.
- Outbound queue reaches capacity.
- Mac sleeps and resumes.
- TUI is suspended with `SIGSTOP` long enough to hit deadlines.

Each test should assert both:

1. The TUI reconnects without losing the shell.
2. The recorded close reason identifies the deliberate fault.

## Corrected rollout priority

I would make the rollout:

1. **Connection cause instrumentation**
2. **Automatic reconnect and authoritative resynchronization**
3. **Per-client bounded reader/writer isolation**
4. **Fix the specific disconnection cause revealed by telemetry**
5. **Asynchronous cached snapshots**
6. **Ghostty renderer-process isolation**

The reconnect design should still ship even before the cause is fully known. Network and Unix-socket connections are inherently fallible, so recovery is required regardless. But reconnect should not be described as eliminating the root cause.

The success criterion for the next captured incident should be a diagnosis like:

> Daemon connection 184 closed at 21:39:01 because its outbound queue remained full for five seconds; the TUI socket reader had stopped consuming while the UI actor was blocked processing a snapshot.

Until you can produce that explanation, the plan **contains and recovers from the disconnection but has not yet eliminated its root cause**.


# Revised diagnosis

The new capture materially changes the immediate architecture decision.

For the freeze captured on **July 23, 2026 at 9:42:26 PM PDT**, the daemon, the `mrwing` terminal actor, the PTY, and Ghostty were still making progress. The TUI had encountered a broken pipe at **9:39:03 PM PDT**, about three minutes earlier, transitioned into a disconnected state, and never attempted to reconnect.

For this incident, the effective architecture was:

```text
Healthy daemon and terminal sessions
             │
             │ broken Unix-socket connection
             ✕
     Permanently disconnected TUI
             │
             └── every tab and VM shown by that TUI appears frozen
```

Therefore:

> **The first production fix should be a reconnectable, resynchronizing TUI-daemon session—not the session/renderer worker split.**

The Ghostty/cgo serialization hazard remains credible, but it is a separate failure class that this capture did not exhibit. The two problems should have separate ADRs, failure injection, and rollout plans.

---

# Recommended target architecture

```text
TUI process
  ├── UI/model actor
  │     └── last known immutable daemon state
  │
  └── connection supervisor
        ├── reconnect state machine
        ├── one socket reader
        ├── one socket writer
        ├── pending request registry
        └── heartbeat and full resynchronization
                         │
                         │ reconnectable Unix connection
                         ▼
Pure-Go daemon
  ├── client session A
  │     ├── bounded outbound mailbox
  │     ├── reader pump
  │     └── writer pump
  ├── client session B
  ├── terminal registry/state actor
  ├── immutable snapshot cache
  │
  ├── Terminal A session actor
  │     ├── PTY and shell
  │     ├── output journal
  │     └── renderer supervisor ── renderer process ── Ghostty/cgo
  │
  └── Terminal B session actor
        ├── PTY and shell
        ├── output journal
        └── renderer supervisor ── renderer process ── Ghostty/cgo
```

This differs from the original proposal in one important way:

> **Keep the pure-Go PTY/session actor in the daemon initially. Move only Ghostty into a renderer process.**

That is the minimum later architecture that:

- Contains a stuck cgo call to one terminal.
- Preserves the shell and PTY when a renderer is killed.
- Reuses the existing actor, process-group, PTY handoff, and teardown code.
- Avoids session-worker adoption, PTY escrow, and additional high-volume IPC.
- Retains the existing daemon live-update model.

A separate session worker is justified only when unexpected daemon-crash survival becomes an explicit requirement.

---

# 1. Reconnectable TUI transport

## Connection state machine

The TUI connection must be treated as disposable presentation infrastructure, not as the owner of a terminal session.

```text
Disconnected
    │ dial succeeds
    ▼
Handshaking
    │ valid Hello/Sync exchange
    ▼
Synchronizing
    │ full state installed atomically
    ▼
Ready
    │ EOF / EPIPE / timeout / protocol error
    └──────────────────────────────────────► Disconnected
```

There must be no terminal `DisconnectedForever` or latched-error state. Only an explicit TUI shutdown stops reconnection.

While disconnected:

- Continue running the TUI event loop.
- Keep displaying the last immutable snapshot.
- Mark it visibly stale and show “Reconnecting to daemon.”
- Keep quit/help/local navigation responsive.
- Reject terminal input rather than silently queueing it.
- Never restart or terminate the daemon merely because one client connection failed.

After reconnecting:

- Perform a full daemon-state synchronization.
- Re-select the previous terminal by durable terminal ID.
- Fetch the latest cached screen snapshot.
- Clear the stale banner.
- Do not create a replacement terminal unless the synchronized state says the original terminal is gone.

## Connection supervisor sketch

```go
type LinkState uint8

const (
	LinkDisconnected LinkState = iota
	LinkConnecting
	LinkHandshaking
	LinkSynchronizing
	LinkReady
)

type LinkStatus struct {
	State          LinkState
	Generation     uint64
	DaemonEpoch    string
	LastEventSeq   uint64
	LastError      error
	LastConnected  time.Time
	LastDisconnect time.Time
}

type Supervisor struct {
	socketPath string
	clientID   string
	dial       func(context.Context, string) (net.Conn, error)

	statusSink func(LinkStatus)
	modelSink  func(SyncSnapshot)
	eventSink  func(DaemonEvent)

	connectionGeneration atomic.Uint64
	current              atomic.Pointer[connectionSession]
}

func (s *Supervisor) Run(ctx context.Context) error {
	backoff := newFullJitterBackoff(100*time.Millisecond, 5*time.Second)

	for ctx.Err() == nil {
		generation := s.connectionGeneration.Add(1)
		s.statusSink(LinkStatus{
			State:      LinkConnecting,
			Generation: generation,
		})

		dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := s.dial(dialCtx, s.socketPath)
		cancel()

		if err != nil {
			s.statusSink(LinkStatus{
				State:      LinkDisconnected,
				Generation: generation,
				LastError:  err,
			})
			if err := sleepContext(ctx, backoff.Next()); err != nil {
				return err
			}
			continue
		}

		session := newConnectionSession(conn, generation, s.clientID)

		s.statusSink(LinkStatus{
			State:      LinkHandshaking,
			Generation: generation,
		})

		syncState, err := session.Handshake(ctx)
		if err != nil {
			session.Close(err)
			s.statusSink(LinkStatus{
				State:      LinkDisconnected,
				Generation: generation,
				LastError:  err,
			})
			continue
		}

		s.statusSink(LinkStatus{
			State:        LinkSynchronizing,
			Generation:   generation,
			DaemonEpoch:  syncState.DaemonEpoch,
			LastEventSeq: syncState.StateSequence,
		})

		// Replace the entire client-side daemon model before accepting events.
		s.modelSink(syncState)
		s.current.Store(session)

		s.statusSink(LinkStatus{
			State:         LinkReady,
			Generation:    generation,
			DaemonEpoch:   syncState.DaemonEpoch,
			LastEventSeq:  syncState.StateSequence,
			LastConnected: time.Now(),
		})
		backoff.Reset()

		err = session.Serve(ctx, s.eventSink)

		s.current.CompareAndSwap(session, nil)
		session.Close(err)

		s.statusSink(LinkStatus{
			State:          LinkDisconnected,
			Generation:     generation,
			DaemonEpoch:    syncState.DaemonEpoch,
			LastEventSeq:   session.LastEventSequence(),
			LastError:      err,
			LastDisconnect: time.Now(),
		})
	}

	return context.Cause(ctx)
}
```

Each connection incarnation owns its reader, writer, heartbeat, and pending requests. When either pump fails:

1. Close the socket once.
2. Cancel the connection context.
3. Unblock the other pump.
4. Complete every pending request according to its delivery state.
5. Discard any subsequently arriving messages tagged with the old local connection generation.
6. Return to the reconnect loop.

---

# 2. Resynchronization protocol

A reconnect cannot simply resume reading events. The TUI may have missed arbitrary registry, generation, focus, title, terminal-close, or snapshot updates.

Use these distinct identities:

| Identity | Purpose |
|---|---|
| `ClientInstanceID` | One TUI process incarnation |
| `ConnectionGeneration` | One TUI socket incarnation |
| `DaemonEpoch` | One daemon process incarnation |
| `StateSequence` | Globally ordered daemon events within an epoch |
| `TerminalGeneration` | One terminal/session incarnation |
| `RendererGeneration` | One renderer worker incarnation |
| `OutputSequence` | One terminal’s ordered PTY byte stream |
| `RequestID` | Mutation/read correlation and deduplication |

Do not reuse “generation” for all of these.

```go
type Hello struct {
	ProtocolVersion      uint16
	ClientInstanceID     string
	ConnectionGeneration uint64

	LastDaemonEpoch   string
	LastStateSequence uint64
}

type SyncSnapshot struct {
	ProtocolVersion uint16
	DaemonEpoch     string
	StateSequence   uint64

	Terminals []TerminalSummary
}

type TerminalSummary struct {
	TerminalID         string
	TerminalGeneration uint64
	RendererGeneration uint64

	Title            string
	VMName           string
	SnapshotSequence uint64
	SnapshotStale    bool
}

type DaemonEvent struct {
	DaemonEpoch   string
	StateSequence uint64
	Kind          EventKind
	Payload       json.RawMessage
}
```

For the first release, always send a full control-state sync after reconnection. Delta replay is unnecessary complexity.

The daemon must make subscription and snapshot capture atomic with respect to state changes:

```text
state actor receives AttachClient
    1. capture immutable state at sequence S
    2. enqueue Sync(S) as the client's first outbound frame
    3. register client as a subscriber
    4. process subsequent state mutations as S+1, S+2, ...
```

The actor must use a nonblocking bounded mailbox operation; it must not perform socket I/O.

Client rules:

- Install `SyncSnapshot` atomically.
- Accept an event only when its daemon epoch matches.
- Ignore duplicate sequences at or below the last applied sequence.
- Require the next event to be exactly `last+1`.
- On a sequence gap, protocol violation, or daemon-epoch change, discard the connection and perform another full sync.

This is much simpler and safer than attempting in-place gap repair.

---

# 3. Request and mutation semantics

A socket failure can make a mutation’s result uncertain.

Go writes can time out after writing only part of a frame, so a failed write is not universally equivalent to “the daemon did not receive it.” 

Track every request through these states:

```go
type DeliveryState uint8

const (
	RequestQueued DeliveryState = iota
	RequestMayHaveBeenSent
	RequestCompleted
)
```

On connection loss:

| Request state | Read operation | Mutation |
|---|---|---|
| Still queued | `ErrDisconnected` | `ErrNotSent` |
| Writing or awaiting response | `ErrDisconnected` | `ErrOutcomeUnknown` |
| Completed | Return result | Return result |

Rules:

- **Never automatically replay keyboard, mouse, paste, shell-start, or other action-like mutations.**
- Automatically reissue only ordinary read operations after synchronization.
- Reconcile desired-state operations as new requests:
  - Current terminal dimensions
  - Current focus state
  - Absolute selected terminal
  - Absolute scroll position, where available
- Prefer client-generated durable IDs for creation requests.
- Make close-by-terminal-ID idempotent.
- Keep a bounded daemon-side deduplication cache keyed by `(DaemonEpoch, ClientInstanceID, RequestID)` for explicit retries.

Exactly-once behavior across a daemon crash is not realistic without durable transactional storage. The correct contract is:

> At-most-once within a daemon epoch, with explicit “outcome unknown” reporting when acknowledgement is lost.

---

# 4. Heartbeats and dead connection detection

The broken connection should be detected before the next user action.

Use an application-level ping/pong on the same framed connection:

```text
client writer → Ping(nonce)
server reader → server outbound mailbox → Pong(nonce)
client reader → heartbeat tracker
```

This exercises the real reader, writer, framing, and outbound queue. It should not be an independent health goroutine that can report success while the connection’s actual pumps are blocked.

Reasonable starting values are:

- Ping every 10 seconds.
- Declare the connection dead after 30 seconds without inbound progress.
- Two-second dial timeout.
- Three-second handshake timeout.
- Five-second write deadline.
- Operation-specific RPC deadlines.

Use a fake clock in tests; avoid real-time sleeps.

---

# 5. Daemon client-session isolation

The current sequential broadcast and sequential request handling are unnecessary shared failure boundaries.

## One writer per connection

Every client gets:

- One reader goroutine.
- One writer goroutine.
- A bounded outbound mailbox.
- A connection-scoped context.
- A bounded number of in-flight RPCs.

No other goroutine writes to the socket.

```go
func (s *clientSession) writePump(ctx context.Context) error {
	for {
		frame, err := s.outbound.Pop(ctx)
		if err != nil {
			return err
		}

		// Set this before every frame because deadlines remain in effect
		// until replaced or cleared.
		if err := s.conn.SetWriteDeadline(
			time.Now().Add(s.writeTimeout),
		); err != nil {
			return err
		}

		if err := writeFrame(s.conn, frame); err != nil {
			return err
		}
	}
}
```

Go connection deadlines apply to pending and future I/O until replaced, which makes per-frame deadline management important. 

Use two bounded priorities:

- High: sync, response, pong, shutdown, protocol errors.
- Low: ordinary events and snapshot-invalidations.

Bound by both frame count and total encoded bytes. A channel bounded only by message count can still retain excessive memory when frames vary greatly in size.

## Nonblocking broadcast

```go
func (d *Daemon) Broadcast(ev DaemonEvent) {
	clients := d.clientRegistry.Snapshot()

	for _, client := range clients {
		if !client.TryEnqueueEvent(ev) {
			client.Close(ErrSlowClient)
		}
	}
}
```

`Broadcast` must never call `conn.Write`. A slow or non-reading client is disconnected and later reconstructs its state through full synchronization.

## Concurrent request dispatch

The socket reader must not execute a terminal request inline.

```text
reader
  → validate frame
  → handle Ping/Cancel immediately
  → submit RPC to bounded dispatcher
  → terminal actor preserves per-terminal mutation order
```

One blocked terminal request must not prevent:

- Ping processing.
- Cancellation.
- Requests concerning another terminal.
- The connection reader from detecting EOF.
- Daemon status requests.

The connection is a transport boundary, not a serialization boundary.

## Client disconnect must never close terminals

Client teardown may:

- Remove the client subscription.
- Cancel connection-scoped handlers.
- Fail pending responses.
- Release the client mailbox.

It must not:

- Close a terminal.
- Kill a PTY.
- Change terminal ownership.
- Trigger a daemon shutdown.
- Restart a VM.

Terminal lifecycle changes require an explicit terminal mutation.

---

# 6. Immutable asynchronous snapshots

Normal terminal reads should not synchronously enter Ghostty.

Each terminal publishes an immutable snapshot:

```go
type TerminalSnapshot struct {
	TerminalID         string
	TerminalGeneration uint64
	RendererGeneration uint64
	SnapshotSequence   uint64
	OutputSequence     uint64

	CreatedAt time.Time
	Stale     bool
	Screen    ScreenSnapshot
}
```

The daemon stores the most recent value, ideally as an atomic pointer or state-actor-owned immutable value.

Screen events become coalescible invalidations:

```text
TerminalSnapshotAvailable {
    terminal_id
    terminal_generation
    renderer_generation
    snapshot_sequence
}
```

The TUI fetches the latest cached snapshot rather than requesting “render now.”

Benefits:

- Reconnect synchronization is inexpensive.
- A frozen renderer leaves the last screen visible and marked stale.
- Daemon status and terminal list never wait for Ghostty.
- Repeated screen changes can collapse into one latest-value update.
- Slow clients do not cause renderer or PTY backpressure.

---

# 7. Ghostty isolation: use a renderer-only process

The minimum architecture that guarantees one stuck Ghostty call cannot block another terminal is:

```text
Pure-Go daemon
  ├── Terminal A PTY/session actor
  │       └── Renderer A process with Ghostty
  └── Terminal B PTY/session actor
          └── Renderer B process with Ghostty
```

The renderer must be a separate executable or separate Go command with a package graph that imports the Ghostty cgo bridge. Do not rely solely on a runtime `--renderer` mode in a daemon binary that still links the cgo package.

The renderer receives no PTY descriptor.

### Output

```text
PTY
 → daemon terminal actor
 → bounded output journal
 → bounded renderer queue
 → Ghostty
 → immutable snapshot
 → daemon cache
```

The PTY reader never waits for renderer delivery. When the renderer queue fills, disconnect and replace that renderer.

### Input

```text
semantic client input
 → daemon
 → renderer encoding
 → daemon terminal actor
 → ordered PTY write queue
 → shell
```

Terminal-generated replies follow the same renderer-to-daemon-to-PTY route.

### Renderer hang

1. Send the health request through the renderer’s Ghostty-owning actor.
2. On deadline, mark its snapshot stale.
3. Stop routing new input to that renderer.
4. Send graceful termination.
5. Escalate to `SIGKILL`.
6. Increment `RendererGeneration`.
7. Start the replacement.
8. Replay the available journal.
9. Replace the cached snapshot when the new renderer catches up.

The shell PID, PTY descriptor, terminal ID, and terminal generation remain unchanged.

This directly achieves the key goal without a session worker or PTY escrow.

---

# 8. Renderer replay is not automatically side-effect-free

Raw terminal output can contain operations that cause external effects, including:

- Terminal replies sent back to the shell.
- Title changes.
- Clipboard requests.
- Notifications.
- Other host integrations.

Blindly replaying journal bytes into a replacement renderer could duplicate those effects.

The renderer protocol therefore needs one of these contracts:

### Preferred initial contract

```text
Replay mode:
    update emulator state
    suppress all external side effects
Live mode:
    permit terminal replies and host effects
```

After the renderer reaches the current output sequence, switch atomically to live mode.

This may omit a response whose original outcome was uncertain at the moment of renderer failure, but it avoids automatically duplicating terminal input to the application.

### More exact later contract

Tag renderer-produced effects with:

```text
terminal ID
terminal generation
output sequence
effect ordinal
effect kind
```

The daemon/session actor can then deduplicate effects across renderer generations. This requires Ghostty integration to expose a reliable causal boundary.

Without full emulator-state serialization, exact recovery cannot be guaranteed. The product should say “renderer recovered from available output history” rather than claiming exact scrollback and mode restoration.

---

# 9. Do not implement PTY escrow in the daemon yet

A duplicated descriptor refers to the same open file description, and duplicated descriptors share underlying status flags and locks.  The underlying open file description remains alive until all associated descriptors are closed. 

That means daemon escrow introduces real complications:

- `O_NONBLOCK` changes can affect every duplicate.
- Any accidental read can steal PTY bytes.
- Extra open references change close and hangup timing.
- Live-update handoff must prove there is always exactly one intended escrow owner.
- Descriptor leaks can keep terminal sessions alive unexpectedly.
- Session-worker failure may leave the shell blocked once the kernel PTY buffer fills, even if an escrow descriptor remains open.

The renderer-only worker design makes escrow unnecessary: the daemon already owns the durable PTY and only the disposable renderer is restarted.

If PTY ownership is moved out of the daemon later, prefer a durable PTY-holder/session process over a daemon-held “unused” duplicate. Treat session-worker failure as session loss until a specific product requirement justifies the additional recovery mechanism.

---

# 10. Renderer stderr handling

Once Ghostty is isolated per process, remove the per-call process-global fd 2 redirection.

Set each renderer’s stderr destination once at process launch:

```text
renderer stderr
  → continuously drained pipe
  → bounded diagnostic ring
  → excess bytes discarded
```

Do not use `bufio.Scanner`; stderr is a byte stream and may contain arbitrarily long lines. Use `io.CopyBuffer` into a writer that retains only a bounded tail while continuing to consume all bytes.

When `os/exec.Cmd.StderrPipe` is used, Go explicitly requires all pipe reads to complete before `Wait` is called.  Structure the renderer supervisor so one component owns process reaping and the stderr drain cannot silently stop on an oversized token.

This removes both the global Ghostty mutex and the scanner-token deadlock mechanism from the daemon.

---

# 11. Revised rollout

## Phase 0: Preserve the diagnostic distinction

Add separate fault labels and test hooks:

```text
client_transport_disconnect
client_blocked_reader
daemon_blocked_response_writer
terminal_actor_stall
ghostty_cgo_hang
renderer_crash
renderer_restart_loop
```

Do not report all of these as “terminal freeze.”

## Phase 1: Automatic reconnect and full resync

Ship first:

- TUI connection supervisor.
- No latched disconnect state.
- Full synchronization handshake.
- Daemon epoch and event sequence.
- Stale cached UI during reconnect.
- Safe pending-request classification.
- Heartbeat through the real connection.
- Intentional `GoAway` for daemon live update.

This directly automates the currently recommended manual recovery of quitting and reopening only the TUI.

## Phase 2: Per-client daemon isolation

- One reader and one writer per connection.
- Bounded high/low outbound mailboxes.
- Write deadline for every frame.
- Nonblocking broadcasts.
- Bounded concurrent request dispatch.
- Disconnect slow clients.
- Connection teardown never affects terminal lifecycle.

## Phase 3: Asynchronous snapshot cache

- Actor-published immutable snapshots.
- Coalesced snapshot invalidations.
- Read RPCs served exclusively from cache.
- Snapshot age and stale/degraded status.

## Phase 4: Renderer-only subprocess

- Separate cgo renderer helper binary.
- Keep PTY/session actors in the daemon.
- Per-terminal renderer generations.
- Output journal and replay mode.
- Same-actor renderer health probes.
- Bounded restart policy.

## Phase 5: Reconsider session workers only with evidence

Move PTYs into session workers only if there is evidence that:

- Pure-Go PTY handling destabilizes the daemon, or
- Shell survival across unexpected daemon crashes is a required product contract.

Do not implement PTY escrow or daemon-crash worker adoption merely as prerequisites for resolving the observed freezes.

---

# 12. Required tests

The reconnect test suite should include:

1. Force-close the TUI socket while a shell is running. Verify the TUI returns to `Ready`, the terminal ID and generation are unchanged, and the shell PID is unchanged.
2. Inject `EPIPE` into the client writer. Verify no permanent disconnect latch.
3. Drop the connection while a mutation is awaiting acknowledgement. Verify `ErrOutcomeUnknown` and no automatic replay.
4. Drop it while a request remains queued. Verify `ErrNotSent`.
5. Remove several events before delivery. Verify the sequence gap forces full synchronization.
6. Replace the daemon socket during live update. Verify clients redial the pathname and validate the new daemon epoch.
7. Block one RPC handler. Verify ping, status, and another terminal’s requests continue.
8. Attach a client that never reads. Verify only that client is disconnected.
9. Fill the client event queue. Verify the daemon remains responsive and memory remains bounded.
10. Generate a reconnect storm from many clients. Verify jitter and singleton daemon startup behavior.

The renderer suite should include:

1. Permanently hang Ghostty for Terminal A. Verify daemon status and Terminal B remain responsive.
2. Verify the health request is queued through the Ghostty actor and times out.
3. Kill Renderer A. Verify Terminal A’s shell PID and PTY remain unchanged.
4. Block Renderer A’s input queue. Verify the daemon continues draining the PTY journal.
5. Replay output and verify replay mode emits no PTY replies, clipboard changes, or notifications.
6. Send obsolete renderer-generation messages. Verify they are discarded.
7. Exhaust the restart budget. Verify only Terminal A is marked degraded.
8. Race child exit, renderer kill, terminal close, daemon shutdown, and live-update handoff.
9. Feed stderr containing a multi-megabyte unterminated line. Verify it is continuously drained.
10. Run race tests and subprocess fault tests repeatedly on both macOS and Linux.

---

# Direct answers to the review questions

1. **Is the global Ghostty mutex sufficient to explain the observed freeze?**  
   It is sufficient to explain a hypothetical daemon-wide cgo freeze, but it does not explain this captured incident. This incident was a dead TUI connection with a healthy daemon.

2. **Combined process per terminal or session/renderer split?**  
   Neither should be the first fix. For cgo containment, use a renderer-only process and keep the PTY actor in the daemon. A combined process unnecessarily loses the shell when killed; the full split is unnecessary until daemon-crash survival is required.

3. **Is daemon PTY escrow worthwhile?**  
   Not now. It adds shared-descriptor and lifecycle complexity while solving no current requirement.

4. **Direct session-to-renderer communication or via daemon?**  
   With the recommended design, the daemon session actor communicates with its renderer directly. If session workers are later introduced, high-volume PTY data should be direct while the daemon remains the supervisor/control plane.

5. **How should exact recovery work without serialization?**  
   It cannot be exact. Replay the bounded journal in side-effect-suppressed mode, report truncated history explicitly, and preserve the stale snapshot until recovery finishes.

6. **Cached asynchronous snapshots or request/reply snapshots?**  
   Cached asynchronous snapshots. Normal TUI reads should never synchronously enter Ghostty.

7. **Are generation IDs and at-most-once semantics sufficient?**  
   Not alone. Also require daemon epochs, event sequences, output sequences, durable request IDs, delivery-state tracking, and explicit uncertain outcomes.

8. **Minimum architecture preventing one cgo call from affecting another terminal?**  
   One Ghostty renderer OS process per terminal, with no Ghostty/cgo linkage in the daemon.

9. **Is process-per-terminal overhead unacceptable?**  
   That requires measurement. Benchmark proportional set size, startup latency, file descriptors, idle CPU, and behavior with the expected maximum terminal count. Use one packaged renderer helper binary.

10. **Implement daemon-crash adoption?**  
    Defer it. It does not address the diagnosed freeze or the cgo blast radius.

11. **Missing Unix edge cases?**  
    Shared open-file-description flags, partial writes, PTY output side effects during replay, extra-master close semantics, `SIGWINCH` ordering, process-group ownership, stale `SCM_RIGHTS` generations, fd leaks, inherited stderr descriptors, one-time child reaping, and bounded shutdown waits.

12. **What is overengineered?**  
    Session workers, daemon PTY escrow, and unexpected-daemon-crash adoption before implementing client reconnect, per-client pumps, event sequencing, and cached snapshots.

# Architecture decision

The recommended decision is:

> **First make TUI-daemon connections reconnectable and state-resynchronizing. Then isolate only Ghostty in a per-terminal renderer process while retaining PTY/session actors in the pure-Go daemon. Defer session workers, PTY escrow, and daemon-crash adoption until evidence or an explicit product contract requires them.**

That design fixes the confirmed failure, contains the unconfirmed cgo hazard, preserves live shells during renderer replacement, and avoids turning the control plane into a distributed terminal system prematurely.

# Revised CodeLima Terminal Freeze Design Review

## 1. Updated diagnosis

The capture from **2026-07-24T04:42:26Z** materially changes the immediate architecture decision.

For this incident:

- The daemon was responsive through newly established connections.
- The `mrwing` terminal actor and shell were alive.
- Terminal reads and snapshots succeeded.
- No Ghostty mutex, actor, or broadcast blockage was sampled.
- The frozen TUI’s existing connection had encountered a broken pipe and remained permanently disconnected.
- Restarting the TUI repaired the problem because it created a new connection and reattached to daemon-owned state.

Therefore, the narrowest verified failure is:

```text
healthy daemon + healthy terminal
              │
              ✕ stale/broken physical client connection
              │
        TUI permanently latches disconnected
```

This capture does **not** prove that every historical freeze has the same cause. The process-global Ghostty mutex remains a credible separate liveness hazard. It simply was not the cause demonstrated by this capture.

A client-side broken pipe also does not establish why the connection was closed. It records the first failed write after the peer was no longer accepting data. The peer may have closed because of a write deadline, slow-reader handling, daemon live update, protocol failure, or another connection-level error. That close reason still needs correlation from the daemon side.

## 2. Primary decision

**Unix socket connections must be disposable. Terminal sessions must not be.**

A transport error should replace only the physical TUI-to-daemon connection. It must not:

- Close the terminal.
- Restart the daemon.
- Restart a terminal actor or worker.
- Discard the TUI’s terminal layout.
- Permanently disable future requests.
- Convert a transport failure into a misleading Lima or shell-start failure.

The automatic recovery path should perform the same logical operation as quitting and reopening the frozen TUI, without destroying the TUI process or its local presentation state.

## 3. Immediate target architecture

```text
TUI application and cached view model
                 │
                 ▼
      Client-session supervisor actor
         │                   │
         │             Desired state
         │       subscriptions, focus, size
         │
         └── replaceable physical connection
                    │
                    ▼
          Daemon connection session
                    │
          ┌─────────┴─────────┐
          ▼                   ▼
 terminal registry     cached snapshots
          │
          ▼
 existing terminal actors and PTYs
```

The new component is the **client-session supervisor**. The logical client session remains alive while physical Unix connections come and go.

### Required client invariants

1. Exactly one goroutine owns mutable connection state.
2. The TUI update/render loop never directly reads or writes a socket.
3. Each physical connection has one reader and one writer goroutine.
4. Every physical connection receives a monotonically increasing local connection generation.
5. Reader, writer, dial, and handshake results are tagged with that generation.
6. Results from obsolete generations are ignored.
7. Any read or write error tears down the entire physical connection.
8. Reconnection does not restart the daemon or any terminal.
9. The last cached terminal screen remains visible while disconnected.
10. Non-idempotent operations are never silently replayed.
11. Every queue is bounded.
12. A locally slow TUI consumer causes resynchronization, not socket-reader blockage.

## 4. Client connection state machine

```text
Disconnected
     │
     ▼
   Dialing
     │
     ▼
Authenticating
     │
     ▼
 Resynchronizing
     │
     ▼
    Ready
```

Any failure from `Dialing`, `Authenticating`, `Resynchronizing`, or `Ready` transitions back to `Disconnected`.

`Stopping` is a separate terminal state entered only when the TUI is explicitly exiting.

### Failure behavior

When either connection goroutine reports an error:

1. Verify that the reported generation is still current.
2. Cancel the connection context with the original cause.
3. Close the socket exactly once.
4. Fail or classify all in-flight operations.
5. Keep terminal models and attachment intent.
6. Display cached screens with a reconnecting indicator.
7. Attempt an immediate reconnect.
8. Use capped exponential backoff with jitter after repeated failures.
9. Reset the backoff only after authentication and state synchronization succeed.

No daemon health conclusion should be drawn solely from the failed connection. The new dial is the relevant reachability test.

### Suggested Go ownership shape

```go
type ConnectionState uint8

const (
	ConnectionDisconnected ConnectionState = iota
	ConnectionDialing
	ConnectionAuthenticating
	ConnectionResynchronizing
	ConnectionReady
	ConnectionStopping
)

type ActiveConnection struct {
	Generation uint64
	Connection net.Conn
	Cancel     context.CancelCauseFunc
	Outbound   chan Frame
	Server     ServerIdentity
}

type ClientSession struct {
	// Owned only by the supervisor actor.
	state      ConnectionState
	nextGen    uint64
	active     *ActiveConnection
	desired    DesiredClientState
	pending    map[uint64]*PendingOperation
	retryTimer *time.Timer

	commands chan ClientCommand
	ioEvents chan ConnectionEvent
}
```

Dialing and authentication should run outside the supervisor actor. Their results return through `ioEvents`, tagged with the attempted generation. The supervisor should never hold a mutex while dialing, waiting, writing, or performing a handshake.

## 5. Authoritative reconnect and resynchronization

Reconnection must not assume that events received before the disconnect represent current state. Events may have been lost after either side decided the connection was dead.

After authentication, the client performs a full authoritative synchronization before enabling terminal input.

### Recommended identities

Use distinct identities for distinct lifetimes:

```go
type ServerIdentity struct {
	Epoch string // Random value for one daemon incarnation.
}

type ConnectionIdentity struct {
	ClientInstanceID string // One TUI process.
	ConnectionID     uint64 // Assigned by daemon.
}

type TerminalIdentity struct {
	TerminalID string
	Generation uint64
}
```

Also maintain a daemon-wide or registry-wide monotonic revision within each server epoch.

Terminal generation is not a substitute for server epoch or connection identity. They protect different replacement boundaries.

### Consistent synchronization cut

The daemon should register the new event subscription and capture the synchronization revision atomically, without performing I/O while holding the registry lock.

A bounded synchronization stream can then be:

```text
SyncBegin {
    server epoch,
    registry revision
}

TerminalState { terminal A metadata and cached snapshot }
TerminalState { terminal B metadata and cached snapshot }
TerminalState { terminal C metadata and cached snapshot }

SyncEnd {
    registry revision
}

Events with revision greater than the synchronization revision
```

Sending one terminal per frame avoids violating the existing 1 MiB frame limit.

The daemon must use cached immutable snapshots for this operation. It must not synchronously invoke Ghostty once per terminal while a reconnecting client waits.

If an event sequence gap, queue overflow, or epoch mismatch is observed, the client repeats the full synchronization instead of guessing what was missed.

An event replay log is not required for the first implementation. A full synchronization is simpler and establishes a stronger correctness baseline.

## 6. Operation delivery semantics

Operations need explicit delivery classes.

| Class | Examples | Reconnect behavior |
|---|---|---|
| Read-only query | status, terminal list, cached snapshot | Retry after synchronization |
| Replaceable state | resize, focus, subscription set, desired viewport | Keep only the latest value and reassert it |
| At-most-once mutation | keyboard input, paste, terminal-generated response | Never automatically replay after an uncertain result |
| Idempotent keyed mutation | close by terminal ID, create using a client-generated terminal ID | Retry only with the same operation ID and daemon deduplication |

For an at-most-once operation, distinguish:

```go
type DeliveryOutcome uint8

const (
	DeliveryNotSent DeliveryOutcome = iota
	DeliveryOutcomeUnknown
	DeliveryAcknowledged
)
```

If a frame was never handed to the writer, the operation was not sent. If the write may have reached the daemon but the response was lost, the result is uncertain.

The UI should report a transport-specific error such as:

> Connection lost before CodeLima confirmed the request; reconnecting.

It should not report that Lima shell startup failed when the request never reached shell startup.

For ordinary terminal typing, avoid one notification per key. Aggregate uncertain or dropped input into one connection-status message.

## 7. Input ownership across reconnects

If CodeLima has an input-owner concept, physical connection cleanup must not race with replacement.

A lease should include at least:

```go
type InputLease struct {
	ClientInstanceID string
	ConnectionID     uint64
	LeaseGeneration  uint64
}
```

When a new connection successfully resumes the same logical client:

1. Atomically transfer or reacquire the lease.
2. Update the lease’s connection ID.
3. Close the old physical connection.
4. Allow old-connection cleanup to release the lease only when its connection ID still matches.

This prevents delayed cleanup from an obsolete connection from revoking ownership held by its replacement.

A short reconnect grace period can preserve input ownership, but it should not be required for terminal survival. After the grace period, another client may acquire the lease according to the existing ownership policy.

## 8. Daemon connection hardening

Automatic client reconnect must be paired with daemon-side client isolation.

### Per-client outbound pump

`Server.Broadcast` should never write directly to client sockets. It should:

1. Copy or snapshot the current client-session references under a lock.
2. Release the lock.
3. Attempt a nonblocking enqueue to each client’s bounded outbound queue.
4. Disconnect only the client whose queue is full.

A slow client is disposable because the client can reconnect and perform a full synchronization.

### One socket writer

Each connection must have exactly one writer goroutine. Responses, events, pongs, and shutdown frames all pass through it.

It is useful to separate:

- A small high-priority queue for responses and protocol control.
- A bounded/coalescing event queue for snapshots and notifications.

A response should not sit behind an unlimited stream of snapshot events.

### Deadlines

The daemon currently gives event broadcasts a write deadline but not ordinary RPC responses. All frame writes need deadlines.

Every request also needs a bounded handler context. Keeping sequential request processing on a single connection is acceptable initially when:

- Every handler has a deadline.
- Snapshot reads are cached.
- Terminal actor submission is bounded.
- A stuck request can affect only that physical client connection.

A partial frame write followed by an error makes that connection unusable. Do not attempt to continue framing on it.

### Client teardown

Connection teardown may:

- Cancel connection-scoped request contexts.
- Remove subscriptions.
- Release or transfer connection-scoped leases.
- Record a close reason.

It must not implicitly call `terminal.close`.

Terminal close must remain an explicit authenticated terminal operation.

## 9. Preventing a slow TUI from causing its own disconnect

The daemon may have closed the connection because the TUI stopped reading, but the current capture does not establish that. The client should nevertheless be hardened against this class.

The socket reader must not block while the TUI performs rendering or processes a large model update.

For high-frequency snapshots, use a latest-value mailbox:

```text
socket reader
    → replace latest immutable snapshot
    → nonblocking notification to UI
```

If several snapshots arrive before the UI renders, only the newest one needs to be rendered.

For important state transitions, the supervisor should update its authoritative local model before notifying the UI. A missed UI notification is then harmless because the next render reads current state.

If the client-side inbound queue nevertheless overflows, deliberately close the connection and resynchronize. Do not stop reading indefinitely and wait for the daemon’s write deadline to fire.

## 10. Connection health and terminal health are different

Maintain separate health dimensions:

```text
Daemon reachability
Client transport connection
Terminal session and shell
Renderer or Ghostty state
Snapshot freshness
```

A client transport heartbeat should pass through the normal connection reader and writer. It tests only that transport and daemon connection handling are making progress.

A renderer health probe, if renderer workers are later introduced, must pass through the same actor that invokes Ghostty. It tests a different boundary.

The UI should be able to display:

- Reconnecting to daemon.
- Connected; terminal snapshot stale.
- Terminal renderer restarting.
- Terminal session exited.
- Daemon unavailable.

A single generic “frozen” state hides the actual recovery action.

## 11. Required observability

Assign and log a connection ID on both ends. At minimum, record:

```text
server_epoch
client_instance_id
connection_id
local_connection_generation
operation/request_id
terminal_id
terminal_generation
connection_phase
close_initiator
close_reason
last_successful_read
last_successful_write
outbound_queue_depth
inbound_queue_depth
underlying_error
```

Daemon close reasons should distinguish:

```text
peer EOF
read deadline
write deadline
slow outbound consumer
protocol violation
authentication failure
daemon shutdown
live update
superseded connection
local administrative close
```

This allows a client-side broken pipe to be correlated with the daemon event that preceded it.

The current capture proves where progress stopped. It does not yet prove which side initiated the close or why.

## 12. Revised native-code isolation architecture

The captured incident should not trigger an immediate migration to two subprocesses per terminal. It should trigger reconnect work.

The Ghostty risk should remain a separate architectural decision.

### Minimum architecture for Ghostty isolation with shell preservation

The simplest target that isolates cgo while preserving existing PTY lifecycle is:

```text
TUI reconnecting client
          │
          ▼
Go daemon, no Ghostty/cgo
          │
          ├── Terminal A session actor
          │       └── PTY and shell
          │
          └── Terminal A renderer process
                  └── Ghostty/cgo
```

In this design, the daemon remains responsible for PTY I/O through the existing Go terminal actor. It is not a pure control plane, but it is cgo-free.

This has several advantages over the proposed session-worker-plus-renderer-worker design:

- Renderer termination preserves the shell and PTY.
- Existing child wait and process-group ownership remain intact.
- Existing authenticated live-update PTY handoff can be retained.
- No PTY escrow is required for renderer recovery.
- No session-worker adoption protocol is required.
- There is only one additional process per terminal.
- The process-global Ghostty stderr mutex becomes terminal-local because each renderer process contains one terminal.

The session actor must forward PTY output to the renderer through a bounded nonblocking queue. If the renderer stops consuming, the actor disconnects and replaces it while continuing to drain the PTY into a bounded journal.

This is the minimum architecture that both:

1. Guarantees a stuck Ghostty call cannot directly block another terminal’s Ghostty calls.
2. Preserves the shell when the renderer is killed.

A separate session process should be introduced only if daemon failure isolation becomes a demonstrated requirement.

## 13. PTY escrow assessment

PTY escrow can preserve an open master descriptor, but it does not provide complete session-worker recovery.

Descriptor duplication and `SCM_RIGHTS` transfer preserve a reference to the same underlying open file description. This creates important consequences:

- File-status flags such as nonblocking behavior can be shared.
- Keeping the escrow descriptor open prevents master-side final closure.
- The daemon must never accidentally read from the escrow descriptor.
- Every validation failure must close received descriptors.
- Close-on-exec must be handled carefully.
- Terminal teardown must close the escrow descriptor exactly once.

The larger limitation is process parenthood.

If a session worker spawned the shell and then dies:

- A replacement process does not become the shell’s parent.
- It generally cannot use the original child-wait contract.
- It may still be able to signal a known process group, but lifecycle supervision is no longer equivalent.
- PID reuse and stale process-group metadata become additional risks.
- Portable daemon adoption of child wait responsibility is not available merely by passing the PTY descriptor.

Therefore:

**PTY escrow is not sufficient to claim transparent session-worker recovery.**

The safer initial decision is:

- Keep PTY and child lifecycle in the daemon’s existing Go actor.
- Isolate only the renderer.
- Retain descriptor transfer for planned daemon live update.
- Do not add crash-recovery escrow until its lifecycle contract is separately proven.

## 14. Renderer communication and recovery

With the terminal session actor remaining in the daemon, the daemon can create an inherited socket pair for each renderer.

The data flow becomes:

```text
PTY output
   → terminal session actor
   → bounded journal
   → renderer socket
   → Ghostty
   → immutable snapshot
   → daemon snapshot cache
```

Input becomes:

```text
client semantic input
   → daemon
   → renderer
   → encoded bytes or terminal response
   → terminal session actor
   → ordered PTY write queue
```

The renderer must never possess the only PTY master descriptor.

### Recovery limitation

Without complete Ghostty state export and import, exact renderer restoration cannot be promised.

A replacement renderer can receive:

- Initial terminal geometry.
- Available raw output journal.
- Current focus and configuration.
- Subsequent PTY output.
- A resize event after replay.

This may reconstruct the visible state sufficiently, but it cannot guarantee exact historical scrollback or every obscure emulator mode after journal truncation.

The daemon should retain the previous immutable snapshot while recovery occurs and mark it:

```text
stale
renderer restarting
partial replay available
```

Do not inject keystrokes such as redraw commands automatically. Sending a resize signal after renderer attachment is reasonable; injecting terminal input could alter a running application.

## 15. The Ghostty stderr drain should still be fixed

Even though it was not implicated in this capture, the current scanner behavior is a real avoidable risk.

The drain must continue consuming bytes regardless of:

- Newline presence.
- Individual message length.
- Logging truncation policy.

Use a continuously draining byte-copy loop and apply truncation or rate limiting in the destination. A logging limit must never cause the pipe reader to exit while Ghostty may still write to the pipe.

That fix does not eliminate the process-global mutex hazard. Per-terminal renderer processes provide the isolation guarantee.

## 16. Revised rollout

### Phase 0: Classify connection failures

Add connection IDs and paired close-reason logging. Add a deterministic daemon test hook that closes a selected TUI connection without touching terminals.

Preserve the existing diagnostic capture flow for suspected actor or cgo freezes.

### Phase 1: Resumable TUI client sessions

Implement:

- Client-session supervisor actor.
- Per-connection generations.
- Dedicated reader and writer goroutines.
- Automatic authentication and reconnect.
- Cached screen display while disconnected.
- Explicit delivery outcome classification.
- No automatic replay of terminal input.
- Authoritative full synchronization before input resumes.

This phase directly fixes the verified incident.

### Phase 2: Isolate clients inside the daemon

Implement:

- Per-client bounded outbound pumps.
- Write deadlines for RPC responses.
- Bounded request contexts.
- Slow-client disconnection.
- Event revisions and resynchronization on gaps.
- Generation-safe lease cleanup.

This prevents one broken or non-reading TUI from delaying another.

### Phase 3: Make reads asynchronous

Ensure terminal list and screen reads use daemon-owned immutable state:

- Cached snapshots.
- Snapshot generation and timestamp.
- Stale/degraded status.
- No synchronous Ghostty call in a normal client read path.

### Phase 4: Harden the current native boundary

Implement:

- A drain that survives arbitrarily long non-newline stderr output.
- Deterministic injectable cgo hangs.
- Actor request deadlines.
- Bounded actor submission queues.
- Diagnostics that distinguish actor queue delay from active cgo delay.

### Phase 5: Extract per-terminal renderer processes

Move only Ghostty into disposable per-terminal renderer workers. Keep PTY, shell, child wait, and live-update behavior in the daemon’s Go session actor.

Do not introduce a separate session worker or PTY escrow in this phase.

### Phase 6: Reconsider session guardians only with evidence

Daemon-crash worker adoption, orphan grace periods, and independent session guardians should remain a separate project. They do not solve the captured TUI disconnection and introduce split-brain, authentication, child-lifecycle, and update-coordination complexity.

## 17. Required tests

The first acceptance test should reproduce the exact captured failure class:

```text
1. Open a terminal and record terminal ID, generation, and shell PID.
2. Force-close only that TUI’s daemon connection.
3. Keep the TUI process running.
4. Confirm the TUI enters reconnecting state.
5. Confirm other clients remain responsive.
6. Confirm the TUI reconnects and performs a full synchronization.
7. Confirm terminal ID, generation, and shell PID are unchanged.
8. Confirm new terminal input works.
```

Additional automated tests should include:

- A connection failure while no request is active.
- A connection failure before an operation is written.
- A failure after a mutation frame is written but before its response.
- No automatic replay of uncertain terminal input.
- Latest resize and focus state reasserted after reconnect.
- A delayed event from an obsolete connection generation is ignored.
- Old connection cleanup cannot release a new connection’s input lease.
- A sequence gap causes full synchronization.
- A synchronization stream spanning multiple bounded frames.
- A TUI that stops reading is disconnected without delaying another client.
- A TUI resumed after `SIGSTOP` reconnects and reattaches.
- A daemon live update drops and reestablishes client connections without terminal loss.
- Ordinary RPC response writes time out for a non-reading client.
- Client and daemon queue bounds remain enforced under snapshot floods.
- Connection close, reader error, writer error, and explicit shutdown can race without double close.
- A greater-than-1-MiB newline-free Ghostty stderr stream remains continuously drained.
- Race testing and the existing project verification targets pass.

When renderer workers are added:

- A permanently hung renderer for Terminal A does not delay Terminal B or daemon status.
- Terminal A’s shell PID survives renderer replacement.
- A renderer that stops consuming cannot stop PTY draining.
- Messages from an obsolete renderer generation are rejected.
- A renderer restart storm degrades only its terminal.

## 18. Direct answers to the review questions

1. **Is the Ghostty mutex sufficient to explain system-wide freezes?**  
   It is sufficient as a theoretical process-wide failure mechanism, but it does not explain the captured incident. Maintain it as a separate fault class.

2. **Combined process per terminal or session/renderer split?**  
   Neither is required for the verified client failure. For later cgo isolation, a renderer-only process with the PTY session actor remaining in the daemon is the better minimum design.

3. **Is PTY escrow safe and worthwhile?**  
   It can preserve a descriptor but does not preserve parent-child wait semantics. It is not worthwhile for the first renderer-isolation design.

4. **Direct session-to-renderer communication or through the daemon?**  
   Keep the session actor in the daemon and connect it directly to its renderer over a private socket pair. Do not route high-volume output through a separate generic daemon RPC hub.

5. **How should exact renderer recovery work?**  
   It cannot be guaranteed without complete emulator-state serialization or an unbounded history. Provide bounded journal replay and explicitly report partial recovery.

6. **Cached asynchronous snapshots or request/reply snapshots?**  
   Cached immutable snapshots are preferable for normal reads and reconnect synchronization. They turn a renderer failure into stale data rather than a blocked daemon request.

7. **Are generation IDs and at-most-once semantics sufficient?**  
   Not alone. Add server epoch, physical connection ID, client instance ID, event revision, operation ID, and generation-safe lease cleanup.

8. **Minimum architecture guaranteeing one hung cgo call cannot affect another terminal?**  
   One Ghostty renderer process per terminal, with no Ghostty calls in the daemon.

9. **Does process-per-terminal create unacceptable overhead?**  
   This must be measured rather than assumed. Measure proportional set size, startup latency, descriptor count, process count, and packaging behavior. Do not pool unrelated terminals into one renderer merely to reduce process count, because that restores the shared blast radius.

10. **Should daemon-crash worker adoption be implemented?**  
    No, not as part of this work. Preserve the current live-update and persisted-respawn ceiling until daemon-crash survival is independently justified.

11. **Which Unix edge cases need attention?**  
    Partial framed writes, half-closed sockets, stale connection cleanup, descriptor close-on-exec, control-message truncation, shared descriptor status flags, process-group identity, PID reuse, child wait ownership, and races between handoff and teardown.

12. **What is overengineered relative to the captured failure?**  
    The session-worker process, PTY escrow, worker adoption, and daemon-crash survival are overengineered as immediate remedies. A reconnecting logical client session, per-client daemon pumps, and cached synchronization directly address the verified failure.

## 19. Recommended ADR split

Use two separate decisions rather than one large terminal-worker ADR:

### ADR A: Treat daemon client connections as disposable and resumable

Decision:

- A TUI maintains a logical client session across physical socket failures.
- Reconnection authenticates, performs authoritative synchronization, and reasserts replaceable state.
- Terminal input is not automatically replayed after uncertain delivery.
- Client connection loss never closes daemon-owned terminal sessions.

### ADR B: Isolate Ghostty in per-terminal renderer processes

Decision:

- The daemon remains Go-only and owns PTY/session lifecycle.
- Each terminal has one disposable Ghostty renderer process.
- Renderer queues and journals are bounded.
- Renderer replacement preserves the shell but provides explicitly partial emulator-state recovery.

This separates the **verified production fix** from the **defense against a still-credible native-code failure mode**.

# Recommended decision

The proposed direction is correct, with several important changes.

**Adopt a cgo-free control-plane daemon, one cgo-free session worker per terminal, and one disposable Ghostty renderer process per terminal.** The daemon must answer status and screen-read requests exclusively from cached state and must never synchronously wait for Ghostty.

This gives a precise guarantee:

> A hung Ghostty call can make one terminal stale or temporarily unavailable, but it cannot block the daemon, another terminal, or another client connection.

The architecture cannot guarantee against whole-host resource exhaustion or kernel failures, but it removes the credible process-wide cgo failure domain currently matching the incident pattern.

## 1. Root-cause assessment

The process-global stderr mutex is sufficient to explain the observed system-wide freeze.

The failure chain does not require the stderr scanner hypothesis:

1. Terminal A acquires `ghosttyStderr.mu`.
2. Terminal A enters any Ghostty operation that never returns.
3. The mutex remains locked.
4. Every other terminal eventually attempts a Ghostty operation and blocks on that mutex.
5. Their synchronous callers wait forever because the actors are alive and `actorDone` does not close.
6. The daemon process remains alive while terminal progress stops.

The `bufio.Scanner` behavior supplies a credible mechanism for step 2. A scanner stops unrecoverably when a token exceeds its configured maximum. Thus, a long stderr record without a newline can stop the drain; once the pipe fills, Ghostty can block while writing to fd 2. 

The other shared boundaries are still defects worth fixing, but they are less consistent with a truly indefinite cross-terminal freeze:

- Sequential event writes create cumulative bounded delay.
- An RPC response without a write deadline can wedge a connection writer.
- Synchronous close can wedge shutdown.
- Sequential request processing can wedge one client connection.

They amplify the incident. The global Ghostty mutex directly explains it.

## 2. Target process architecture

```text
TUI / CLI clients
        │
        │ framed Unix RPC
        ▼
┌──────────────────────────────────────────────┐
│ codelimad                                    │
│ CGO_ENABLED=0                                │
│                                              │
│  terminal registry                          │
│  per-terminal supervisor actors              │
│  immutable snapshot caches                  │
│  worker generations and restart budgets     │
│  per-client bounded outbound pumps          │
└──────────────┬───────────────────────────────┘
               │
        Terminal A supervisor
               │
       ┌───────┴────────┐
       │                │
       ▼                ▼
┌──────────────┐  ┌─────────────────┐
│ session-A    │  │ renderer-A      │
│ CGO_ENABLED=0│  │ Ghostty + cgo   │
│              │  │ disposable      │
│ PTY + shell  │  │ no PTY fd       │
│ journal      │  │ one Ghostty     │
│ write queue  │  │ actor           │
└──────┬───────┘  └────────┬────────┘
       │ direct framed data │
       └────────────────────┘
```

There should be three logical links:

| Link | Traffic |
|---|---|
| Daemon ↔ session | Lifecycle, resize requests, status, attach/fence operations |
| Daemon ↔ renderer | Semantic input, health probes, snapshots, lifecycle |
| Session ↔ renderer | Ordered terminal-output events, encoded input, terminal responses, progress acknowledgements |

The high-volume PTY stream should **not** pass through the daemon.

## 3. Process responsibilities

### Control-plane daemon

The daemon owns:

- Durable terminal identity and ordering
- A per-terminal supervisor actor
- Session and renderer generations
- Worker process handles and restart policy
- Cached immutable snapshots and status
- Client RPC and event routing
- Persisted launch intent
- Cooperative live-update orchestration

It must not:

- Import the cgo renderer package
- Link Ghostty into its executable
- Own or perform PTY I/O
- Block on worker IPC
- Call `Process.Wait` in a supervisor actor
- Write directly to a client socket from a broadcast loop
- Hold registry locks while communicating with anything

A single executable with `daemon` and `renderer` subcommands does not satisfy the strongest version of this boundary because the daemon executable still links the cgo dependency. Use separate binaries:

```text
codelimad                    CGO_ENABLED=0
codelima-session-worker      CGO_ENABLED=0
codelima-renderer-worker     CGO_ENABLED=1
```

### Session worker

The session worker owns the only active PTY master used for I/O.

It owns:

- Shell creation and child lifecycle
- PTY reads
- PTY writes
- Resize ioctl and signal ordering
- A bounded journal of renderer-relevant events
- Output sequencing
- A bounded, ordered PTY-write queue
- Renderer attachment and generation fencing
- Dedupe of renderer-originated writes

It must continue draining the PTY whether or not a renderer is connected.

### Renderer worker

The renderer owns:

- Exactly one Ghostty instance
- Exactly one actor that invokes Ghostty
- Terminal-output application
- Input encoding
- Terminal-response generation
- Snapshot construction
- Renderer progress acknowledgements

The renderer should own **no PTY descriptor at all**, not merely “not the final reference.” That makes accidental PTY reads, writes, closes, and flag changes impossible.

## 4. Required changes to the proposed design

### 4.1 Use role-specific generations

A single `WorkerGeneration` is ambiguous once session and renderer workers can restart independently.

Use at least:

```go
type Generations struct {
    TerminalEpoch      uint64
    SessionGeneration  uint64
    RendererGeneration uint64
    AttachmentID       uint64
}
```

`AttachmentID` identifies one particular direct session-renderer connection. It prevents frames buffered on an old connection from being accepted after a renderer replacement.

A worker connection is bound to its role and generation during the initial handshake. Envelope fields are then validation data, not the source of authority.

### 4.2 Separate transport IDs from mutation IDs

`RequestID` is only for matching a transport response. It is not enough for at-most-once semantics.

Use three concepts:

```go
type RequestID uint64 // one RPC/link request

type MutationID struct {
    TerminalEpoch uint64
    Counter       uint64
}

type RendererSideEffectID struct {
    SessionGeneration uint64
    JournalSequence   uint64
    Ordinal           uint32
}
```

`MutationID` identifies non-idempotent user input such as keystrokes, mouse events, and paste.

`RendererSideEffectID` identifies Ghostty-generated writes such as terminal query responses. The session worker deduplicates these across renderer restarts.

This distinction is important during output replay. Replaying a terminal query through a replacement renderer can otherwise send a response to the shell a second time.

### 4.3 Journal render events, not only output bytes

Terminal state depends on more than PTY output. Resize history can affect wrapping and reflow.

The session journal should contain an ordered stream such as:

```go
type JournalEntry struct {
    Sequence uint64
    Kind     JournalKind

    Output []byte
    Size   *TerminalSize
}
```

At minimum, journal:

- PTY output chunks
- Resize events at their exact ordering point
- Terminal initialization or configuration changes that affect emulation

Raw output replay without resize history cannot claim exact restoration.

### 4.4 Pull asynchronous snapshots into the first isolation release

Cached snapshots should not wait until a later phase.

Moving Ghostty into another process but continuing to synchronously request snapshots leaves client paths dependent on a worker timeout. The first worker-based release should make these operations local daemon reads:

- `terminal.read`
- `terminal.snapshot`
- `terminal.status`
- terminal listing and ordering

A snapshot should include:

```go
type PublishedSnapshot struct {
    TerminalID          string
    TerminalEpoch       uint64
    SessionGeneration   uint64
    RendererGeneration  uint64
    JournalSequence     uint64
    OutputByteSequence  uint64
    ProducedAt          time.Time
    State               TerminalState
    Recovery            RecoveryFidelity
    Payload             []byte // immutable after publication
}
```

Store it with an `atomic.Pointer` or a very small cache mutex. The payload must become immutable before publication.

### 4.5 Use JSON for control, not the data plane

Typed JSON is reasonable for low-rate control messages. It is a poor encoding for raw output because `[]byte` becomes base64 and grows significantly.

Use:

- A fixed binary frame header
- JSON for small control payloads
- Raw bytes for PTY output and encoded input
- Binary or chunked encoding for snapshots

A 1 MiB **frame** cap is sensible. A complete large snapshot may exceed 1 MiB, so support a separately bounded logical message:

```text
SnapshotBegin
SnapshotChunk × N
SnapshotEnd
```

Validate the total announced size before allocating.

### 4.6 Remove per-call stderr redirection

Inside each renderer process:

1. Redirect fd 2 once during startup, before creating Ghostty.
2. Drain it continuously with `io.CopyBuffer`, not `bufio.Scanner`.
3. Retain only a bounded tail for diagnostics.
4. Never synchronously forward every stderr write into a potentially blocking global logger.

There is then no per-call stderr mutex. A pathological stderr writer can at worst hang its own renderer and trigger its watchdog.

## 5. Output flow and backpressure

The session worker should be the authoritative output sequencer.

```text
PTY read
  → session actor
  → assign journal sequence and byte sequence
  → append to bounded journal
  → offer data to renderer under credit window
  → continue reading PTY regardless of renderer state
```

A plain buffered channel is not enough for replay and prolonged backpressure. Use a bounded credit window:

```text
sent-through - acknowledged-through <= output-window
```

The renderer acknowledges output only after its Ghostty actor has applied it.

When the credit window is exhausted:

- The session stops sending to that renderer.
- It continues reading the PTY into the journal.
- If the renderer misses its health deadline, the daemon replaces it.
- If the journal evicts data that the renderer still needs, the attachment is declared unrecoverable and disconnected.

The PTY reader must never wait for:

- A renderer socket write
- A renderer acknowledgement
- A daemon RPC
- A client event
- Snapshot generation

## 6. Input and terminal-response semantics

### User input

```text
client semantic event
  → daemon supervisor assigns MutationID
  → renderer actor encodes through Ghostty
  → session worker validates renderer generation
  → session deduplicates MutationID
  → ordered PTY write
  → acknowledgement
```

A success response should mean the session accepted the mutation into its authoritative PTY-write path. Whether it acknowledges after enqueue or after complete PTY write should be an explicit contract.

If a deadline expires after the command was enqueued, return an uncertain result:

```go
type MutationOutcome uint8

const (
    OutcomeNotAccepted MutationOutcome = iota
    OutcomeAccepted
    OutcomeUnknown
)
```

Do not automatically retry `OutcomeUnknown`.

### Idempotent operations

Not every mutation needs at-most-once treatment.

| Operation | Semantics |
|---|---|
| Keyboard, paste, mouse button/delta | At most once; no automatic retry |
| Absolute resize | Latest value wins; retry/coalesce is safe |
| Absolute focus state | Latest value wins |
| Relative scroll | At most once, or redesign as an absolute viewport |
| Close | Idempotent state transition |
| Snapshot/status | Cached local read |

### Renderer-generated terminal responses

When processing a journal entry, the renderer tags each Ghostty-generated PTY response with:

```text
(session generation, journal sequence, response ordinal)
```

The session keeps a bounded committed high-water mark or dedupe window. During renderer replay:

- Responses already accepted from the old renderer are discarded.
- Responses from previously unprocessed journal entries are accepted.
- Duplicate shell input is avoided even when the old renderer died between sending a response and reporting progress.

Without this rule, raw output replay can produce duplicate terminal query responses.

## 7. Renderer health

The renderer should have one Ghostty-owning actor:

```text
control reader ─┐
session reader ─┼─→ Ghostty actor ─→ response/snapshot publishers
timers ─────────┘
```

Health probes must enter that actor. A separate goroutine must not answer them.

The probe need not perform an elaborate Ghostty operation. Successfully reaching the actor after all previous work proves that the actor is not stuck in an earlier cgo call. If Ghostty offers a documented cheap state operation, the probe may invoke it.

The health response should include:

- Current renderer generation
- Last applied journal sequence
- Oldest queued event age
- Actor queue depths
- Last completed Ghostty operation
- Snapshot publication age

If Ghostty requires operating-system-thread affinity, the actor can call `runtime.LockOSThread` once at startup; that function pins the goroutine to its current OS thread. This should only be enabled if Ghostty’s API contract actually requires it. 

## 8. Renderer replacement sequence

A safe replacement sequence is:

1. Health deadline expires.
2. Supervisor marks the current snapshot stale and terminal state `Recovering`.
3. Supervisor increments `RendererGeneration`.
4. Supervisor tells the session to fence the old generation.
5. Session closes or detaches the old renderer link and rejects future side effects from it.
6. Daemon sends graceful termination to the old renderer.
7. After a bounded grace period, daemon sends `SIGKILL`.
8. A reaper goroutine waits for process exit; the supervisor actor does not wait.
9. Daemon creates a new session-renderer socket pair.
10. One endpoint is passed to the session in a prepare operation.
11. The other endpoint is inherited by the new renderer.
12. Renderer and session complete a two-phase attach handshake.
13. Session replays the available journal under output credit.
14. Renderer catches up to the session’s current journal head.
15. Renderer publishes its first current snapshot.
16. Daemon atomically replaces the stale cached snapshot and marks the terminal `Running`.

Input should be rejected, rather than queued, while the renderer is replaying. Encoding input against incomplete terminal state can be incorrect.

The shell PID, PTY, and session generation remain unchanged throughout this sequence.

## 9. Recovery fidelity

There are only three honest recovery states:

```go
type RecoveryFidelity uint8

const (
    RecoveryExact RecoveryFidelity = iota
    RecoveryPartial
    RecoveryUnavailable
)
```

`RecoveryExact` is valid only when the replacement renderer receives every state-affecting event from terminal initialization onward, including resize history, with no journal gaps.

`RecoveryPartial` means:

- The shell and jobs survived.
- The last cached screen remained visible during replacement.
- The replacement consumed the retained journal.
- Scrollback, parser state, modes, reflow, or selection may differ.

`RecoveryUnavailable` means the renderer cannot be safely reconstructed and only manual restart or close is available.

Do not attempt to manufacture a new Ghostty state from a screen-cell snapshot. A visual frame does not contain parser state, modes, scrollback, hyperlink state, or all input-encoding state.

Ghostty currently describes `libghostty` as an unstable API, and its VT C header is explicitly marked incomplete and work in progress. Exact state transfer should therefore not depend on undocumented internal memory layouts. 

An optional same-size resize or `SIGWINCH` can encourage some full-screen applications to repaint after partial recovery, but it changes application behavior and should be an explicit policy rather than a hidden guarantee.

## 10. Supervisor implementation shape

Each terminal gets one supervisor goroutine.

```go
type Supervisor struct {
    commands chan command
    events   chan event
    done     chan struct{}

    snapshot atomic.Pointer[PublishedSnapshot]
    status   atomic.Pointer[PublishedStatus]
}
```

The actor owns:

- Worker states
- Generations
- Pending requests
- Restart budget
- Timers and backoff
- Attach state

It does not perform blocking work. Process startup, socket reads, socket writes, signals, and process waits occur in helpers that report results as events.

A mutation API can conservatively represent timeout uncertainty:

```go
func (s *Supervisor) Mutate(
    ctx context.Context,
    mutation Mutation,
) (MutationOutcome, error) {
    reply := make(chan mutationResult, 1)

    cmd := mutationCommand{
        Mutation: mutation,
        Reply:    reply,
    }

    select {
    case s.commands <- cmd:
        // Once enqueued, cancellation may no longer mean "not executed".
    case <-ctx.Done():
        return OutcomeNotAccepted, ctx.Err()
    case <-s.done:
        return OutcomeNotAccepted, ErrTerminalClosed
    }

    select {
    case result := <-reply:
        return result.Outcome, result.Err
    case <-ctx.Done():
        return OutcomeUnknown, ctx.Err()
    case <-s.done:
        return OutcomeUnknown, ErrTerminalClosed
    }
}
```

The reply channel is buffered so the actor never blocks when the caller has already timed out.

Snapshots are direct cache reads:

```go
func (s *Supervisor) Snapshot() (*PublishedSnapshot, bool) {
    snapshot := s.snapshot.Load()
    return snapshot, snapshot != nil
}
```

The returned object must be treated as immutable.

## 11. Worker link implementation

Each connection gets exactly:

- One reader goroutine
- One writer goroutine
- One bounded outbound queue
- Read and write deadlines
- A maximum decoded frame size

The supervisor interacts with a nonblocking link API:

```go
type Link interface {
    TrySend(Frame) error
    Close(error)
    Events() <-chan LinkEvent
}
```

`TrySend` should fail with backpressure rather than block the supervisor actor.

The writer owns all calls to `Write`. Go’s Unix connections support write deadlines, so every frame write can have a bounded completion time. 

For initial child connections, `exec.Cmd.ExtraFiles` maps entry `i` to child descriptor `3+i` on Unix systems. 

A useful package structure is:

```text
cmd/codelimad/
cmd/codelima-session-worker/
cmd/codelima-renderer-worker/

internal/terminal/supervisor/
internal/terminal/snapshot/
internal/sessionworker/
internal/rendererworker/
internal/workerproto/
internal/workerproc/
internal/unixfd/
internal/faultinject/
```

The cgo import must be reachable only from `cmd/codelima-renderer-worker`.

## 12. Client isolation

Worker isolation alone is insufficient if a slow TUI can block daemon event delivery.

Every client should have a dedicated outbound pump with:

- A bounded response queue
- Latest-wins snapshot/event slots
- A write deadline on every response and event
- Disconnect-on-overflow behavior

Broadcasting should only perform nonblocking enqueue operations:

```text
daemon broadcast
  → try-enqueue client A
  → try-enqueue client B
  → try-enqueue client C
```

It must never write a socket inline.

Responses should not be dropped. If a client cannot accept them within its queue and deadline, disconnect that client. Snapshot-change notifications and similar state events can be coalesced.

Client request processing may remain ordered for mutation requests, but cached queries should not be serialized behind a mutation waiting for a worker. A practical implementation has:

- An ordered mutation lane
- Concurrent bounded query dispatch
- One response writer keyed by RPC request ID

## 13. Restart policy

Use a terminal-local token bucket or rolling-window budget, for example:

```text
Immediate first restart
Exponential backoff with jitter
Maximum N restarts in a rolling interval
Budget reset after a sustained healthy period
```

When exhausted:

- Keep the session worker and shell alive.
- Mark the terminal `DegradedRenderer`.
- Keep the last snapshot visible and stale.
- Reject semantic input.
- Expose manual renderer restart.
- Continue operating every other terminal.

The budget is per terminal, never global.

## 14. PTY escrow recommendation

Do **not** implement daemon PTY escrow in the initial target architecture.

Escrow addresses a different failure: session-worker loss. The observed failure is a renderer/cgo liveness problem. The pure-Go session worker is deliberately small, making its failure substantially less likely than the current renderer failure.

Escrow is technically feasible but introduces substantial lifecycle risk. `SCM_RIGHTS` transfers a reference to the same open file description and is semantically equivalent to duplicating a descriptor into another process. Duplicates share file-status flags such as `O_NONBLOCK`; close-on-exec is a descriptor-local flag and must be set on every received descriptor. 

If escrow is added later, require all of these rules:

- Session configures `O_NONBLOCK` before escrow is created.
- The daemon never calls `read`, `write`, `ioctl`, `poll`, or `F_SETFL` on escrow.
- Every received descriptor gets `FD_CLOEXEC`.
- Only one process may read the PTY master at a time.
- Explicit terminal close closes every master duplicate.
- Descriptor leaks are tested because an extra master reference changes hangup timing.
- Session replacement is fast enough to prevent the PTY output buffer from blocking the shell.
- Escrow transfer during live update is authenticated and generation-fenced.

This deserves a separate ADR and fault-injection suite.

## 15. Live update

Cooperative live update should be preserved, but unexpected daemon-crash adoption should remain deferred.

Do not transfer an actively used worker stream to a new daemon and allow both daemons to hold readers simultaneously. Use a prepare/commit adoption protocol:

1. Old and new daemons authenticate using the existing handoff mechanism.
2. New daemon creates fresh worker control links.
3. Old daemon passes each new link to the appropriate worker.
4. Worker validates a new daemon epoch and capability token.
5. Worker prepares the new link but keeps the old link authoritative.
6. Old daemon quiesces terminal mutations.
7. Worker atomically commits the new daemon epoch.
8. New daemon confirms adoption.
9. Old daemon closes its links.

The direct session-renderer data connection is unaffected.

For an unexpected daemon crash in the initial release:

- Workers exit after control-link EOF or a bounded lease.
- Persisted respawn remains the documented ceiling.
- System service supervision restarts the daemon.
- No split-brain adoption protocol is needed.

## 16. Unix and process edge cases to cover

The implementation and tests should explicitly cover:

| Edge case | Required behavior |
|---|---|
| Shared file-status flags after `dup`/`SCM_RIGHTS` | No component independently toggles `O_NONBLOCK` |
| Descriptor-local `FD_CLOEXEC` | Set it after every receive |
| Two PTY master readers | Prohibited by handoff/fencing |
| Extra PTY master references | Explicit close ownership; no leaked hangup suppression |
| Partial PTY writes | Preserve byte order; return uncertain outcome after partial commit |
| `EINTR` and `EAGAIN` | Retry under bounded policy |
| Resize ordering | Session applies and journals resize before renderer observes it |
| Shell job control | Audit session, controlling TTY, foreground and background process groups |
| Renderer process groups | Separate from session and shell groups |
| Child exit versus watchdog kill | One `Wait`, one terminal state transition |
| Old worker output after replacement | Rejected by generation and attachment fencing |
| Stream descriptor passing | One framed handoff and one `recvmsg` owner |
| Daemon live update | Never two active readers on one worker control stream |
| Worker stderr flood | Token-independent continuous drain |
| Session replacement with escrow | PTY drain resumes before its kernel buffer fills |
| Platform PTY close behavior | Normalize macOS/Linux EOF and error differences in platform tests |

## 17. What is and is not overengineered

| Element | Assessment |
|---|---|
| Per-terminal Ghostty process | Minimum hard isolation boundary |
| Session/renderer split | Justified when shell and running jobs must survive |
| Cached asynchronous snapshots | Required for daemon liveness |
| Direct session-renderer data path | Required to keep daemon out of backpressure path |
| Role generations and attachment fencing | Required for safe replacement |
| Stable PTY-write IDs | Required to avoid duplicate input/responses |
| Bounded event journal | Required for PTY draining and replay |
| PTY escrow now | Premature |
| Unexpected daemon-crash adoption now | Premature |
| Exact Ghostty-state recreation without an API | Not a viable release requirement |
| JSON for all raw data | Too inefficient and creates unnecessary size pressure |
| A shared renderer pool | Violates the one-hung-call/one-terminal guarantee |

## 18. Revised rollout

### Phase 0: Evidence and immediate hardening

Implement immediately:

- Replace stderr `Scanner` with a continuous byte drain.
- Log Ghostty call start, completion, operation type, terminal ID, and elapsed time.
- Measure global Ghostty mutex wait time.
- Record actor last-progress timestamps and queue depth.
- Add deadlines to ordinary RPC response writes.
- Make close and daemon shutdown bounded.
- Add deterministic cgo-hang and stderr-flood fault modes.

This reduces incident frequency and should confirm the current hypothesis, but it is not the architectural fix.

### Phase 1: Per-terminal combined process plus asynchronous daemon

Move the current runtime into one cgo worker per terminal, while also adding:

- Supervisor actors
- Generation fencing
- Cached snapshots
- Bounded worker protocol
- Per-client outbound pumps
- Renderer watchdog and kill escalation

This is the minimum release that eliminates the global freeze blast radius. Killing the worker will still lose that terminal’s shell.

Shape the protocol around worker roles and independent generations now, so Phase 2 is not a rewrite.

### Phase 2: Split session and renderer

Add:

- Pure-Go session worker
- Direct session-renderer data link
- Ordered event journal
- Output credit and acknowledgements
- Renderer-side-effect IDs
- Renderer replacement while preserving shell PID
- Partial-recovery reporting

This is the target architecture for the reported user problem.

### Phase 3: Cooperative live update

Add worker control-link adoption with daemon epochs and prepare/commit fencing.

### Deferred work

Only after production evidence:

- PTY escrow
- Session-worker replacement preserving the shell
- Unexpected daemon-crash worker adoption
- Full renderer state import/export if Ghostty exposes a stable API

## 19. Required acceptance tests

The architecture should not be considered complete until these are deterministic automated tests:

1. Terminal A blocks forever inside an actual cgo helper.
2. Terminal B continues input, output, status, and snapshot operations within an explicit latency bound.
3. Daemon status remains responsive throughout.
4. Terminal A’s renderer generation increments and the old process is killed.
5. After the split, Terminal A’s session PID, shell PID, and PTY remain unchanged.
6. A renderer that stops reading cannot stop the session from draining PTY output.
7. Output larger than the journal cap keeps memory bounded and reports partial recovery.
8. A replacement renderer cannot duplicate terminal responses generated before the crash.
9. A timed-out input is never automatically replayed.
10. Old-generation snapshots, acknowledgements, and side effects are discarded.
11. A client that never reads is disconnected without delaying other clients.
12. Snapshot publication drops intermediate frames rather than accumulating them.
13. Renderer crash loops degrade only their terminal.
14. Shutdown, child exit, watchdog kill, and manual close can race without double wait or double close.
15. Oversized, truncated, and malformed frames terminate only the offending link or worker.
16. macOS and Linux PTY-close, resize, process-group, and descriptor-transfer tests pass.
17. Worker RSS, daemon RSS, queue sizes, journal bytes, and process counts remain within declared bounds.

The integration test should use an actual cgo function that never returns, not merely a blocked Go goroutine. That is what proves the process boundary.

## Answers to the reviewer questions

| Question | Answer |
|---|---|
| Is the global mutex sufficient? | Yes. A single non-returning call while holding it explains the complete symptom. The scanner is a plausible trigger, not a required part of the explanation. |
| Is a combined process enough? | Enough for cross-terminal isolation; not enough to preserve the affected shell. |
| Is the split justified? | Yes, when running jobs must survive renderer replacement. |
| Is PTY escrow worthwhile now? | No. Defer it until session-worker recovery becomes an evidenced requirement. |
| Direct session-renderer or via daemon? | Direct data plane; daemon control plane. |
| How should exact recovery work? | Only from a complete ordered event history or a future Ghostty state import API. Otherwise explicitly report partial recovery. |
| Cached snapshots? | Yes, mandatory. Normal client reads must never issue renderer RPCs. |
| Are generations and at-most-once semantics sufficient? | Necessary but not sufficient. Add role-specific generations, attachment fencing, semantic mutation IDs, and an explicit prepare/commit attach sequence. |
| Minimum architecture that isolates one hung cgo call? | One OS process per terminal containing every Ghostty call, plus bounded daemon IPC. |
| Process overhead unacceptable? | It must be measured with 1, 10, and expected-maximum terminal counts. Pooling renderers would weaken the required guarantee. |
| Daemon-crash adoption now? | No. Preserve cooperative live update and retain persisted respawn for unexpected crashes. |
| Missing Unix concerns? | Shared descriptor flags, final-master close semantics, single-reader ownership, controlling TTY/job-control groups, partial writes, close-on-exec, and two-controller handoff races. |
| What is overengineered? | Escrow, unexpected daemon adoption, and exact renderer restoration without a supported Ghostty state API. |

The ADR decision should therefore be: **isolate every Ghostty instance in a dedicated per-terminal renderer process; preserve the shell in a cgo-free session worker; make daemon reads asynchronous and cached; defer PTY escrow and unexpected-daemon adoption.**
