package codelima

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEventsLog(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(events.jsonl) error = %v", err)
	}
	return path
}

func eventLine(t *testing.T, eventType, message string) string {
	t.Helper()

	payload, err := json.Marshal(Event{Timestamp: time.Unix(0, 0).UTC(), Type: eventType, Message: message})
	if err != nil {
		t.Fatalf("Marshal(Event) error = %v", err)
	}
	return string(payload) + "\n"
}

func readEventsWithLog(t *testing.T, path string) ([]Event, string) {
	t.Helper()

	var logs bytes.Buffer
	events, err := readEvents(path, newTextLogger(&logs, slog.LevelDebug))
	if err != nil {
		t.Fatalf("readEvents() error = %v", err)
	}
	return events, logs.String()
}

func TestReadEventsReturnsEmptyForAMissingLog(t *testing.T) {
	t.Parallel()

	events, err := readEvents(filepath.Join(t.TempDir(), "absent.jsonl"), nil)
	if err != nil {
		t.Fatalf("readEvents(missing) error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("readEvents(missing) = %+v, want no events", events)
	}
}

func TestReadEventsKeepsEarlierRecordsAfterATornFinalWrite(t *testing.T) {
	t.Parallel()

	// A process killed mid-append leaves a partial, unterminated last line.
	path := writeEventsLog(t,
		eventLine(t, "node.created", "first")+
			eventLine(t, "node.started", "second")+
			`{"timestamp":"1970-01-01T00:00:00Z","type":"node.st`)

	events, logs := readEventsWithLog(t, path)
	if len(events) != 2 {
		t.Fatalf("readEvents() = %+v, want the 2 complete records", events)
	}
	if events[0].Type != "node.created" || events[1].Type != "node.started" {
		t.Fatalf("readEvents() returned unexpected records: %+v", events)
	}
	if !strings.Contains(logs, "discarding torn trailing event record") {
		t.Fatalf("expected the torn tail to be reported at debug, logs = %q", logs)
	}
	// Crash debris is expected, so it must not be logged as corruption.
	if strings.Contains(logs, "skipping corrupt event record") {
		t.Fatalf("torn tail was reported as corruption, logs = %q", logs)
	}
}

func TestReadEventsSkipsAGarbageRecordInTheMiddle(t *testing.T) {
	t.Parallel()

	path := writeEventsLog(t,
		eventLine(t, "node.created", "first")+
			"\n"+
			"}{ not json at all\n"+
			eventLine(t, "node.started", "second"))

	events, logs := readEventsWithLog(t, path)
	if len(events) != 2 {
		t.Fatalf("readEvents() = %+v, want the 2 decodable records", events)
	}
	if events[0].Type != "node.created" || events[1].Type != "node.started" {
		t.Fatalf("readEvents() returned unexpected records: %+v", events)
	}
	if !strings.Contains(logs, "skipping corrupt event record") {
		t.Fatalf("expected a corruption warning for the middle record, logs = %q", logs)
	}
	// A fully written but undecodable record is real damage, not crash debris.
	if !strings.Contains(logs, "level=WARN") {
		t.Fatalf("corrupt middle record was not warned about, logs = %q", logs)
	}
	if !strings.Contains(logs, "line=3") {
		t.Fatalf("expected the warning to name the physical line, logs = %q", logs)
	}
}

func TestReadEventsAcceptsRecordsLargerThanTheDefaultScannerLimit(t *testing.T) {
	t.Parallel()

	// The default bufio.Scanner token limit is 64KiB, which used to fail the
	// whole read forever once one record grew past it.
	oversizedMessage := strings.Repeat("m", 256*1024)
	if len(oversizedMessage) <= bufio.MaxScanTokenSize {
		t.Fatalf("test message is not larger than the default scanner limit")
	}
	path := writeEventsLog(t,
		eventLine(t, "node.created", "first")+
			eventLine(t, "node.bootstrap", oversizedMessage)+
			eventLine(t, "node.started", "third"))

	events, logs := readEventsWithLog(t, path)
	if len(events) != 3 {
		t.Fatalf("readEvents() = %d records, want all 3", len(events))
	}
	if events[1].Message != oversizedMessage {
		t.Fatalf("the large record was truncated: %d bytes, want %d", len(events[1].Message), len(oversizedMessage))
	}
	if strings.Contains(logs, "skipping") || strings.Contains(logs, "discarding") {
		t.Fatalf("a valid large record was skipped, logs = %q", logs)
	}
}

func TestReadEventsSkipsARecordBeyondTheRetentionLimitAndKeepsReading(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create(events.jsonl) error = %v", err)
	}
	writer := bufio.NewWriter(file)
	if _, err := writer.WriteString(eventLine(t, "node.created", "first")); err != nil {
		t.Fatalf("WriteString(first) error = %v", err)
	}
	// One runaway record past eventsRecordLimit: it is dropped, but the reader
	// consumes it to the next newline so everything after it survives.
	chunk := strings.Repeat("x", 1<<20)
	for written := 0; written <= eventsRecordLimit; written += len(chunk) {
		if _, err := writer.WriteString(chunk); err != nil {
			t.Fatalf("WriteString(oversized) error = %v", err)
		}
	}
	if _, err := writer.WriteString("\n" + eventLine(t, "node.started", "second")); err != nil {
		t.Fatalf("WriteString(second) error = %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	events, logs := readEventsWithLog(t, path)
	if len(events) != 2 {
		t.Fatalf("readEvents() = %+v, want the 2 records around the oversized one", events)
	}
	if events[0].Type != "node.created" || events[1].Type != "node.started" {
		t.Fatalf("readEvents() returned unexpected records: %+v", events)
	}
	if !strings.Contains(logs, "exceeds the maximum event record size") {
		t.Fatalf("expected the oversized record to be reported, logs = %q", logs)
	}
}

func TestNodeEventsToleratesADamagedLog(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	node := saveTestNode(t, store, "eventful")

	for _, event := range []Event{
		{Timestamp: time.Unix(1, 0).UTC(), Type: "node.created"},
		{Timestamp: time.Unix(2, 0).UTC(), Type: "node.started"},
	} {
		if err := store.AppendNodeEvent(node.ID, event); err != nil {
			t.Fatalf("AppendNodeEvent() error = %v", err)
		}
	}

	// Append damage the way a crash would: a garbage record followed by a torn
	// one that never got its newline.
	path := store.nodeEventsPath(node.ID)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(events.jsonl) error = %v", err)
	}
	if _, err := file.WriteString("\x00\x00 torn\n{\"type\":\"node.st"); err != nil {
		t.Fatalf("WriteString(damage) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var logs bytes.Buffer
	store.SetLogger(newTextLogger(&logs, slog.LevelDebug))
	events, err := store.NodeEvents(node.ID)
	if err != nil {
		t.Fatalf("NodeEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("NodeEvents() = %+v, want the 2 intact records", events)
	}
	if events[0].Type != "node.created" || events[1].Type != "node.started" {
		t.Fatalf("NodeEvents() returned unexpected records: %+v", events)
	}
	if !strings.Contains(logs.String(), "skipping corrupt event record") {
		t.Fatalf("expected the damaged record to be reported, logs = %q", logs.String())
	}
	if !strings.Contains(logs.String(), path) {
		t.Fatalf("expected the warning to name the log path, logs = %q", logs.String())
	}
}
