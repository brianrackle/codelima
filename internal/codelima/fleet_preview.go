package codelima

import (
	"path/filepath"
	"slices"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type fleetRuntimeEvidence struct {
	VMType            string `json:"vm_type,omitempty"`
	MountType         string `json:"mount_type,omitempty"`
	Nested            *bool  `json:"nested,omitempty"`
	WorkspacePath     string `json:"workspace_path,omitempty"`
	WorkspaceWritable *bool  `json:"workspace_writable,omitempty"`
}

type fleetPreviewEvidence struct {
	NodeID        string                   `json:"node_id"`
	NodeUpdatedAt time.Time                `json:"node_updated_at"`
	Runtime       *fleetRuntimeEvidence    `json:"runtime,omitempty"`
	RuntimeError  string                   `json:"runtime_error,omitempty"`
	MetricsEpoch  string                   `json:"metrics_epoch,omitempty"`
	History       []daemon.NodeMetricPoint `json:"history,omitempty"`
	AgentChecks   []fleetAgentCheck        `json:"agent_checks,omitempty"`
	AgentError    string                   `json:"agent_error,omitempty"`
	AuthImport    *fleetAuthImportEvidence `json:"auth_import,omitempty"`
}

// Optional local inspection: no runtime preparation, command execution or guest
// connection. Providers without this capability leave configuration unknown.
type runtimeConfigurationInspector interface {
	InspectConfiguration(Node) (fleetRuntimeEvidence, error)
}

func (c *LimaClient) InspectConfiguration(node Node) (fleetRuntimeEvidence, error) {
	var result fleetRuntimeEvidence
	if !validSandboxNamePattern.MatchString(node.SandboxName) {
		return result, invalidArgument("invalid instance name for configuration inspection", nil)
	}
	home, err := c.resolvedLimaHome()
	if err != nil {
		return result, err
	}
	config, err := readLimaConfiguration(filepath.Join(home, node.SandboxName, "lima.yaml"))
	if err != nil {
		return result, err
	}
	result.VMType = boundedOperationText(plainLogText(config.VMType), 64)
	result.MountType = boundedOperationText(plainLogText(config.MountType), 64)
	result.Nested = config.Nested
	if nodeWorkspaceMode(node) == "mounted" {
		guestPath := coalesce(node.GuestWorkspacePath, node.WorkspaceMountPath, node.DirectoryPath)
		for _, mount := range config.Mounts {
			if filepath.Clean(mount.Location) != filepath.Clean(node.DirectoryPath) || filepath.Clean(coalesce(mount.MountPoint, mount.Location)) != filepath.Clean(guestPath) {
				continue
			}
			writable := mount.Writable != nil && *mount.Writable
			result.WorkspaceWritable = &writable
			result.WorkspacePath = boundedOperationText(plainLogText(guestPath), 512)
			break
		}
	}
	return result, nil
}

func (h *daemonHost) fleetPreview(node Node, claimed bool) *fleetPreviewEvidence {
	p := &fleetPreviewEvidence{NodeID: node.ID, NodeUpdatedAt: node.UpdatedAt}
	p.AuthImport = h.service.store.fleetAuthImport(node)
	bootstrap, err := h.service.store.LoadBootstrapState(node.ID)
	if err != nil {
		p.AgentChecks, p.AgentError = configuredFleetAgentChecks(node), fleetEvidenceError(err)
	} else {
		p.AgentChecks = summarizeFleetAgentChecks(node, bootstrap, claimed, h.service.now())
		if bootstrap.AgentProfileName != node.AgentProfileName {
			p.AgentError = "bootstrap profile does not match node"
		}
	}
	if inspector, ok := h.service.sandbox.(runtimeConfigurationInspector); ok {
		config, err := inspector.InspectConfiguration(node)
		if err != nil {
			p.RuntimeError = fleetEvidenceError(err)
		} else {
			p.Runtime = &config
		}
	} else {
		p.RuntimeError = "runtime configuration unavailable"
	}
	if h.forwarder != nil {
		h.forwarder.mu.RLock()
		p.MetricsEpoch = h.forwarder.metricsEpoch
		if history := h.forwarder.metricsHistory[node.ID]; history != nil {
			now := h.forwarder.nowUTC()
			// Read only the selected minute, not the whole hour or every node's
			// history. Values are copied across the forwarder's lock boundary.
			for i := len(history.points) - 1; i >= 0 && len(p.History) < 60; i-- {
				point := history.points[(history.head+i)%len(history.points)]
				if !point.SampledAt.After(now.Add(-time.Minute)) {
					break
				}
				if point.SampledAt.After(now) {
					continue
				}
				p.History = append(p.History, cloneMetricPoint(point))
			}
			slices.Reverse(p.History)
		}
		h.forwarder.mu.RUnlock()
	}
	return p
}
