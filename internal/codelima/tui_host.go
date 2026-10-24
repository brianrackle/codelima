package codelima

import (
	"context"
	"fmt"
	"runtime"

	"github.com/brianrackle/codelima/internal/hostinfo"
)

type tuiHostSummary struct {
	Capacity             hostinfo.Snapshot
	Nodes                []Node
	AllocatedCPUs        uint64
	AllocatedMemoryBytes uint64
	AllocationsKnown     bool
	UnknownNodes         int
}

// Allocation is global and follows the VM observation. In particular, a
// bootstrap failure does not mean the VM released its configured resources.
func summarizeTUIHost(capacity hostinfo.Snapshot, nodes []Node) tuiHostSummary {
	host := tuiHostSummary{Capacity: capacity, AllocationsKnown: true}
	for _, node := range nodes {
		if node.DeletedAt != nil {
			continue
		}
		observation := node.LastRuntimeObservation
		if observation == nil {
			host.UnknownNodes++
			continue
		}
		if !observation.Exists || (observation.Status != ObservationRunning && observation.Status != ObservationInstalling) {
			continue
		}
		host.Nodes = append(host.Nodes, node)
		host.AllocatedCPUs += uint64(node.VCPUs)
		host.AllocatedMemoryBytes += uint64(node.MemoryMiB) << 20
	}
	return host
}

// Load once globally, then scope only the fleet. All I/O runs before publishing
// the immutable completion event; no renderer or key handler samples the host.
func loadTUIRefresh(ctx context.Context, service *Service, scope string) tuiRefreshCompleteEvent {
	lima, ok := service.sandbox.(*LimaClient)
	if !ok {
		lima = NewLimaClient(service.cfg.MetadataRoot)
	}
	home, _ := lima.resolvedLimaHome()
	host := tuiHostSummary{Capacity: hostinfo.Read(home)}
	event := tuiRefreshCompleteEvent{Host: &host}
	nodes, err := loadTUINodes(ctx, service)
	if err != nil {
		event.Err = err
		return event
	}
	host = summarizeTUIHost(host.Capacity, nodes)
	event.Nodes, event.Err = filterNodesByDirectoryRoot(nodes, scope)
	return event
}

func (host tuiHostSummary) model() string {
	model := host.Capacity.Model
	if model == "" {
		model = runtime.GOARCH
	}
	return model
}

func (host tuiHostSummary) identity() string {
	model := host.model()
	if len([]rune(model)) > 24 {
		model = fmt.Sprintf("%.23s…", model)
	}
	return fmt.Sprintf("%s · %d vCPU · %s", model, host.Capacity.CPUs, tuiHostBytes(host.Capacity.MemoryBytes))
}

func tuiHostBytes(value *uint64) string {
	if value == nil {
		return "—"
	}
	if *value >= 1<<40 {
		return fmt.Sprintf("%.1f TiB", float64(*value)/(1<<40))
	}
	return fmt.Sprintf("%.1f GiB", float64(*value)/(1<<30))
}

func (host tuiHostSummary) allocationText() string {
	if !host.AllocationsKnown {
		return "allocation unavailable"
	}
	text := fmt.Sprintf("allocated %d/%d vCPU · mem %s/%s · disk %s free", host.AllocatedCPUs, host.Capacity.CPUs, tuiHostBytes(&host.AllocatedMemoryBytes), tuiHostBytes(host.Capacity.MemoryBytes), tuiHostBytes(host.Capacity.DiskAvailableBytes))
	if host.UnknownNodes > 0 {
		text += fmt.Sprintf(" · %d unknown", host.UnknownNodes)
	}
	return text
}
