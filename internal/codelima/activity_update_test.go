package codelima

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestActivityNativeUpdateFailureDoesNotRecordSuccess(t *testing.T) {
	for _, failure := range []string{"unauthorized", "acknowledgment"} {
		t.Run(failure, func(t *testing.T) { testActivityUpdateFailure(t, failure) })
	}
}

func testActivityUpdateFailure(t *testing.T, failure string) {
	t.Helper()
	service, _ := newHandoffTestService(t)
	path := filepath.Join(daemon.HomePaths(service.cfg.MetadataRoot).Dir, "reject.sock")
	listener, err := daemon.ListenHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close(); _ = os.Remove(path) }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDaemonImport(ctx, service, path, "expected", nil) }()
	_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	peer := daemon.NewHandoffConnection(conn, daemon.HandoffFramingLengthPrefixed)
	if hello, err := peer.ReadControlMessage(); err != nil || hello.Type != "hello" {
		t.Fatal(hello, err)
	}
	if err := peer.WriteJSON(handoffManifestFor(nil, "expected"), nil); err != nil {
		t.Fatal(err)
	}
	if ready, err := peer.ReadControlMessage(); err != nil || ready.Type != "ready" {
		t.Fatal(ready, err)
	}
	token := "wrong"
	if failure == "acknowledgment" {
		token = "expected"
		// Refuse the committed reply while leaving the write side available
		// to send authorization. The importer can acquire ownership, but its
		// final write must fail before it opens native event publication.
		if err := conn.CloseRead(); err != nil {
			t.Fatal(err)
		}
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "commit", Token: token}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("failed commit succeeded")
		}
	case <-ctx.Done():
		t.Fatal("import did not finish after failed commit")
	}
	if _, err := os.Stat(filepath.Join(service.cfg.MetadataRoot, "_activity")); !os.IsNotExist(err) {
		t.Fatal("failed import created Activity history", err)
	}
}
