package codelima

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func (h *daemonHost) handlePorts(method string, raw json.RawMessage) (any, error) {
	if h.forwarder == nil {
		if method == "ports.pin" {
			return nil, daemon.Error("Unavailable", "port forwarding is unavailable", ExitDependencyUnavailable, nil)
		}
		return daemon.PortsSnapshot{}, nil
	}
	if method == "ports.list" {
		return h.forwarder.portsSnapshot(), nil
	}
	var params daemon.PinPortParams
	if err := json.Unmarshal(raw, &params); err != nil || params.NodeID == "" || params.Port < 1024 || params.Port > 65535 {
		return nil, daemon.Error("InvalidArgument", "node_id and an HTTP port from 1024 to 65535 are required", ExitInvalidArgument, nil)
	}
	if err := h.enterTerminalMutation(); err != nil {
		return nil, err
	}
	defer h.leaveTerminalMutation()
	if err := h.forwarder.pinBarePort(params.NodeID, params.Port); err != nil {
		return nil, toDaemonError(err)
	}
	h.forwarder.publishActivity()
	return h.forwarder.portsSnapshot(), nil
}

func portRouteID(nodeID, kind string, host, guest int) string {
	return fmt.Sprintf("%s/%s/%d/%d", nodeID, kind, host, guest)
}

func (f *dynamicForwarder) portsSnapshot() daemon.PortsSnapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	result := daemon.PortsSnapshot{Enabled: true, Authorized: f.prepared, LastPollAt: f.lastPoll,
		IntervalSeconds: int(durationOrDefault(f.interval, forwardingPollInterval) / time.Second), InventoryError: f.inventoryErr, PolicyError: f.policyWriteErr}
	if f.policyErr != nil {
		result.PolicyError = f.policyErr.Error()
	}
	for _, server := range f.servers {
		result.Listeners += len(server.listeners)
	}
	for _, server := range f.tcpServers {
		result.Listeners += len(server.listeners)
	}
	for _, node := range f.inventory {
		result.Nodes = append(result.Nodes, daemon.PortNode{ID: node.ID, Slug: node.Slug, Status: string(node.Status), Ready: applicationRoutesReady(node), Legacy: !node.DaemonPortForwarding && len(node.Ports) != 0})
		for _, value := range node.Ports {
			parts := strings.SplitN(value, ":", 2)
			if len(parts) != 2 {
				continue
			}
			host, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			guest, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}
			row := daemon.PortRoute{ID: portRouteID(node.ID, "tcp", host, guest), NodeID: node.ID, NodeSlug: node.Slug, Kind: "tcp", Port: host, GuestPort: guest, Address: fmt.Sprintf("127.0.0.1:%d", host), Status: "stopped"}
			running := node.LastRuntimeObservation != nil && node.LastRuntimeObservation.Status == ObservationRunning
			switch {
			case !node.DaemonPortForwarding && running:
				row.Status, row.Error = "legacy", "Lima owns this mapping; stop/start the node to enable readiness gating"
			case !applicationRoutesReady(node) && running:
				row.Status = "held"
			case f.tcpServers[host] != nil:
				server := f.tcpServers[host]
				if server.mapping.nodeID == node.ID || server.mapping.conflict {
					row.Status, row.Error = f.publishedTCPStatusLocked(server), server.lastErr
					if row.Status == "serving" {
						row.Status = "live"
					}
				}
			case running:
				row.Status = "held"
			}
			if state := f.nodes[node.ID]; state != nil {
				row.RetryAt = state.backoffUntil
				row.Process = state.portProcesses[guest]
			}
			result.Rows = append(result.Rows, row)
		}
	}
	for key, route := range f.routes {
		node := f.inventory[route.nodeID]
		row := daemon.PortRoute{ID: portRouteID(route.nodeID, "http", key.port, key.port), NodeID: route.nodeID, NodeSlug: coalesce(node.Slug, key.node), Kind: "http", Port: key.port, GuestPort: key.port,
			Address: route.url(), Status: "live", DiscoveredAt: route.discoveredAt, LastSeenAt: route.seenAt}
		if route.lapsed {
			row.Status = "retrying"
		}
		server := f.servers[key.port]
		if server == nil || server.status != "serving" {
			row.Status = "conflicted"
			if server != nil {
				row.Error = server.lastErr
			}
		} else if owner := f.routes[dynamicRouteKey{node: server.defaultNode, port: key.port}]; owner != nil {
			row.OwnerID, row.OwnerSlug = owner.nodeID, coalesce(f.inventory[owner.nodeID].Slug, server.defaultNode)
			row.OwnerReason = "first active"
			if key.port == codexLoginCallbackPort {
				row.OwnerReason = "newest listener"
			}
			if f.pins[key.port] == owner.nodeID {
				row.OwnerReason = "pinned"
			}
		}
		row.Pinned = f.pins[key.port] == route.nodeID
		if state := f.nodes[route.nodeID]; state != nil {
			row.RetryAt = state.backoffUntil
			row.Process = state.portProcesses[key.port]
		}
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].Slug < result.Nodes[j].Slug })
	sort.Slice(result.Rows, func(i, j int) bool {
		a, b := result.Rows[i], result.Rows[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.NodeSlug != b.NodeSlug {
			return a.NodeSlug < b.NodeSlug
		}
		return a.ID < b.ID
	})
	return result
}

var listenerProcessPattern = regexp.MustCompile(`\("([^"\r\n]+)",pid=`)

// ss is optional enrichment. Only names attached to loopback/wildcard TCP
// listeners qualify; missing permissions/tools never manufacture a label or
// change the independent /proc routing scan.
func parseListenerProcesses(data []byte) map[int]string {
	names := map[int][]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[0] != "codelima-listener" || fields[1] != "LISTEN" {
			continue
		}
		host, portText, err := net.SplitHostPort(fields[4])
		if err != nil {
			continue
		}
		ip := net.ParseIP(host)
		if host != "*" && (ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified())) {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		for _, match := range listenerProcessPattern.FindAllStringSubmatch(line, 4) {
			name := boundedOperationText(plainLogText(match[1]), 64)
			if name != "" && !slices.Contains(names[port], name) {
				names[port] = append(names[port], name)
			}
		}
	}
	result := map[int]string{}
	for port, values := range names {
		slices.Sort(values)
		result[port] = strings.Join(values, ", ")
	}
	return result
}
