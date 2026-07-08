// Package terminal holds the durable identity model for CodeLima terminals:
// the opaque per-terminal ID and the project/node target key that a terminal
// belongs to. It is a leaf package — it imports nothing from the parent
// codelima package, so the dependency arrow always points inward (see ADR 61
// and IMPROVEMENT_PLAN Part E, Track 1).
package terminal

import (
	"fmt"
	"strings"
)

// TerminalID is the opaque, durable identity of a single live terminal.
//
// It is allocated once and never derived from anything positional: NOT from a
// tab index, pane, layout position, or the TargetKey it currently belongs to.
// Identity must survive any future change in how terminals are arranged,
// displayed, or owned (the herdr lesson, reimplemented from IMPROVEMENT_PLAN
// Part E, not from herdr source). Allocation convention: "term_" +
// unix-nano-hex + "_" + counter-hex. It exists in this package now but is only
// wired into tuiSession in Track 1 PR2; PR1 introduces the type alone.
type TerminalID string

// TargetKind distinguishes the two kinds of thing a terminal can attach to: a
// project (a host-local workspace shell) or a node (a VM shell).
type TargetKind int

const (
	// TargetProject is a project host-shell target.
	TargetProject TargetKind = iota
	// TargetNode is a node (VM) shell target.
	TargetNode
)

const (
	projectPrefix = "project:"
	nodePrefix    = "node:"
)

// TargetKey identifies the project or node a terminal belongs to. It is a
// comparable value, so it may be used directly as a map key. Its String()
// rendering is byte-identical to the "project:<id>" / "node:<id>" strings the
// TUI has always produced, so on-disk and on-screen identifiers do not change.
type TargetKey struct {
	Kind TargetKind
	ID   string
}

// ProjectTarget builds a project target key.
func ProjectTarget(id string) TargetKey { return TargetKey{Kind: TargetProject, ID: id} }

// NodeTarget builds a node target key.
func NodeTarget(id string) TargetKey { return TargetKey{Kind: TargetNode, ID: id} }

// String renders the target key exactly as the TUI has always rendered it:
// "project:<id>" or "node:<id>". An unknown kind renders as the empty string,
// matching the legacy tuiTreeEntry.key() sentinel for an empty entry.
func (k TargetKey) String() string {
	switch k.Kind {
	case TargetProject:
		return projectPrefix + k.ID
	case TargetNode:
		return nodePrefix + k.ID
	default:
		return ""
	}
}

// ParseTargetKey is the single chokepoint for turning a "project:<id>" /
// "node:<id>" string back into a TargetKey. Every other call site must go
// through here rather than hand-parsing the prefix. Everything after a
// recognized prefix is taken verbatim as the ID (IDs are opaque and may be
// empty, matching the legacy strings.TrimPrefix behavior); a string with no
// recognized prefix is rejected.
func ParseTargetKey(s string) (TargetKey, error) {
	if id, ok := strings.CutPrefix(s, projectPrefix); ok {
		return TargetKey{Kind: TargetProject, ID: id}, nil
	}
	if id, ok := strings.CutPrefix(s, nodePrefix); ok {
		return TargetKey{Kind: TargetNode, ID: id}, nil
	}
	return TargetKey{}, fmt.Errorf("terminal: %q is not a valid target key", s)
}
