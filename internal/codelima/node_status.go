package codelima

// nodeVMStatus is a presentation of the runtime observation, independent of
// setup and lifecycle operations. Legacy running/stopped values are useful
// only when no runtime observation has been made yet.
func nodeVMStatus(node Node) string {
	if observation := node.LastRuntimeObservation; observation != nil {
		if observation.Status != "" {
			return string(observation.Status)
		}
		if !observation.Exists {
			return "missing"
		}
		return "unknown"
	}
	switch node.Status {
	case NodeStatusRunning, NodeStatusStopped:
		return string(node.Status)
	default:
		return "unknown"
	}
}

func nodeSetupStatus(node Node) string {
	switch state := nodeLifecycleState(node); state {
	case NodeStatusFailed, NodeStatusProvisioning, NodeStatusRegistering:
		return string(state)
	}
	if node.BootstrapCompleted {
		return "ready"
	}
	return "pending"
}

// CLI presentation leaves the internal lifecycle record intact. Daemon clients
// and lifecycle actions still need its failure and in-flight states for retry,
// operation ownership and guest-shell readiness.
type nodePresentation struct {
	Node        `yaml:",inline"`
	SetupStatus string `json:"setup_status" yaml:"setup_status"`
}

func presentNode(node Node) nodePresentation {
	setup := nodeSetupStatus(node)
	node.Status = NodeStatus(nodeVMStatus(node))
	return nodePresentation{Node: node, SetupStatus: setup}
}

func presentNodeOutput(value any) any {
	switch data := value.(type) {
	case Node:
		return presentNode(data)
	case []Node:
		nodes := make([]nodePresentation, len(data))
		for i, node := range data {
			nodes[i] = presentNode(node)
		}
		return nodes
	default:
		return value
	}
}
