package terminal

import (
	"strings"
	"testing"
)

func TestRegistryAllocateResolveRemove(t *testing.T) {
	t.Parallel()

	reg := NewTerminalRuntimeRegistry[string]()
	if reg.Len() != 0 {
		t.Fatalf("new registry Len() = %d, want 0", reg.Len())
	}

	rt := reg.Allocate("backend-a")
	if reg.Len() != 1 {
		t.Fatalf("after Allocate Len() = %d, want 1", reg.Len())
	}
	if rt.Backend != "backend-a" {
		t.Fatalf("runtime Backend = %q, want backend-a", rt.Backend)
	}

	got, ok := reg.Lookup(rt.ID)
	if !ok || got != rt {
		t.Fatalf("Lookup(%q) = (%v, %v), want the allocated runtime", rt.ID, got, ok)
	}

	removed, ok := reg.Remove(rt.ID)
	if !ok || removed != rt {
		t.Fatalf("Remove(%q) = (%v, %v), want the allocated runtime", rt.ID, removed, ok)
	}
	if reg.Len() != 0 {
		t.Fatalf("after Remove Len() = %d, want 0", reg.Len())
	}
	if _, ok := reg.Lookup(rt.ID); ok {
		t.Fatalf("Lookup after Remove should miss")
	}
	if _, ok := reg.Remove(rt.ID); ok {
		t.Fatalf("second Remove should be a no-op")
	}
}

func TestRegistryAllocatesDistinctOpaqueIDs(t *testing.T) {
	t.Parallel()

	reg := NewTerminalRuntimeRegistry[int]()
	seen := map[TerminalID]bool{}
	for i := 0; i < 1000; i++ {
		id := reg.Allocate(i).ID
		if seen[id] {
			t.Fatalf("duplicate terminal id %q", id)
		}
		seen[id] = true
		if !strings.HasPrefix(string(id), "term_") {
			t.Fatalf("terminal id %q missing opaque prefix", id)
		}
	}
}

// TestTerminalIDIsNeverATargetOrTabString enforces the identity rule: an
// allocated TerminalID must not be mistakable for a target key ("project:"/"node:")
// or a tab/session key ("<target>#<n>").
func TestTerminalIDIsNeverATargetOrTabString(t *testing.T) {
	t.Parallel()

	reg := NewTerminalRuntimeRegistry[int]()
	id := string(reg.Allocate(0).ID)

	if _, err := ParseTargetKey(id); err == nil {
		t.Fatalf("terminal id %q must not parse as a target key", id)
	}
	if strings.Contains(id, ":") {
		t.Fatalf("terminal id %q must not contain a target delimiter", id)
	}
	if strings.Contains(id, "#") {
		t.Fatalf("terminal id %q must not contain a tab-key delimiter", id)
	}
	for _, s := range []string{"project:p1", "node:n1", "project:p1#1", "node:n1#2", ""} {
		if id == s {
			t.Fatalf("terminal id collided with %q", s)
		}
	}
}
