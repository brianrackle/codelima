package codelima

import (
	"slices"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const maxCPUTrendPoints = 60

// Inventory supplies a minute of CPU only. Existing usage pushes then extend
// the same observation; no frontend sampler or extra telemetry event is needed.
func (h *nodeMetricsHistory) recentCPU(now time.Time) []daemon.NodeMetricPoint {
	points := make([]daemon.NodeMetricPoint, 0, maxCPUTrendPoints)
	for i := len(h.points) - 1; i >= 0 && len(points) < maxCPUTrendPoints; i-- {
		point := h.points[(h.head+i)%len(h.points)]
		if point.SampledAt.Before(now.Add(-time.Minute)) {
			break
		}
		if point.SampledAt.After(now) {
			continue
		}
		points = append(points, daemon.NodeMetricPoint{SampledAt: point.SampledAt, CPUUsagePercent: metricValueCopy(point.CPUUsagePercent)})
	}
	slices.Reverse(points)
	return points
}

func mergeCPUTrend(previous, incoming []daemon.NodeMetricPoint, at time.Time, cpu *float64) []daemon.NodeMetricPoint {
	if at.IsZero() {
		return nil
	}
	byTime := make(map[time.Time]daemon.NodeMetricPoint, maxCPUTrendPoints*2+1)
	cutoff := at.Add(-time.Minute)
	add := func(point daemon.NodeMetricPoint) {
		if point.SampledAt.Before(cutoff) || point.SampledAt.After(at) {
			return
		}
		byTime[point.SampledAt] = daemon.NodeMetricPoint{SampledAt: point.SampledAt, CPUUsagePercent: metricValueCopy(point.CPUUsagePercent)}
	}
	for _, point := range previous {
		add(point)
	}
	for _, point := range incoming {
		add(point)
	}
	add(daemon.NodeMetricPoint{SampledAt: at, CPUUsagePercent: cpu})
	points := make([]daemon.NodeMetricPoint, 0, len(byTime))
	for _, point := range byTime {
		points = append(points, point)
	}
	slices.SortFunc(points, func(a, b daemon.NodeMetricPoint) int { return a.SampledAt.Compare(b.SampledAt) })
	return points[max(0, len(points)-maxCPUTrendPoints):]
}
