package daemon

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDaemonStopRejectsAReplacementBeforeMutatingIt(t *testing.T) {
	s := NewServer(Config{Handler: testHandler{}})
	s.identity.Token = "replacement"
	s.accepting.Store(true)
	params, err := json.Marshal(StopParams{ExpectedEpoch: "previous"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.handle(context.Background(), &clientConn{}, Request{Method: "daemon.stop", Params: params}, false)
	if err == nil || !s.accepting.Load() {
		t.Fatal("stale restart stopped the replacement daemon")
	}
	_, err = s.handle(context.Background(), &clientConn{}, Request{Method: "daemon.stop", Params: json.RawMessage(`{"expected_epoch":3}`)}, false)
	if err == nil || !s.accepting.Load() {
		t.Fatal("malformed stop condition mutated the daemon")
	}
	if !MutatingInputMethod("daemon.stop") {
		t.Fatal("lost stop replies must be classified as outcome unknown")
	}
}
