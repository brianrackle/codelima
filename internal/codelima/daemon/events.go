package daemon

import (
	"encoding/json"
	"time"
)

// Event names. The daemon package owns the wire protocol, so an event is
// spelled exactly once -- here -- and every publisher and subscriber refers to
// that spelling. Before this, names were string literals repeated at the emit
// site and again in each consumer's switch, which is the same drift hazard the
// method classification in dispatch.go was unified to remove: a typo on either
// side is silently "an event nobody listens for".
const (
	EventDaemonHeartbeat       = "daemon.heartbeat"
	EventDaemonShutdown        = "daemon.shutdown"
	EventDaemonUpdateStarting  = "daemon.update_starting"
	EventDaemonUpdateFailed    = "daemon.update_failed"
	EventDaemonUpdateCommitted = "daemon.update_committed"
	EventInputRevoked          = "input.revoked"
	EventNodeStatusChanged     = "node.status_changed"
	EventNodeUsageChanged      = "node.usage_changed"
	EventTargetTabsChanged     = "target.tabs_changed"
	EventTerminalClipboard     = "terminal.clipboard"
	EventTerminalClosed        = "terminal.closed"
	EventTerminalCreated       = "terminal.created"
	EventTerminalDirty         = "terminal.dirty"
	EventTerminalError         = "terminal.error"
	EventTerminalResized       = "terminal.resized"
)

// The payload types below replace ad-hoc map literals at the emit sites. Their
// field order is deliberate: encoding/json writes map keys in sorted order and
// struct fields in declaration order, so declaring each struct's fields in the
// sorted-key order of the map it replaces keeps the published bytes identical.
// events_test.go pins that byte-for-byte.
//
// Events whose payload is already a typed value -- terminal.created,
// terminal.closed and terminal.resized publish a TerminalState; node.status_changed
// publishes the parent package's Node record, which is a domain type and stays
// there -- keep publishing it, so there is still exactly one definition per event.

// TerminalDirtyEvent announces that a terminal published a new screen. It
// carries the sequence rather than the screen: a subscriber pulls the snapshot
// it wants through terminal.snapshot, which is served from the daemon's
// encode-once body cache.
type TerminalDirtyEvent struct {
	SnapshotSequence uint64 `json:"snapshot_sequence"`
	Stale            bool   `json:"stale"`
	TerminalID       string `json:"terminal_id"`
}

// TerminalErrorEvent reports a terminal-local failure that did not close the
// terminal (a failed snapshot, a renderer error frame).
type TerminalErrorEvent struct {
	Error      string `json:"error"`
	TerminalID string `json:"terminal_id"`
}

// TerminalClipboardEvent forwards an OSC 52 clipboard write from the guest
// program to whichever client owns the tab. TabID may be empty when the
// terminal has already been removed from the host's table.
type TerminalClipboardEvent struct {
	TabID      string `json:"tab_id"`
	TerminalID string `json:"terminal_id"`
	Text       string `json:"text"`
}

// TargetTabsChangedEvent announces that one target's tab set or tab order
// changed. It names only the target: tab lists are pulled with terminal.list.
type TargetTabsChangedEvent struct {
	Target string `json:"target"`
}

// DaemonUpdateFailedEvent reports that a live update rolled back, with the
// cause the operator needs to see.
type DaemonUpdateFailedEvent struct {
	Error string `json:"error"`
}

// DaemonUpdateCommittedEvent reports that a live update handed off successfully
// and names the replacement daemon process.
type DaemonUpdateCommittedEvent struct {
	PID int `json:"pid"`
}

// InputRevokedEvent tells the previous input owner that another client took the
// input lease.
type InputRevokedEvent struct {
	ClientID string `json:"client_id"`
}

// NodeUsageEvent is the payload of node.usage_changed: one node's live usage as
// of one sample. It carries exactly the fields a node.list reply merges into a
// node record, so a subscriber can apply a pushed sample and a polled one
// through the same path and pick the newer by SampledAt. Absent pointers mean
// "no reading", not "unchanged": the daemon publishes the whole sample every
// time, so a cleared reading clears the display.
type NodeUsageEvent struct {
	NodeID           string    `json:"node_id"`
	SampledAt        time.Time `json:"sampled_at"`
	CPUUsagePercent  *float64  `json:"cpu_usage_percent,omitempty"`
	MemoryUsedBytes  *uint64   `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes *uint64   `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes    *uint64   `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes   *uint64   `json:"disk_total_bytes,omitempty"`
}

// DecodeEventData is the single decode seam for broadcast payloads. Event.Data
// is `any` because the envelope is shared by every event, so the shape a
// subscriber gets back depends on how it arrived: the server pre-encodes to
// json.RawMessage, and a client that unmarshaled the envelope holds the generic
// map[string]any encoding/json produces for an `any` field. Consumers used to
// dig keys out of that map by hand, one hand-written reader per event per call
// site, silently yielding zero values whenever a key or a type moved. Routing
// every consumer through one round-trip into the owning struct means the wire
// shape has exactly one reader: the struct's own tags.
//
// It reports false rather than an error because every caller's response to a
// malformed payload is the same -- ignore the event -- and an event stream is
// not a request/response channel where a cause could be returned to anyone.
func DecodeEventData[T any](data any) (T, bool) {
	var value T
	var raw []byte
	switch typed := data.(type) {
	case nil:
		return value, false
	case json.RawMessage:
		raw = typed
	case []byte:
		raw = typed
	default:
		encoded, err := json.Marshal(data)
		if err != nil {
			return value, false
		}
		raw = encoded
	}
	if len(raw) == 0 || string(raw) == "null" {
		return value, false
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		var zero T
		return zero, false
	}
	return value, true
}
