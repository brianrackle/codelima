package daemon

import "time"

// PortsSnapshot is the cached routing projection, without terminal grids or
// guest probes on the request path. A failed inventory read retains the last
// successful membership and is reported separately from an empty fleet.
type PortsSnapshot struct {
	Enabled         bool        `json:"enabled"`
	Authorized      bool        `json:"authorized"`
	LastPollAt      time.Time   `json:"last_poll_at"`
	IntervalSeconds int         `json:"interval_seconds"`
	Listeners       int         `json:"listeners"`
	Rows            []PortRoute `json:"rows"`
	Nodes           []PortNode  `json:"nodes"`
	InventoryError  string      `json:"inventory_error,omitempty"`
	PolicyError     string      `json:"policy_error,omitempty"`
}

type PortNode struct {
	ID     string `json:"id"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
	Ready  bool   `json:"ready"`
	Legacy bool   `json:"legacy"`
}

type PortRoute struct {
	ID           string    `json:"id"`
	NodeID       string    `json:"node_id"`
	NodeSlug     string    `json:"node_slug"`
	Kind         string    `json:"kind"` // http or tcp
	Port         int       `json:"port"`
	GuestPort    int       `json:"guest_port"`
	Address      string    `json:"address"`
	Process      string    `json:"process,omitempty"`
	Status       string    `json:"status"` // live, retrying, conflicted, held, stopped, legacy
	Error        string    `json:"error,omitempty"`
	OwnerID      string    `json:"owner_id,omitempty"`
	OwnerSlug    string    `json:"owner_slug,omitempty"`
	OwnerReason  string    `json:"owner_reason,omitempty"`
	Pinned       bool      `json:"pinned"`
	DiscoveredAt time.Time `json:"discovered_at,omitzero"`
	LastSeenAt   time.Time `json:"last_seen_at,omitzero"`
	RetryAt      time.Time `json:"retry_at,omitzero"`
}

type PinPortParams struct {
	NodeID string `json:"node_id"`
	Port   int    `json:"port"`
}
