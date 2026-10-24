package codelima

type hostAllocations struct {
	Nodes                []Node
	AllocatedCPUs        uint64
	AllocatedMemoryBytes uint64
	UnknownNodes         int
}

// Allocation is global and follows observed VM state, including resources
// still held by a VM whose bootstrap failed. Missing observations stay unknown.
func summarizeHostAllocations(nodes []Node) hostAllocations {
	var allocation hostAllocations
	for _, node := range nodes {
		if node.DeletedAt != nil {
			continue
		}
		observation := node.LastRuntimeObservation
		if observation == nil {
			allocation.UnknownNodes++
			continue
		}
		if !observation.Exists || observation.Status == ObservationStopped || observation.Status == ObservationUninitialized {
			continue
		}
		if observation.Status != ObservationRunning && observation.Status != ObservationInstalling {
			allocation.UnknownNodes++
			continue
		}
		allocation.Nodes = append(allocation.Nodes, node)
		allocation.AllocatedCPUs += uint64(node.VCPUs)
		allocation.AllocatedMemoryBytes += uint64(node.MemoryMiB) << 20
	}
	return allocation
}
