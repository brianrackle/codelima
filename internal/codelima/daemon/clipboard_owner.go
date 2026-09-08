package daemon

import (
	"encoding/json"
	"errors"
)

// EmitInputOwner delivers an ephemeral clipboard effect only to the current
// physical input-owning frontend. It never consumes the shared state sequence:
// observers must not see sequence gaps for effects they were not meant to see,
// and synchronization must never replay an uncertain clipboard delivery.
func (s *Server) EmitInputOwner(name string, data any) bool {
	if name != EventTerminalClipboard {
		return false
	}
	payload, err := json.Marshal(data)
	if err != nil || len(payload) > MaxMessageSize {
		return false
	}
	s.mu.Lock()
	lease := s.input
	epoch := s.identity.Token
	var target *clientConn
	for _, client := range s.clients {
		if client.clientInstanceID == lease.clientInstanceID && client.connectionID == lease.connectionID && client.subscribed.Load() {
			target = client
			break
		}
	}
	s.mu.Unlock()
	if target == nil || lease.clientInstanceID == "" {
		return false
	}
	frame, err := marshalLine(Event{Event: name, Data: json.RawMessage(payload), DaemonEpoch: epoch})
	if err != nil || len(frame) > MaxMessageSize {
		return false
	}
	// Revalidate at bounded queue admission, serialized with seat takeover and
	// disconnect. An ownership change during encoding discards the effect.
	s.mu.Lock()
	if s.input != lease || s.identity.Token != epoch || s.clients[target.id] != target || !target.subscribed.Load() {
		s.mu.Unlock()
		return false
	}
	queued := target.outbound.TryPush(frame, outboundLow)
	s.mu.Unlock()
	if !queued {
		target.fail("daemon", "ready", CloseQueueFull, errors.New("input owner outbound queue is full during clipboard delivery"))
	}
	return queued
}
