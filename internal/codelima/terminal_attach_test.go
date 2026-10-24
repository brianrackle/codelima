package codelima

import (
	"bytes"
	"testing"
)

func TestRawAttachmentAnswersQueriesOnExactlyOneSideOfTheCut(t *testing.T) {
	var written bytes.Buffer
	lease := rawAttachmentGate{write: func(data []byte) { _, _ = written.Write(data) }}
	if err := lease.begin("client", "attachment", 10); err != nil {
		t.Fatal(err)
	}
	lease.response(9, []byte("before "))
	lease.response(11, []byte("cut "))
	lease.response(12, []byte("host-only "))
	if err := lease.commit("attachment", 11); err != nil {
		t.Fatal(err)
	}
	if got := written.String(); got != "before cut " {
		t.Fatalf("capture boundary wrote %q", got)
	}
	if err := lease.delivered("client", "attachment", 12); err != nil {
		t.Fatal(err)
	}
	if err := lease.acknowledge("client", "attachment", 12); err != nil {
		t.Fatal(err)
	}
	lease.response(13, []byte("not-delivered "))
	lease.end("attachment")
	lease.response(12, []byte("late duplicate "))
	lease.response(14, []byte("resumed"))
	if got := written.String(); got != "before cut not-delivered resumed" {
		t.Fatalf("detach query ownership wrote %q", got)
	}
}

func TestRawAttachmentRejectsCompetingOwnerAndForgedAcknowledgement(t *testing.T) {
	lease := rawAttachmentGate{write: func([]byte) {}}
	if err := lease.begin("a", "one", 1); err != nil {
		t.Fatal(err)
	}
	if err := lease.begin("b", "two", 1); err == nil {
		t.Fatal("accepted competing attachment")
	}
	if err := lease.commit("one", 1); err != nil {
		t.Fatal(err)
	}
	if err := lease.acknowledge("b", "one", 1); err == nil {
		t.Fatal("accepted different owner")
	}
	if err := lease.acknowledge("a", "one", 2); err == nil {
		t.Fatal("accepted output that was never sent")
	}
	lease.end("stale-token")
	if err := lease.acknowledge("a", "one", 1); err != nil {
		t.Fatal("stale detach ended current attachment")
	}
}

func TestRawJournalReadsAreBoundedAndDetectDroppedOutput(t *testing.T) {
	journal := newRendererJournal(40)
	journal.AppendOutput([]byte("one"))
	journal.AppendOutput([]byte("two"))
	journal.AppendOutput([]byte("three"))
	data, through, err := journal.readOutputAfter(0, 6)
	if err != nil || string(data) != "onetwo" || through != 2 {
		t.Fatalf("bounded read = %q, %d, %v", data, through, err)
	}
	journal.AppendOutput(bytes.Repeat([]byte("x"), 40))
	if _, _, err := journal.readOutputAfter(through, 40); err == nil {
		t.Fatal("silently skipped discarded output")
	}
}
