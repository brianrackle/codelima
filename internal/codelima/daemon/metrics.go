package daemon

import "time"

type CPUCoreUsage struct {
	Index        int      `json:"index"`
	UsagePercent *float64 `json:"usage_percent,omitempty"`
}

type NodeUsageDetails struct {
	CPUCores             []CPUCoreUsage `json:"cpu_cores,omitempty"`
	MemoryAvailableBytes *uint64        `json:"memory_available_bytes,omitempty"`
	MemoryCachedBytes    *uint64        `json:"memory_cached_bytes,omitempty"`
	SwapUsedBytes        *uint64        `json:"swap_used_bytes,omitempty"`
	SwapTotalBytes       *uint64        `json:"swap_total_bytes,omitempty"`
	UptimeSeconds        *float64       `json:"uptime_seconds,omitempty"`
}

// NodeMetricPoint is the bounded aggregate history shape. Per-core and process
// detail belongs to the current sample, not every stored point.
type NodeMetricPoint struct {
	SampledAt        time.Time `json:"at"`
	CPUUsagePercent  *float64  `json:"cpu,omitempty"`
	MemoryUsedBytes  *uint64   `json:"memory_used,omitempty"`
	MemoryTotalBytes *uint64   `json:"memory_total,omitempty"`
	DiskUsedBytes    *uint64   `json:"disk_used,omitempty"`
	DiskTotalBytes   *uint64   `json:"disk_total,omitempty"`
}

// NodeMetricsParams resumes history only within the matching daemon epoch.
type NodeMetricsParams struct {
	Node        string    `json:"node"`
	Epoch       string    `json:"epoch,omitempty"`
	After       time.Time `json:"after,omitzero"`
	Processes   bool      `json:"processes,omitempty"`
	ProcessSort string    `json:"process_sort,omitempty"`
}

type NodeMetricsSnapshot struct {
	NodeID       string               `json:"node_id"`
	Epoch        string               `json:"epoch"`
	History      []NodeMetricPoint    `json:"history"`
	Latest       *NodeUsageEvent      `json:"latest,omitempty"`
	Processes    *NodeProcessSnapshot `json:"processes,omitempty"`
	ProcessError string               `json:"process_error,omitempty"`
}

type NodeProcess struct {
	PID        int      `json:"pid"`
	User       string   `json:"user"`
	CPUPercent *float64 `json:"cpu_percent,omitempty"`
	RSSBytes   uint64   `json:"rss_bytes"`
	Command    string   `json:"command"`
}

// CPU percentages use one logical CPU as 100%, matching top's process view.
type NodeProcessSnapshot struct {
	SampledAt time.Time     `json:"sampled_at"`
	Rows      []NodeProcess `json:"rows"`
	Load      [3]float64    `json:"load"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated,omitempty"`
}
