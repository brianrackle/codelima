package codelima

import (
	"context"
	"strings"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestUnsupportedLegacyHandoffTransportDetection(t *testing.T) {
	t.Parallel()
	legacy := daemon.Error(
		"ExternalCommandFailed",
		"daemon live update failed: listen unixpacket /tmp/handoff.sock: socket: protocol not supported",
		ExitExternalFailure,
		nil,
	)
	if !isUnsupportedLegacyHandoffTransport(legacy) {
		t.Fatal("legacy macOS unixpacket failure was not recognized")
	}
	if isUnsupportedLegacyHandoffTransport(context.DeadlineExceeded) {
		t.Fatal("unrelated update failure would trigger destructive restart fallback")
	}
	if isUnsupportedLegacyHandoffTransport(daemon.Error("ExternalCommandFailed", "protocol not supported", ExitExternalFailure, nil)) {
		t.Fatal("generic protocol error would trigger legacy fallback")
	}
	if !strings.Contains(legacy.Error(), "unixpacket") {
		t.Fatal("test fixture does not describe the legacy transport")
	}
}
