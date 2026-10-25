package codelima

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type nodeUsageDetailsSample struct {
	Details   daemon.NodeUsageDetails
	SampledAt time.Time
}

func parseGuestPlatform(fields []string, details *daemon.NodeUsageDetails) {
	if fields[0] == "codelima-kvm" {
		details.KVMAccessible = nil
		if len(fields) == 2 && (fields[1] == "0" || fields[1] == "1") {
			accessible := fields[1] == "1"
			details.KVMAccessible = &accessible
		}
		return
	}
	details.Architecture = ""
	if len(fields) != 2 || len(fields[1]) > 32 {
		return
	}
	for _, ch := range fields[1] {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return
		}
	}
	switch fields[1] {
	case "aarch64":
		details.Architecture = "arm64"
	case "x86_64":
		details.Architecture = "amd64"
	case "armv6l", "armv7l":
		details.Architecture = "arm"
	case "i386", "i486", "i586", "i686":
		details.Architecture = "386"
	default:
		details.Architecture = fields[1]
	}
}

func parseGuestMemoryDetails(fields []string, details *daemon.NodeUsageDetails) {
	if len(fields) != 5 {
		return
	}
	var values [4]uint64
	for i := range values {
		value, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil || value > math.MaxUint64/1024 {
			return
		}
		values[i] = value * 1024
	}
	if values[3] > values[2] {
		return
	}
	used := values[2] - values[3]
	details.MemoryAvailableBytes, details.MemoryCachedBytes = &values[0], &values[1]
	details.SwapTotalBytes, details.SwapUsedBytes = &values[2], &used
}

func cloneNodeUsageDetails(source *daemon.NodeUsageDetails) *daemon.NodeUsageDetails {
	if source == nil {
		return nil
	}
	copy := *source
	copy.CPUCores = slices.Clone(source.CPUCores)
	for i := range copy.CPUCores {
		copy.CPUCores[i].UsagePercent = metricValueCopy(copy.CPUCores[i].UsagePercent)
	}
	copy.MemoryAvailableBytes = metricValueCopy(copy.MemoryAvailableBytes)
	copy.MemoryCachedBytes = metricValueCopy(copy.MemoryCachedBytes)
	copy.SwapUsedBytes = metricValueCopy(copy.SwapUsedBytes)
	copy.SwapTotalBytes = metricValueCopy(copy.SwapTotalBytes)
	copy.UptimeSeconds = metricValueCopy(copy.UptimeSeconds)
	copy.KVMAccessible = metricValueCopy(copy.KVMAccessible)
	return &copy
}

func (f *dynamicForwarder) recordUsageDetailsLocked(nodeID string, observation forwardingGuestObservation, at time.Time) {
	if f.coreCounters == nil {
		f.coreCounters = map[string]map[int]guestCPUCounters{}
	}
	if f.usageDetails == nil {
		f.usageDetails = map[string]nodeUsageDetailsSample{}
	}
	details := *cloneNodeUsageDetails(&observation.Details)
	for _, index := range slices.Sorted(maps.Keys(observation.CPUCores)) {
		core := daemon.CPUCoreUsage{Index: index}
		if previous, ok := f.coreCounters[nodeID][index]; ok {
			if percent, valid := guestCPUUsagePercent(previous, observation.CPUCores[index]); valid {
				core.UsagePercent = &percent
			}
		}
		details.CPUCores = append(details.CPUCores, core)
	}
	f.coreCounters[nodeID] = maps.Clone(observation.CPUCores)
	if len(details.CPUCores) == 0 && details.MemoryAvailableBytes == nil && details.MemoryCachedBytes == nil && details.SwapTotalBytes == nil && details.UptimeSeconds == nil && details.Architecture == "" && details.KVMAccessible == nil {
		delete(f.usageDetails, nodeID)
		return
	}
	f.usageDetails[nodeID] = nodeUsageDetailsSample{Details: details, SampledAt: at}
}
