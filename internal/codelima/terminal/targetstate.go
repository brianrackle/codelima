package terminal

// TabID identifies a single terminal tab within a target. In v1 there is one
// pane per tab and the TabID is the session-key string "<target>#<n>"; it
// becomes a first-class opaque id once tabs grow into layout trees (Track 6.6).
type TabID string

// TerminalTabState is the pure per-tab bookkeeping for one tab: its identity,
// display label, and the opaque id of the terminal runtime that backs it.
type TerminalTabState struct {
	ID         TabID
	Label      string
	TerminalID TerminalID
}

// TargetTerminalState is the per-target tab bookkeeping that used to live as
// three parallel maps on the TUI session store (tabCounters, sessionOrder,
// sessionErrors). It is owned by a single goroutine (the event loop today, the
// daemon later) and holds no lock.
//
// NextTabIndex is the monotonic per-target tab counter: it only ever increases
// and is never reset when a tab closes, so tab numbering never reuses a value
// (pinned by the identity characterization tests). OpenError records the last
// open failure for the target (target-keyed, independent of any tab).
// ActiveTabID names the tab a UI currently shows; in today's split TUI the
// model still owns active-tab tracking because the session-manager interface is
// fixed, so this field is reserved for the unified owner in Track 2.
type TargetTerminalState struct {
	Target       TargetKey
	Tabs         []TerminalTabState
	ActiveTabID  TabID
	NextTabIndex int
	OpenError    error
}

// AllocateTabIndex advances and returns the monotonic per-target tab counter.
func (s *TargetTerminalState) AllocateTabIndex() int {
	s.NextTabIndex++
	return s.NextTabIndex
}

// AppendTab records a newly opened tab at the end of the ordered list, unless a
// tab with the same id is already open.
func (s *TargetTerminalState) AppendTab(tab TerminalTabState) {
	if s.HasTab(tab.ID) {
		return
	}
	s.Tabs = append(s.Tabs, tab)
}

// RemoveTab drops the tab with id, preserving the order of the rest. It reports
// whether a tab was removed.
func (s *TargetTerminalState) RemoveTab(id TabID) bool {
	for i, tab := range s.Tabs {
		if tab.ID != id {
			continue
		}
		s.Tabs = append(s.Tabs[:i], s.Tabs[i+1:]...)
		return true
	}
	return false
}

// HasTab reports whether a tab with id is open.
func (s *TargetTerminalState) HasTab(id TabID) bool {
	for _, tab := range s.Tabs {
		if tab.ID == id {
			return true
		}
	}
	return false
}

// TabIDs returns the open tab ids in order.
func (s *TargetTerminalState) TabIDs() []TabID {
	ids := make([]TabID, len(s.Tabs))
	for i, tab := range s.Tabs {
		ids[i] = tab.ID
	}
	return ids
}
