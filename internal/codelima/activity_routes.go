package codelima

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
)

type activityRoute struct {
	NodeID, NodeSlug string
	Port             int
	Lapsed, Serving  bool
}

type forwardingActivitySnapshot struct {
	Routes map[dynamicRouteKey]activityRoute
	Owners map[int]activityRoute
}

// The forwarding lock protects only this small projection. The callback may
// enqueue transitions after unlock, but cannot delay routing with journal I/O.
func (f *dynamicForwarder) publishActivity() {
	f.mu.RLock()
	observer := f.activityObserver
	if observer == nil {
		f.mu.RUnlock()
		return
	}
	view := forwardingActivitySnapshot{Routes: make(map[dynamicRouteKey]activityRoute), Owners: make(map[int]activityRoute)}
	for key, route := range f.routes {
		slug := key.node
		if node := f.nodes[route.nodeID]; node != nil {
			slug = node.node.Slug
		}
		server := f.servers[key.port]
		value := activityRoute{NodeID: route.nodeID, NodeSlug: slug, Port: key.port, Lapsed: route.lapsed, Serving: server != nil && server.status == "serving"}
		view.Routes[key] = value
		if value.Serving && server.defaultNode == key.node {
			view.Owners[key.port] = value
		}
	}
	f.mu.RUnlock()
	observer(view)
}

func (e *daemonActivityEvents) observeForwarding(view forwardingActivitySnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.handoff {
		return
	}
	keys := slices.Collect(maps.Keys(view.Routes))
	for key := range e.routes.Routes {
		if _, exists := view.Routes[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].node != keys[j].node {
			return keys[i].node < keys[j].node
		}
		return keys[i].port < keys[j].port
	})
	for _, key := range keys {
		previous, existed := e.routes.Routes[key]
		current, exists := view.Routes[key]
		level, transition, detail := slog.LevelInfo, "", ""
		switch {
		case !exists:
			current, transition = previous, "removed"
		case !existed:
			transition = "added"
		case current.Lapsed && !previous.Lapsed:
			level, transition, detail = slog.LevelWarn, "degraded", " · guest tunnel unavailable; retrying"
		case !current.Lapsed && previous.Lapsed:
			transition = "restored"
		case current.Serving != previous.Serving:
			if current.Serving {
				transition = "restored"
			} else {
				level, transition = slog.LevelWarn, "unavailable"
			}
		}
		if transition == "" {
			continue
		}
		if exists && !current.Serving {
			level, detail = slog.LevelWarn, detail+" · host port unavailable"
		}
		e.enqueueLocked(activityMessageRequest{Source: "ports", Level: level, NodeID: current.NodeID, NodeSlug: current.NodeSlug, Text: fmt.Sprintf("route :%d %s%s", key.port, transition, detail), Action: activityOpenPorts})
	}
	ports := slices.Collect(maps.Keys(view.Owners))
	for port := range e.routes.Owners {
		if _, exists := view.Owners[port]; !exists {
			ports = append(ports, port)
		}
	}
	slices.Sort(ports)
	for _, port := range ports {
		previous, existed := e.routes.Owners[port]
		current, exists := view.Owners[port]
		if existed && exists && previous.NodeID == current.NodeID {
			continue
		}
		text := fmt.Sprintf("now owns localhost:%d", port)
		if !exists {
			current, text = previous, fmt.Sprintf("released localhost:%d", port)
		} else if existed {
			text += " · was " + previous.NodeSlug
		}
		e.enqueueLocked(activityMessageRequest{Source: "ports", NodeID: current.NodeID, NodeSlug: current.NodeSlug, Text: text, Action: activityOpenPorts})
	}
	e.routes = forwardingActivitySnapshot{Routes: maps.Clone(view.Routes), Owners: maps.Clone(view.Owners)}
}
