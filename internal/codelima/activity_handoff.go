package codelima

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type activityHandoffRoute struct {
	Node  string        `json:"node"`
	Port  int           `json:"port"`
	Route activityRoute `json:"route"`
}

type activityHandoffState struct {
	Pending    []activityMessageRequest    `json:"pending,omitempty"`
	Dropped    uint64                      `json:"dropped,omitempty"`
	Terminals  map[string]TerminalMetadata `json:"terminals"`
	Routes     []activityHandoffRoute      `json:"routes,omitempty"`
	Owners     map[int]activityRoute       `json:"owners,omitempty"`
	CPUOver    bool                        `json:"cpu_over,omitempty"`
	MemoryOver bool                        `json:"memory_over,omitempty"`
}

// The caller owns updateGate exclusively, so no flush can remove an event.
// All observers share mu: freezing their admission and copying their baselines
// with the pending queue forms one cut, including a publisher already in flight.
func (e *daemonActivityEvents) prepareHandoff(entries []*daemonTerminalEntry) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handoff = true
	state := activityHandoffState{
		Pending: slices.Clone(e.pending), Dropped: e.dropped,
		CPUOver: e.cpuOver, MemoryOver: e.memoryOver,
		Terminals: make(map[string]TerminalMetadata, len(entries)),
		Owners:    e.routes.Owners,
	}
	for _, entry := range entries {
		state.Terminals[entry.state.TerminalID] = activityBaselineMetadata(entry.activity.metadata)
	}
	for key, route := range e.routes.Routes {
		state.Routes = append(state.Routes, activityHandoffRoute{Node: key.node, Port: key.port, Route: route})
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode native Activity handoff: %w", err)
	}
	if len(data) > daemon.MaxHandoffActivityBytes {
		return nil, errors.New("native Activity handoff exceeds its size bound")
	}
	return data, nil
}

func activityBaselineMetadata(metadata TerminalMetadata) TerminalMetadata {
	return TerminalMetadata{
		PromptIdleSinceUnixMilli: metadata.PromptIdleSinceUnixMilli,
		BellCount:                metadata.BellCount, NotificationCount: metadata.NotificationCount,
		NotificationTitle: terminalMetadataText(metadata.NotificationTitle, 120),
		NotificationBody:  terminalMetadataText(metadata.NotificationBody, 512),
	}
}

func (e *daemonActivityEvents) resumeAfterHandoff() {
	e.mu.Lock()
	e.handoff = false
	e.mu.Unlock()
	e.notify()
}

// Validate the whole payload before changing state. Restored admission remains
// memory-only until the successor owns the daemon lock and commits its reply.
func (e *daemonActivityEvents) restoreHandoff(manifest daemon.HandoffManifest) error {
	if manifest.Version < daemon.PreviousActivityHandoffVersion {
		return nil
	}
	var state activityHandoffState
	if len(manifest.Activity) == 0 || len(manifest.Activity) > daemon.MaxHandoffActivityBytes {
		return errors.New("invalid native Activity handoff size")
	}
	if err := json.Unmarshal(manifest.Activity, &state); err != nil {
		return fmt.Errorf("read native Activity handoff: %w", err)
	}
	if len(state.Pending) > nativeActivityQueueLimit || len(state.Terminals) != len(manifest.Session.Terminals) {
		return errors.New("invalid native Activity handoff count")
	}
	seen := make(map[string]bool, len(state.Pending))
	for _, request := range state.Pending {
		normalized, err := normalizeActivityMessage(request)
		if err != nil || normalized != request || seen[request.ID] {
			return errors.New("invalid native Activity handoff message")
		}
		seen[request.ID] = true
	}
	for _, terminal := range manifest.Session.Terminals {
		metadata, ok := state.Terminals[terminal.TerminalID]
		if !ok || metadata.PromptIdleSinceUnixMilli < 0 || metadata != activityBaselineMetadata(metadata) {
			return errors.New("invalid native Activity terminal baseline")
		}
	}
	routes := make(map[dynamicRouteKey]activityRoute, len(state.Routes))
	for _, item := range state.Routes {
		key := dynamicRouteKey{node: item.Node, port: item.Port}
		if _, duplicate := routes[key]; duplicate || item.Node == "" || len(item.Node) > 128 || item.Port != item.Route.Port || !validActivityHandoffRoute(item.Port, item.Route) {
			return errors.New("invalid native Activity route baseline")
		}
		routes[key] = item.Route
	}
	for port, route := range state.Owners {
		if !validActivityHandoffRoute(port, route) {
			return errors.New("invalid native Activity owner baseline")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pending, e.dropped = state.Pending, state.Dropped
	e.cpuOver, e.memoryOver = state.CPUOver, state.MemoryOver
	e.routes = forwardingActivitySnapshot{Routes: routes, Owners: state.Owners}
	e.adoptedBaselines = state.Terminals
	return nil
}

func validActivityHandoffRoute(port int, route activityRoute) bool {
	request := activityMessageRequest{ID: route.NodeID, NodeID: route.NodeID, NodeSlug: route.NodeSlug, Source: "ports", Text: "baseline"}
	normalized, err := normalizeActivityMessage(request)
	return port > 0 && port <= 65535 && route.Port == port && err == nil && normalized == request
}

func (e *daemonActivityEvents) adoptedBaseline(id string, fallback TerminalMetadata) TerminalMetadata {
	e.mu.Lock()
	defer e.mu.Unlock()
	if metadata, ok := e.adoptedBaselines[id]; ok {
		delete(e.adoptedBaselines, id)
		return metadata
	}
	return fallback
}
