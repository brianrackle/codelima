package testutil

import (
	"testing"
	"time"
)

func WaitForCondition(t *testing.T, timeout time.Duration, fn func() bool, description string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
