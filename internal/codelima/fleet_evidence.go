package codelima

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const fleetEvidenceNodeLimit = 128

type fleetEvidenceRequest struct {
	Nodes      []string `json:"nodes"`
	RecentNode string   `json:"recent_node,omitempty"`
}

// Fleet evidence carries bounded facts, never command bodies or output. The
// selected node's recent records are independent of the Activity page cursor.
type fleetEvidenceSnapshot struct {
	Preview        *fleetPreviewEvidence        `json:"preview,omitempty"`
	ObservedAt     time.Time                    `json:"observed_at"`
	Nodes          map[string]fleetNodeEvidence `json:"nodes"`
	RecentNode     string                       `json:"recent_node,omitempty"`
	Recent         []ActivityRecord             `json:"recent,omitempty"`
	RecentError    string                       `json:"recent_error,omitempty"`
	LifecycleError string                       `json:"lifecycle_error,omitempty"`
}

type fleetNodeEvidence struct {
	NodeUpdatedAt time.Time               `json:"node_updated_at"`
	ClaimState    string                  `json:"claim_state,omitempty"`
	Start         *fleetLifecycleEvidence `json:"start,omitempty"`
	Step          *fleetStepEvidence      `json:"step,omitempty"`
	StoppedAt     *time.Time              `json:"stopped_at,omitempty"`
	Error         string                  `json:"error,omitempty"`
}

type fleetLifecycleEvidence struct {
	ID         string                 `json:"id"`
	State      string                 `json:"state"`
	Phase      string                 `json:"phase,omitempty"`
	Progress   *NodeOperationProgress `json:"progress,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	FinishedAt *time.Time             `json:"finished_at,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

type fleetStepEvidence struct {
	Index          int                `json:"index"`
	Total          int                `json:"total"`
	Kind           string             `json:"kind"`
	Source         string             `json:"source,omitempty"`
	State          BootstrapStepState `json:"state"`
	ExitCode       *int               `json:"exit_code,omitempty"`
	DurationMillis *int64             `json:"duration_ms,omitempty"`
	StartedAt      *time.Time         `json:"started_at,omitempty"`
}

func (h *daemonHost) handleFleetEvidence(ctx context.Context, raw json.RawMessage) (any, error) {
	var request fleetEvidenceRequest
	if err := json.Unmarshal(raw, &request); err != nil || len(request.Nodes) > fleetEvidenceNodeLimit {
		return nil, toDaemonError(invalidArgument("fleet evidence requires at most 128 node IDs", nil))
	}
	ids := make(map[string]bool, len(request.Nodes))
	for _, id := range request.Nodes {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return nil, toDaemonError(invalidArgument("fleet evidence requires canonical node IDs", nil))
		}
		ids[id] = true
	}
	if request.RecentNode != "" && !ids[request.RecentNode] {
		return nil, toDaemonError(invalidArgument("recent node must be included in fleet evidence IDs", nil))
	}
	// Opening the operation/journal owners may recover persisted state. Share
	// their daemon replacement barrier even though this is a query-lane RPC.
	if err := h.enterTerminalMutation(); err != nil {
		return nil, err
	}
	defer h.leaveTerminalMutation()
	result := fleetEvidenceSnapshot{ObservedAt: h.service.now(), Nodes: make(map[string]fleetNodeEvidence, len(ids)), RecentNode: request.RecentNode}
	var records []NodeOperation
	operations, err := h.nodeOperations()
	if err != nil {
		result.LifecycleError = fleetEvidenceError(err)
	} else {
		records = operations.List()
	}
	for id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, toDaemonError(err)
		}
		node, err := h.service.store.NodeByID(id)
		if err != nil || node.DeletedAt != nil {
			if err == nil {
				err = notFound("node deleted", nil)
			}
			result.Nodes[id] = fleetNodeEvidence{Error: fleetEvidenceError(err)}
			continue
		}
		evidence := summarizeFleetLifecycle(id, records)
		evidence.NodeUpdatedAt = node.UpdatedAt
		if result.LifecycleError != "" {
			evidence.Error = result.LifecycleError
		}
		if nodeStatusInFlight(node.Status) {
			claimed, err := nodeOperationClaimed(h.service.cfg.MetadataRoot, id)
			switch {
			case err != nil:
				evidence.Error = fleetEvidenceError(err)
			case claimed:
				evidence.ClaimState = "active"
			default:
				evidence.ClaimState = "unclaimed"
			}
		}
		if evidence.Error == "" && (node.Status == NodeStatusFailed || nodeStatusInFlight(node.Status) || evidence.Start != nil && (evidence.Start.Phase == "bootstrap" || evidence.Start.Phase == "validation")) {
			bootstrap, err := h.service.store.LoadBootstrapState(id)
			if err != nil {
				evidence.Error = fleetEvidenceError(err)
			} else {
				evidence.Step = summarizeFleetStep(id, bootstrap, evidence.Start)
			}
		}
		result.Nodes[id] = evidence
		if id == request.RecentNode {
			result.Preview = h.fleetPreview(node, evidence.ClaimState == "active")
		}
	}
	if request.RecentNode != "" {
		result.RecentError = result.LifecycleError
		selected := slices.DeleteFunc(slices.Clone(records), func(r NodeOperation) bool { return r.NodeID != request.RecentNode })
		var messages []ActivityMessage
		journal, err := h.activityJournal()
		if err != nil {
			result.RecentError = strings.TrimSpace(result.RecentError + " " + fleetEvidenceError(err))
		} else {
			messages, _ = journal.Snapshot()
			messages = slices.DeleteFunc(messages, func(m ActivityMessage) bool { return m.NodeID != request.RecentNode })
		}
		page, err := buildActivityPage(selected, messages, "", 3)
		if err != nil {
			result.RecentError = fleetEvidenceError(err)
		} else {
			result.Recent = page.History
		}
		for i := range result.Recent {
			result.Recent[i].Text = boundedOperationText(plainLogText(result.Recent[i].Text), 256)
		}
	}
	return result, nil
}

