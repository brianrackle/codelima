package daemon

import (
	"encoding/json"
	"errors"
)

// EmitInputOwner delivers an ephemeral clipboard effect only to the current
// input-owning frontend's current event connection. Its input lease remains
// bound to the separate physical request connection. It never consumes the
// shared state sequence: observers must not see sequence gaps for private
// effects, and synchronization must never replay an uncertain delivery.
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
	recipient := s.clipboardRecipients[lease.clientInstanceID]
	var target *clientConn
	for _, client := range s.clients {
		if client.clientInstanceID == lease.clientInstanceID && client.connectionID == recipient && client.subscribed.Load() {
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
	if s.input != lease || s.identity.Token != epoch || s.clients[target.id] != target ||
		s.clipboardRecipients[lease.clientInstanceID] != recipient || !target.subscribed.Load() {
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

// Register only a live connection whose synchronization reply was admitted.
// Connection IDs increase within the daemon epoch, so a late subscription on
// an older connection cannot replace an already synchronized successor.
func (s *Server) registerClipboardRecipientLocked(client *clientConn) {
	if s.clients[client.id] != client || client.clientInstanceID == "" {
		return
	}
	if s.clipboardRecipients == nil {
		s.clipboardRecipients = make(map[string]uint64)
	}
	if client.connectionID > s.clipboardRecipients[client.clientInstanceID] {
		s.clipboardRecipients[client.clientInstanceID] = client.connectionID
	}
}

func (s *Server) forgetClipboardRecipientIfDisconnectedLocked(instanceID string) {
	for _, client := range s.clients {
		if client.clientInstanceID == instanceID {
			return
		}
	}
	delete(s.clipboardRecipients, instanceID)
}
