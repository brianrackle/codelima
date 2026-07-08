package terminal

import (
	"errors"
	"testing"
)

func TestTargetTerminalStateAllocateTabIndexIsMonotonic(t *testing.T) {
	t.Parallel()

	st := &TargetTerminalState{Target: NodeTarget("n1")}
	if got := st.AllocateTabIndex(); got != 1 {
		t.Fatalf("first AllocateTabIndex = %d, want 1", got)
	}
	if got := st.AllocateTabIndex(); got != 2 {
		t.Fatalf("second AllocateTabIndex = %d, want 2", got)
	}
	if got := st.AllocateTabIndex(); got != 3 {
		t.Fatalf("third AllocateTabIndex = %d, want 3", got)
	}
	// Removing tabs must not roll the counter back.
	st.AppendTab(TerminalTabState{ID: "node:n1#3"})
	st.RemoveTab("node:n1#3")
	if got := st.AllocateTabIndex(); got != 4 {
		t.Fatalf("AllocateTabIndex after remove = %d, want 4 (never reused)", got)
	}
}

func TestTargetTerminalStateTabOrderingAndMembership(t *testing.T) {
	t.Parallel()

	st := &TargetTerminalState{Target: NodeTarget("n1")}
	st.AppendTab(TerminalTabState{ID: "node:n1#1", Label: "a", TerminalID: "term_a"})
	st.AppendTab(TerminalTabState{ID: "node:n1#2", Label: "b", TerminalID: "term_b"})
	st.AppendTab(TerminalTabState{ID: "node:n1#3", Label: "c", TerminalID: "term_c"})

	if got := st.TabIDs(); !equalTabIDs(got, []TabID{"node:n1#1", "node:n1#2", "node:n1#3"}) {
		t.Fatalf("TabIDs after appends = %v", got)
	}
	if !st.HasTab("node:n1#2") {
		t.Fatalf("HasTab(#2) = false, want true")
	}

	// Appending a duplicate id is a no-op.
	st.AppendTab(TerminalTabState{ID: "node:n1#2", Label: "dup"})
	if got := st.TabIDs(); !equalTabIDs(got, []TabID{"node:n1#1", "node:n1#2", "node:n1#3"}) {
		t.Fatalf("TabIDs after duplicate append = %v", got)
	}

	// Removing the middle tab preserves the order of the rest.
	if !st.RemoveTab("node:n1#2") {
		t.Fatalf("RemoveTab(#2) = false, want true")
	}
	if got := st.TabIDs(); !equalTabIDs(got, []TabID{"node:n1#1", "node:n1#3"}) {
		t.Fatalf("TabIDs after remove = %v", got)
	}
	if st.HasTab("node:n1#2") {
		t.Fatalf("HasTab(#2) after remove = true, want false")
	}
	if st.RemoveTab("node:n1#2") {
		t.Fatalf("second RemoveTab(#2) = true, want false")
	}
}

func TestTargetTerminalStateOpenErrorIsTargetScoped(t *testing.T) {
	t.Parallel()

	st := &TargetTerminalState{Target: ProjectTarget("p1")}
	if st.OpenError != nil {
		t.Fatalf("new state OpenError = %v, want nil", st.OpenError)
	}
	sentinel := errors.New("workspace missing")
	st.OpenError = sentinel
	if st.OpenError != sentinel {
		t.Fatalf("OpenError = %v, want the recorded error", st.OpenError)
	}
	// An error can exist with no tabs open.
	if len(st.Tabs) != 0 {
		t.Fatalf("expected no tabs, got %d", len(st.Tabs))
	}
}

func equalTabIDs(a, b []TabID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
