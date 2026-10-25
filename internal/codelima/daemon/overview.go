package daemon

import "time"

// Overview contains cached runtime observations only. It excludes terminal
// grids, output journals and renderer probes, so regular UI refreshes do not
// pay the cost of a diagnostic snapshot.
type Overview struct {
	Status  Status           `json:"status"`
	Runtime *RuntimeOverview `json:"runtime,omitempty"`
}

type OverviewHandler interface {
	Overview() RuntimeOverview
}

type RuntimeOverview struct {
	UpdatedAt  time.Time          `json:"updated_at"`
	Forwarding ForwardingOverview `json:"forwarding"`
	Reclaim    ReclaimOverview    `json:"virtiofs_reclaim"`
}

type ForwardingOverview struct {
	Enabled         bool      `json:"enabled"`
	Authorized      bool      `json:"authorized"`
	Listeners       int       `json:"listeners"`
	Ports           int       `json:"ports"`
	DegradedPorts   int       `json:"degraded_ports"`
	LapsedRoutes    int       `json:"lapsed_routes"`
	IntervalSeconds int       `json:"interval_seconds"`
	LastPollAt      time.Time `json:"last_poll_at"`
	LastError       string    `json:"last_error,omitempty"`
	InventoryError  string    `json:"inventory_error,omitempty"`
}

type ReclaimOverview struct {
	Enabled            bool      `json:"enabled"`
	Supported          bool      `json:"supported"`
	IntervalSeconds    int       `json:"interval_seconds"`
	LastRunAt          time.Time `json:"last_run_at"`
	NextRunAt          time.Time `json:"next_run_at"`
	LastReclaimedNodes int       `json:"last_reclaimed_nodes"`
	LastError          string    `json:"last_error,omitempty"`
}
