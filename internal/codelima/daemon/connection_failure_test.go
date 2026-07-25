package daemon

import (
	"errors"
	"testing"
)

func TestConnectionFailurePreservesFirstCause(t *testing.T) {
	t.Parallel()

	failure := &connectionFailure{}
	readErr := errors.New("reader protocol error")
	writeErr := errors.New("writer broken pipe")

	if !failure.Fail(ConnectionCloseRecord{Reason: CloseProtocolError, Underlying: readErr.Error()}, readErr) {
		t.Fatal("first failure was not accepted")
	}
	if failure.Fail(ConnectionCloseRecord{Reason: CloseWriteError, Underlying: writeErr.Error()}, writeErr) {
		t.Fatal("secondary failure replaced the first failure")
	}

	if !errors.Is(failure.Cause(), readErr) {
		t.Fatalf("Cause() = %v, want first reader error", failure.Cause())
	}
	record := failure.Record()
	if record.Reason != CloseProtocolError || record.Underlying != readErr.Error() {
		t.Fatalf("Record() = %#v, want first failure record", record)
	}
}
