package codelima

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/google/uuid"
)

const routingPolicyLimit = 16 << 20

type routingPolicy struct {
	Version int            `json:"version"`
	Claims  []routingClaim `json:"claims"`
	Pins    []routingPin   `json:"pins"`
}
type routingClaim struct {
	NodeID       string    `json:"node_id"`
	Node         string    `json:"node"`
	Port         int       `json:"port"`
	DiscoveredAt time.Time `json:"discovered_at"`
	RemovedAt    time.Time `json:"removed_at,omitzero"`
}
type routingPin struct {
	Port   int    `json:"port"`
	NodeID string `json:"node_id"`
}

// Loading is read-only and precedes discovery, including in a handoff candidate
// that does not yet own the daemon lock. Only the owned host may persist.
func (f *dynamicForwarder) loadRoutingPolicy() {
	f.policyMu.Lock()
	defer f.policyMu.Unlock()
	if f.policyPath == "" {
		return
	}
	data, truncated, err := readDiagnosticFile(f.policyPath, routingPolicyLimit)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if truncated {
		err = errors.New("routing preferences exceed the size limit")
	}
	var saved routingPolicy
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err == nil {
		err = validateRoutingPolicy(saved)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.policyErr = fmt.Errorf("read routing preferences: %w", err)
		return
	}
	f.restored = map[dynamicRouteKey]routeDiscoveryMemory{}
	if f.discovered == nil {
		f.discovered = map[dynamicRouteKey]routeDiscoveryMemory{}
	}
	f.pins = map[int]string{}
	for _, claim := range saved.Claims {
		key := dynamicRouteKey{node: claim.Node, port: claim.Port}
		memory := routeDiscoveryMemory{nodeID: claim.NodeID, discoveredAt: claim.DiscoveredAt, removedAt: claim.RemovedAt}
		if claim.RemovedAt.IsZero() {
			f.restored[key] = memory
		} else {
			f.discovered[key] = memory
		}
	}
	for _, pin := range saved.Pins {
		f.pins[pin.Port] = pin.NodeID
	}
}

func validateRoutingPolicy(saved routingPolicy) error {
	if saved.Version != 1 {
		return errors.New("unsupported routing preferences version")
	}
	claims := map[dynamicRouteKey]bool{}
	active := map[routingPin]bool{}
	for _, claim := range saved.Claims {
		key := dynamicRouteKey{node: claim.Node, port: claim.Port}
		if _, err := uuid.Parse(claim.NodeID); err != nil {
			return errors.New("invalid claim node ID")
		}
		if validateSandboxName(claim.Node) != nil || claim.Port < 1024 || claim.Port > 65535 || claim.DiscoveredAt.IsZero() || claims[key] {
			return errors.New("invalid or duplicate routing claim")
		}
		claims[key] = true
		if claim.RemovedAt.IsZero() {
			active[routingPin{Port: claim.Port, NodeID: claim.NodeID}] = true
		}
	}
	pins := map[int]bool{}
	for _, pin := range saved.Pins {
		if !active[pin] || pins[pin.Port] {
			return errors.New("pin has no unique active claim")
		}
		pins[pin.Port] = true
	}
	return nil
}

// The caller holds policyMu. mu only protects the in-memory projection, never
// serialization/fsync. Route identity changes also take policyMu first.
func (f *dynamicForwarder) routingPolicySnapshot(pins map[int]string) (routingPolicy, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.policyErr != nil {
		return routingPolicy{}, f.policyErr
	}
	claims := maps.Clone(f.discovered)
	if claims == nil {
		claims = map[dynamicRouteKey]routeDiscoveryMemory{}
	}
	maps.Copy(claims, f.restored)
	for key, route := range f.routes {
		claims[key] = routeDiscoveryMemory{nodeID: route.nodeID, discoveredAt: route.discoveredAt}
	}
	result := routingPolicy{Version: 1}
	for key, claim := range claims {
		result.Claims = append(result.Claims, routingClaim{NodeID: claim.nodeID, Node: key.node, Port: key.port, DiscoveredAt: claim.discoveredAt, RemovedAt: claim.removedAt})
	}
	if pins == nil {
		pins = f.pins
	}
	for port, id := range pins {
		result.Pins = append(result.Pins, routingPin{Port: port, NodeID: id})
	}
	sort.Slice(result.Claims, func(i, j int) bool {
		if result.Claims[i].Node != result.Claims[j].Node {
			return result.Claims[i].Node < result.Claims[j].Node
		}
		return result.Claims[i].Port < result.Claims[j].Port
	})
	sort.Slice(result.Pins, func(i, j int) bool { return result.Pins[i].Port < result.Pins[j].Port })
	return result, nil
}

