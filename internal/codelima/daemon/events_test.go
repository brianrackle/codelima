package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

// TestEventPayloadWireShapes is the golden pin for every broadcast payload the
// daemon owns. These payloads used to be map literals built at the emit site,
// which is why the wire order looks arbitrary: encoding/json writes map keys
// sorted and struct fields in declaration order, so each struct declares its
// fields in the sorted-key order of the map it replaced. Introducing the types
// was a refactor, and this test is what makes "the bytes did not change" a
// checkable claim rather than an assertion in a commit message.
//
// A deliberate wire change means updating a golden here AND bumping
// ProtocolVersion; a change that only updates the golden is a bug.
func TestEventPayloadWireShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		payload any
		want    string
	}{
		{
			name:    EventTerminalDirty,
			payload: TerminalDirtyEvent{SnapshotSequence: 42, Stale: true, TerminalID: "term-1"},
			want:    `{"snapshot_sequence":42,"stale":true,"terminal_id":"term-1"}`,
		},
		{
			// Every key is present even when empty: the map literals had no
			// omitempty, and a subscriber that reads "stale" or "tab_id" must
			// keep seeing the field rather than infer it from absence.
			name:    EventTerminalDirty + " zero",
			payload: TerminalDirtyEvent{},
			want:    `{"snapshot_sequence":0,"stale":false,"terminal_id":""}`,
		},
		{
			name:    EventTerminalError,
			payload: TerminalErrorEvent{Error: "snapshot unavailable", TerminalID: "term-1"},
			want:    `{"error":"snapshot unavailable","terminal_id":"term-1"}`,
		},
		{
			name:    EventTerminalClipboard,
			payload: TerminalClipboardEvent{TabID: "tab-1", TerminalID: "term-1", Text: "copied"},
			want:    `{"tab_id":"tab-1","terminal_id":"term-1","text":"copied"}`,
		},
		{
			name:    EventTargetTabsChanged,
			payload: TargetTabsChangedEvent{Target: "node:demo"},
			want:    `{"target":"node:demo"}`,
		},
		{
			name:    EventDaemonUpdateFailed,
			payload: DaemonUpdateFailedEvent{Error: "handoff transport unsupported"},
			want:    `{"error":"handoff transport unsupported"}`,
		},
		{
			name:    EventDaemonUpdateCommitted,
			payload: DaemonUpdateCommittedEvent{PID: 4242},
			want:    `{"pid":4242}`,
		},
		{
			name:    EventInputRevoked,
			payload: InputRevokedEvent{ClientID: "tui-1"},
			want:    `{"client_id":"tui-1"}`,
		},
		{
			name:    EventNodeUsageChanged + " empty sample",
			payload: NodeUsageEvent{NodeID: "node-1"},
			want:    `{"node_id":"node-1","sampled_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			name: EventNodeUsageChanged,
			payload: NodeUsageEvent{
				NodeID:           "node-1",
				SampledAt:        time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
				CPUUsagePercent:  ptrForTest(12.5),
				MemoryUsedBytes:  ptrForTest(uint64(1024)),
				MemoryTotalBytes: ptrForTest(uint64(4096)),
				DiskUsedBytes:    ptrForTest(uint64(2048)),
				DiskTotalBytes:   ptrForTest(uint64(8192)),
			},
			want: `{"node_id":"node-1","sampled_at":"2026-08-07T12:00:00Z",` +
				`"cpu_usage_percent":12.5,"memory_used_bytes":1024,"memory_total_bytes":4096,` +
				`"disk_used_bytes":2048,"disk_total_bytes":8192}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.want {
				t.Fatalf("%s payload =\n  %s\nwant\n  %s", test.name, encoded, test.want)
			}
		})
	}
}

// TestSnapshotCellWireEncodingIsCompact pins the per-cell encoding. A published
// 160x50 grid is 8000 of these, so the key names are the payload: this test
// exists so the tags cannot quietly grow back into descriptive ones, and so the
// exact bytes a client parses are written down somewhere.
func TestSnapshotCellWireEncodingIsCompact(t *testing.T) {
	t.Parallel()

	blank := SnapshotCell{Grapheme: " ", Width: 1, FG: 0xC0C0C0, BG: 0x101010, FGDefault: true, BGDefault: true}
	if got, want := string(mustMarshalForTest(t, blank)), `{"g":" ","w":1,"f":12632256,"b":1052688,"fd":true,"bd":true}`; got != want {
		t.Fatalf("blank cell = %s, want %s", got, want)
	}

	// Everything is omitempty, so a fully default cell costs almost nothing and
	// only the attributes a cell actually carries are paid for.
	if got, want := string(mustMarshalForTest(t, SnapshotCell{})), `{}`; got != want {
		t.Fatalf("zero cell = %s, want %s", got, want)
	}

	styled := SnapshotCell{
		Grapheme: "A", Width: 1, FG: 1, BG: 2,
		Bold: true, Faint: true, Italic: true, Underline: true,
		Strikethrough: true, Inverse: true, Invisible: true, Blink: true,
		Hyperlink: "https://example.test",
	}
	want := `{"g":"A","w":1,"f":1,"b":2,"bo":true,"fa":true,"i":true,"u":true,` +
		`"st":true,"iv":true,"in":true,"bl":true,"h":"https://example.test"}`
	if got := string(mustMarshalForTest(t, styled)); got != want {
		t.Fatalf("styled cell = %s, want %s", got, want)
	}

	// Round-tripping is what actually matters: omitempty is only safe because
	// producer and consumer share this one struct.
	var decoded SnapshotCell
	if err := json.Unmarshal(mustMarshalForTest(t, styled), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != styled {
		t.Fatalf("cell round trip = %#v, want %#v", decoded, styled)
	}
}

// TestDecodeEventDataAcceptsEveryPayloadShape covers the one decode seam. The
// same payload reaches consumers in three forms depending on where they sit --
// pre-encoded on the server, a generic map after a client unmarshaled the
// envelope, or the typed value itself in-process -- and all three must land in
// the same struct.
func TestDecodeEventDataAcceptsEveryPayloadShape(t *testing.T) {
	t.Parallel()

	want := TerminalClipboardEvent{TabID: "tab-1", TerminalID: "term-1", Text: "copied"}
	raw := mustMarshalForTest(t, want)

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string]any{
		"raw message": json.RawMessage(raw),
		"bytes":       raw,
		"generic map": generic,
		"typed value": want,
	} {
		got, ok := DecodeEventData[TerminalClipboardEvent](data)
		if !ok || got != want {
			t.Fatalf("DecodeEventData(%s) = %#v, %v; want %#v, true", name, got, ok, want)
		}
	}

	for name, data := range map[string]any{
		"nil":          nil,
		"null":         json.RawMessage("null"),
		"empty":        json.RawMessage(nil),
		"wrong shape":  json.RawMessage(`["not an object"]`),
		"unencodable":  make(chan int),
		"broken bytes": []byte("{not json"),
	} {
		if got, ok := DecodeEventData[TerminalClipboardEvent](data); ok {
			t.Fatalf("DecodeEventData(%s) = %#v, true; want a rejection", name, got)
		}
	}
}

func ptrForTest[T any](value T) *T { return &value }

func mustMarshalForTest(t *testing.T, value any) []byte {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
