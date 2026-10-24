package codelima

import (
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const (
	nodeMetricRetention  = time.Hour
	maxNodeMetricSamples = 3600
)

// nodeMetricsHistory is an event-loop/forwarder-lock-owned ring, never a second
// sampler. A nil value marks an unavailable interval rather than zero load.
type nodeMetricsHistory struct {
	points []daemon.NodeMetricPoint
	head   int
}

func metricValueCopy[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneMetricPoint(point daemon.NodeMetricPoint) daemon.NodeMetricPoint {
	point.CPUUsagePercent = metricValueCopy(point.CPUUsagePercent)
	point.MemoryUsedBytes = metricValueCopy(point.MemoryUsedBytes)
	point.MemoryTotalBytes = metricValueCopy(point.MemoryTotalBytes)
	point.DiskUsedBytes = metricValueCopy(point.DiskUsedBytes)
	point.DiskTotalBytes = metricValueCopy(point.DiskTotalBytes)
	return point
}

func (h *nodeMetricsHistory) append(event nodeUsageEvent) {
	h.appendPoint(daemon.NodeMetricPoint{SampledAt: event.SampledAt, CPUUsagePercent: event.CPUUsagePercent, MemoryUsedBytes: event.MemoryUsedBytes, MemoryTotalBytes: event.MemoryTotalBytes, DiskUsedBytes: event.DiskUsedBytes, DiskTotalBytes: event.DiskTotalBytes})
}

func (h *nodeMetricsHistory) appendPoint(point daemon.NodeMetricPoint) {
	if point.SampledAt.IsZero() {
		return
	}
	if len(h.points) > 0 {
		last := (h.head + len(h.points) - 1) % len(h.points)
		if point.SampledAt.Before(h.points[last].SampledAt) {
			return
		}
		if point.SampledAt.Equal(h.points[last].SampledAt) {
			h.points[last] = cloneMetricPoint(point)
			return
		}
	}
	point = cloneMetricPoint(point)
	if len(h.points) < maxNodeMetricSamples {
		h.points = append(h.points, point)
		return
	}
	h.points[h.head] = point
	h.head = (h.head + 1) % maxNodeMetricSamples
}

func (h *nodeMetricsHistory) latestAt() time.Time {
	if len(h.points) == 0 {
		return time.Time{}
	}
	return h.points[(h.head+len(h.points)-1)%len(h.points)].SampledAt
}

func (h *nodeMetricsHistory) after(now, after time.Time) []daemon.NodeMetricPoint {
	points := make([]daemon.NodeMetricPoint, 0)
	cutoff := now.Add(-nodeMetricRetention)
	for i := range len(h.points) {
		point := h.points[(h.head+i)%len(h.points)]
		if point.SampledAt.Before(cutoff) || point.SampledAt.After(now) || !point.SampledAt.After(after) {
			continue
		}
		points = append(points, cloneMetricPoint(point))
	}
	return points
}

// Caller holds f.mu; the ring shares ownership with the existing telemetry
// maps. It survives TUI reconnects and is intentionally lost on daemon restart.
func (f *dynamicForwarder) recordMetricsHistoryLocked(event nodeUsageEvent) {
	if f.metricsHistory == nil {
		f.metricsHistory = make(map[string]*nodeMetricsHistory)
	}
	if f.metricsEpoch == "" {
		f.metricsEpoch = newID()
	}
	history := f.metricsHistory[event.NodeID]
	if history == nil {
		history = &nodeMetricsHistory{}
		f.metricsHistory[event.NodeID] = history
	}
	history.append(event)
}

func (f *dynamicForwarder) pruneMetricsHistoryLocked(now time.Time) {
	for id, history := range f.metricsHistory {
		if history.latestAt().Before(now.Add(-nodeMetricRetention)) {
			delete(f.metricsHistory, id)
		}
	}
}

func (f *dynamicForwarder) metricsSnapshot(nodeID, epoch string, after time.Time) daemon.NodeMetricsSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.metricsEpoch == "" {
		f.metricsEpoch = newID()
	}
	now := f.nowUTC()
	f.pruneMetricsHistoryLocked(now)
	if epoch != f.metricsEpoch {
		after = time.Time{}
	}
	result := daemon.NodeMetricsSnapshot{NodeID: nodeID, Epoch: f.metricsEpoch, History: []daemon.NodeMetricPoint{}}
	if history := f.metricsHistory[nodeID]; history != nil {
		result.History = history.after(now, after)
		latest := f.nodeUsageEventLocked(nodeID, history.latestAt())
		result.Latest = &latest
	}
	return result
}