func (f *dynamicForwarder) writeRoutingPolicy(saved routingPolicy) (err error) {
	defer func() {
		f.mu.Lock()
		f.policyWriteErr = ""
		if err != nil {
			f.policyWriteErr = err.Error()
		}
		f.mu.Unlock()
	}()
	if f.policyPath == "" {
		return nil
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if len(data) > routingPolicyLimit {
		return errors.New("routing preferences exceed the size limit")
	}
	if string(data) == f.policySaved {
		return nil
	}
	if err := atomicWriteFile(f.policyPath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("save routing preferences: %w", err)
	}
	f.policySaved = string(data)
	return nil
}

func (f *dynamicForwarder) persistRoutingPolicy() error {
	f.policyMu.Lock()
	defer f.policyMu.Unlock()
	if f.policyClosed {
		return nil
	}
	saved, err := f.routingPolicySnapshot(nil)
	if err != nil {
		return err
	}
	return f.writeRoutingPolicy(saved)
}

func (f *dynamicForwarder) prepareRoutingHandoff() error {
	f.policyMu.Lock()
	defer f.policyMu.Unlock()
	if f.policyClosed {
		return daemon.Error("Unavailable", "port forwarding is shutting down", ExitDependencyUnavailable, nil)
	}
	saved, err := f.routingPolicySnapshot(nil)
	if err != nil {
		return err
	}
	if err := f.writeRoutingPolicy(saved); err != nil {
		return err
	}
	// Freeze identity at this durable cut. In-flight scans cannot expire pins
	// or change claim timestamps after the candidate has loaded this file.
	// Existing HTTP/TCP requests and cached inspection remain available.
	f.policyHandoff = true
	return nil
}

func (f *dynamicForwarder) resumeRoutingAfterHandoff() {
	f.policyMu.Lock()
	f.policyHandoff = false
	f.policyMu.Unlock()
}

func (f *dynamicForwarder) pinBarePort(nodeID string, port int) error {
	f.policyMu.Lock()
	defer f.policyMu.Unlock()
	if f.policyClosed {
		return daemon.Error("Unavailable", "port forwarding is shutting down", ExitDependencyUnavailable, nil)
	}
	f.mu.RLock()
	var key dynamicRouteKey
	for candidate, route := range f.routes {
		if route.nodeID == nodeID && candidate.port == port {
			key = candidate
			break
		}
	}
	server := f.servers[port]
	if key.node == "" || server == nil || server.status != "serving" {
		f.mu.RUnlock()
		return daemon.Error("PreconditionFailed", "the selected HTTP route is no longer available; refresh Ports", ExitPreconditionFailed, nil)
	}
	pins := maps.Clone(f.pins)
	if pins == nil {
		pins = map[int]string{}
	}
	pins[port] = nodeID
	f.mu.RUnlock()
	saved, err := f.routingPolicySnapshot(pins)
	if err == nil {
		err = f.writeRoutingPolicy(saved)
	}
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.pins = pins
	// A reconcile can replace the server while the disk write is in flight,
	// although policyMu keeps the chosen route identity stable.
	if current := f.servers[port]; current != nil {
		current.defaultNode = f.preferredClaimantLocked(port)
	}
	f.mu.Unlock()
	return nil
}

func (h *daemonHost) persistForwardingPolicy() {
	// TryRLock avoids blocking producer shutdown behind a handoff writer. The
	// update path performs its own final flush while holding that writer lock.
	if !h.activityEvents.owned.Load() || !h.serving.Load() || !h.updateGate.TryRLock() {
		return
	}
	defer h.updateGate.RUnlock()
	if h.replaced.Load() {
		return
	}
	h.forwarder.mu.RLock()
	previous := h.forwarder.policyWriteErr
	if h.forwarder.policyErr != nil {
		previous = h.forwarder.policyErr.Error()
	}
	h.forwarder.mu.RUnlock()
	if err := h.forwarder.persistRoutingPolicy(); err != nil && err.Error() != previous {
		h.forwarder.recordError(err)
	}
}
