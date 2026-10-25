package codelima

// NodeOperationProgress is a bounded read projection of durable phase evidence.
// The fraction counts observed completed/skipped phases, not elapsed time or a
// prediction of remaining work. Full receipts keep the authoritative phases.
type NodeOperationProgress struct {
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Phase     string `json:"phase,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func summarizeNodeOperationProgress(record NodeOperation) *NodeOperationProgress {
	if b := record.Batch; b != nil {
		return &NodeOperationProgress{Total: b.Total, Completed: b.Total - b.Queued - b.Running, Phase: "Batch", Detail: b.summary()}
	}
	if len(record.Phases) == 0 {
		return nil
	}
	p := &NodeOperationProgress{Total: len(record.Phases)}
	for _, phase := range record.Phases {
		if phase.State == "succeeded" || phase.State == "skipped" {
			p.Completed++
		}
		if phase.ID == record.CurrentPhase {
			p.Phase = boundedOperationText(plainLogText(phase.Label), 64)
			p.Detail = boundedOperationText(plainLogText(phase.Detail), 1024)
		}
	}
	return p
}