func fleetEvidenceError(err error) string {
	return boundedOperationText(plainLogText(err.Error()), 512)
}

func summarizeFleetLifecycle(nodeID string, records []NodeOperation) fleetNodeEvidence {
	var result fleetNodeEvidence
	for _, record := range records {
		if record.NodeID != nodeID {
			continue
		}
		if record.Request.Kind == "stop" && record.State == "succeeded" && record.FinishedAt != nil && (result.StoppedAt == nil || record.FinishedAt.After(*result.StoppedAt)) {
			finished := *record.FinishedAt
			result.StoppedAt = &finished
		}
		if !record.Request.startsNode() || result.Start != nil && (record.CreatedAt.Before(result.Start.CreatedAt) || record.CreatedAt.Equal(result.Start.CreatedAt) && record.ID <= result.Start.ID) {
			continue
		}
		result.Start = &fleetLifecycleEvidence{ID: record.ID, State: record.State, Phase: record.CurrentPhase, CreatedAt: record.CreatedAt, Error: boundedOperationText(plainLogText(record.Error), 512)}
		if record.Progress != nil {
			p := *record.Progress
			p.Detail = boundedOperationText(plainLogText(p.Detail), 256)
			result.Start.Progress = &p
		}
		if record.FinishedAt != nil {
			finished := *record.FinishedAt
			result.Start.FinishedAt = &finished
		}
	}
	if result.Start != nil && result.StoppedAt != nil && !result.Start.CreatedAt.Before(*result.StoppedAt) {
		result.StoppedAt = nil
	}
	return result
}

func summarizeFleetStep(nodeID string, bootstrap BootstrapState, latest *fleetLifecycleEvidence) *fleetStepEvidence {
	// A newer boot/import failure cannot be attributed to an older failed
	// bootstrap. Legacy records without a retained receipt remain inspectable.
	if latest != nil && latest.Phase != "bootstrap" && latest.Phase != "validation" {
		return nil
	}
	index, total := 0, 0
	for _, step := range bootstrap.Steps {
		if step.Kind == "bootstrap" {
			total++
		}
	}
	for _, step := range bootstrap.Steps {
		if step.Kind == "bootstrap" {
			index++
		}
		if step.State != BootstrapStepFailed && step.State != BootstrapStepRunning && step.State != BootstrapStepInterrupted {
			continue
		}
		if step.Kind != "bootstrap" && step.Kind != "validation" || latest != nil && step.Kind != latest.Phase {
			continue
		}
		if step.ExecutionNodeID != "" && step.ExecutionNodeID != nodeID {
			continue
		}
		if latest != nil && (step.StartedAt == nil || step.StartedAt.Before(latest.CreatedAt)) {
			continue
		}
		result := &fleetStepEvidence{Index: index, Total: total, Kind: step.Kind, Source: boundedOperationText(plainLogText(coalesce(step.SourceName, step.SourceKind)), 128), State: step.State}
		if step.Kind == "validation" {
			result.Index, result.Total = 1, 1
		}
		if step.ExitCode != nil {
			exit := *step.ExitCode
			result.ExitCode = &exit
		}
		if step.DurationMillis != nil && *step.DurationMillis >= 0 {
			duration := *step.DurationMillis
			result.DurationMillis = &duration
		}
		if step.StartedAt != nil {
			started := *step.StartedAt
			result.StartedAt = &started
		}
		return result
	}
	return nil
}
