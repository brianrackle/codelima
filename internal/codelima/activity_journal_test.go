package codelima

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActivityJournalPersistsPrivateIdempotentMessages(t *testing.T) {
	s, _ := newTestService(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	j, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	request := activityMessageRequest{ID: newID(), Source: "clipboard", Level: slog.LevelInfo, Text: "Copied localhost:3000"}
	record, err := j.Append(request)
	if err != nil || record.Sequence != 1 || !record.Time.Equal(now) {
		t.Fatal(record, err)
	}
	now = now.Add(time.Minute)
	duplicate, err := j.Append(request)
	if err != nil || duplicate != record {
		t.Fatal("retry changed a durable message", duplicate, err)
	}
	request.Text = "different intent"
	if _, err := j.Append(request); exitCodeForError(err) != ExitPreconditionFailed {
		t.Fatal("conflicting message ID was accepted", err)
	}
	for path, want := range map[string]os.FileMode{j.dir: 0o700, filepath.Join(j.dir, record.ID+".json"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatal("message privacy", path, info, err)
		}
	}
	reopened, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	records, through := reopened.Snapshot()
	if len(records) != 1 || records[0] != record || through != 1 || reopened.epoch == j.epoch {
		t.Fatal("restart lost evidence or reused its live projection epoch", records, through)
	}
	records[0].Text = "mutated"
	records, _ = reopened.Snapshot()
	if records[0].Text != record.Text {
		t.Fatal("snapshot exposed mutable storage")
	}
}

func TestActivityJournalBoundsRetentionAndContinuesSequenceAfterRestart(t *testing.T) {
	s, _ := newTestService(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	j, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	j.limit = 3
	var first ActivityMessage
	for i := range 4 {
		record, err := j.Append(activityMessageRequest{ID: newID(), Source: "tui", Level: slog.LevelWarn, Text: "capacity warning"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = record
		}
	}
	records, through := j.Snapshot()
	if len(records) != 3 || through != 4 || records[0].Sequence != 4 {
		t.Fatal("bounded journal order", records, through)
	}
	if _, err := os.Stat(filepath.Join(j.dir, first.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("evicted message still occupies disk", err)
	}
	reopened, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	next, err := reopened.Append(activityMessageRequest{ID: newID(), Source: "daemon", Level: slog.LevelInfo, Text: "reconnected"})
	if err != nil || next.Sequence != 5 {
		t.Fatal("restart reset the retained sequence", next, err)
	}
	now = now.Add(activityMessageRetention + time.Second)
	if records, _ := reopened.Snapshot(); len(records) != 0 {
		t.Fatal("expired messages leaked into a read", records)
	}
	if _, err := reopened.Append(activityMessageRequest{ID: newID(), Source: "daemon", Level: slog.LevelInfo, Text: "new day"}); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(j.dir)
	if err != nil || len(files) != 1 {
		t.Fatal("expired files were not pruned", files, err)
	}
}

func TestActivityJournalRejectsInvalidAndUncommittedMessages(t *testing.T) {
	s, _ := newTestService(t)
	j, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	valid := activityMessageRequest{ID: newID(), Source: "tui", Level: slog.LevelError, Text: "could not open shell"}
	for _, invalid := range []activityMessageRequest{
		{ID: "../escape", Source: "tui", Text: "bad ID"},
		{ID: newID(), Source: "tui", Text: "  "},
		{ID: newID(), Source: "tui", Text: strings.Repeat("x", 4097)},
		{ID: newID(), Source: "tui", Text: "bad level", Level: 100},
	} {
		if _, err := j.Append(invalid); exitCodeForError(err) != ExitInvalidArgument {
			t.Fatal("invalid message accepted", invalid, err)
		}
	}
	blocked := filepath.Join(j.dir, valid.ID+".json")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(valid); err == nil {
		t.Fatal("failed persistence was acknowledged")
	}
	if records, through := j.Snapshot(); len(records) != 0 || through != 0 {
		t.Fatal("failed persistence changed the journal", records, through)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(valid); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("broken JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newActivityJournal(s); err == nil {
		t.Fatal("corrupt retained evidence was silently discarded")
	}
}

func TestActivityJournalConcurrentWritersKeepUniqueSequences(t *testing.T) {
	s, _ := newTestService(t)
	j, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	for range 24 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if _, err := j.Append(activityMessageRequest{ID: newID(), Source: "tui", Text: "message"}); err != nil {
				t.Error(err)
			}
		}()
	}
	writers.Wait()
	records, through := j.Snapshot()
	if len(records) != 24 || through != 24 {
		t.Fatal("concurrent writer lost evidence", len(records), through)
	}
	for i, record := range records {
		if record.Sequence != uint64(24-i) {
			t.Fatal("sequence was reused or reordered", records)
		}
	}
}
