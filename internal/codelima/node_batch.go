package codelima

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	nodeBatchLimit       = 64
	nodeBatchParallelism = 4
	nodeOperationLimit   = 16
)

// Targets contain the canonical identity and name the user reviewed. A changed
// name or missing identity rejects the complete batch before any member runs.
type NodeBatchTarget struct {
	NodeID   string `json:"node_id"`
	NodeSlug string `json:"node_slug"`
	Kind     string `json:"kind"`
}

type NodeBatchMember struct {
	NodeBatchTarget
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
}

type NodeBatchState struct {
	Members     []NodeBatchMember `json:"members,omitempty"`
	Total       int               `json:"total"`
	Queued      int               `json:"queued"`
	Running     int               `json:"running"`
	Succeeded   int               `json:"succeeded"`
	Failed      int               `json:"failed"`
	Canceled    int               `json:"canceled"`
	Interrupted int               `json:"interrupted"`
}

func (b *NodeBatchState) recount() {
	b.Total, b.Queued, b.Running, b.Succeeded, b.Failed, b.Canceled, b.Interrupted = len(b.Members), 0, 0, 0, 0, 0, 0
	for _, member := range b.Members {
		switch member.State {
		case "queued":
			b.Queued++
		case "running", "canceling":
			b.Running++
		case "succeeded":
			b.Succeeded++
		case "failed":
			b.Failed++
		case "canceled":
			b.Canceled++
		case "interrupted":
			b.Interrupted++
		}
	}
}

func (b *NodeBatchState) summary() string {
	return fmt.Sprintf("%d succeeded · %d failed · %d canceled · %d interrupted · %d running · %d queued", b.Succeeded, b.Failed, b.Canceled, b.Interrupted, b.Running, b.Queued)
}

func (m *nodeOperations) prepareBatch(request NodeOperationRequest) (*NodeBatchState, []string, error) {
	if request.Node != "" || len(request.Batch) == 0 || len(request.Batch) > nodeBatchLimit {
		return nil, nil, invalidArgument("batch requires 1–64 captured targets and no single node", nil)
	}
	batch := &NodeBatchState{}
	var keys []string
	seen := make(map[string]bool, len(request.Batch))
	for _, target := range request.Batch {
		switch target.Kind {
		case "start", "retry", "restart", "stop", "delete":
		default:
			return nil, nil, invalidArgument("unsupported batch action", nil)
		}
		id, err := uuid.Parse(target.NodeID)
		if err != nil || id.String() != target.NodeID || target.NodeSlug == "" {
			return nil, nil, invalidArgument("batch targets require canonical node IDs and names", nil)
		}
		if seen[target.NodeID] {
			return nil, nil, invalidArgument("batch contains a duplicate node", nil)
		}
		seen[target.NodeID] = true
		node, err := m.service.store.NodeByID(target.NodeID)
		if err != nil {
			return nil, nil, err
		}
		if node.Slug != target.NodeSlug || node.DeletedAt != nil {
			return nil, nil, preconditionFailed("batch target changed; review the nodes again", map[string]any{"node_id": target.NodeID})
		}
		keys = append(keys, "node:"+node.ID, "slug:"+node.Slug)
		batch.Members = append(batch.Members, NodeBatchMember{NodeBatchTarget: target, OperationID: newID(), State: "queued"})
	}
	batch.recount()
	return batch, keys, nil
}

// The root receipt is the atomic admission boundary. Each member's child ID is
// committed there before any child receipt or side effect can exist. Child
// receipts retain the ordinary phases/output and are durable before execution.
func (m *nodeOperations) runBatch(ctx context.Context, operation *runningNodeOperation) {
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		m.mu.Lock()
		batch := operation.record.Batch
		changed := false
		for i := range batch.Members {
			member := &batch.Members[i]
			if child := m.records[member.OperationID]; child != nil {
				state, message := child.record.State, boundedOperationText(child.record.Error, 512)
				if member.State != state || member.Error != message {
					member.State, member.Error, changed = state, message, true
				}
			} else if member.State == "queued" && ctx.Err() != nil {
				member.State, member.Error, changed = "canceled", context.Cause(ctx).Error(), true
				if errors.Is(context.Cause(ctx), errNodeOperationsClosed) {
					member.State = "interrupted"
				}
			}
		}
		batch.recount()
		if ctx.Err() == nil {
			active := m.executingCountLocked()
			for i := range batch.Members {
				member := &batch.Members[i]
				if member.State != "queued" || batch.Running >= nodeBatchParallelism || active >= nodeOperationLimit {
					continue
				}
				request := NodeOperationRequest{ID: member.OperationID, Kind: member.Kind, Node: member.NodeID, Origin: operation.record.Request.Origin}
				record := NodeOperation{ID: request.ID, Request: request, BatchID: operation.record.ID, NodeID: member.NodeID, NodeSlug: member.NodeSlug, State: "running", CreatedAt: m.service.now(), Phases: initialNodeOperationPhases(member.Kind)}
				changed = true
				if err := m.persist(record); err != nil {
					member.State, member.Error = "failed", boundedOperationText(err.Error(), 512)
					continue
				}
				childCtx, cancel := context.WithCancelCause(ctx)
				child := &runningNodeOperation{record: record, cancel: cancel}
				m.records[record.ID] = child
				member.State = "running"
				batch.Running++
				active++
				m.workers.Add(1)
				go m.run(childCtx, child)
			}
		}
		batch.recount()
		done := batch.Queued+batch.Running == 0
		if done {
			finished := m.service.now()
			operation.record.State, operation.record.FinishedAt = "succeeded", &finished
			if batch.Succeeded != batch.Total {
				operation.record.State, operation.record.Error = "failed", batch.summary()
			}
			switch {
			case errors.Is(context.Cause(ctx), errNodeOperationCanceled):
				operation.record.State = "canceled"
			case errors.Is(context.Cause(ctx), errNodeOperationsClosed):
				operation.record.State, operation.record.FinishedAt = "interrupted", nil
			}
			operation.cancel = nil
			changed = true
		}
		if changed {
			if err := m.persist(operation.record); err != nil {
				// The accepted membership and child receipts already exist. A
				// recovery reprojects those children without replaying mutations.
				m.service.log().Error("batch progress could not be persisted", "operation", operation.record.ID, "error", err)
				if done {
					operation.record.State, operation.record.Error = "failed", err.Error()
				}
			}
		}
		m.mu.Unlock()
		if changed && m.onChange != nil {
			m.onChange()
		}
		if done {
			return
		}
		// Poll retained member state, including explicit per-child cancellation.
		// A canceled root still waits for active children to finish their cleanup.
		<-tick.C
	}
}

func (m *nodeOperations) executingCountLocked() int {
	count := 0
	for _, operation := range m.records {
		if operation.cancel != nil && operation.record.Batch == nil {
			count++
		}
	}
	return count
}

// A daemon restart never guesses whether an interrupted mutation is safe to
// repeat. Completed children are retained; unstarted and uncertain members are
// interrupted. The user can review and explicitly submit a new batch.
func (m *nodeOperations) recoverBatches() error {
	for _, operation := range m.records {
		batch := operation.record.Batch
		if batch == nil || operation.record.State != "interrupted" {
			continue
		}
		for i := range batch.Members {
			member := &batch.Members[i]
			if child := m.records[member.OperationID]; child != nil && child.record.BatchID == operation.record.ID && child.record.NodeID == member.NodeID {
				member.State, member.Error = child.record.State, boundedOperationText(child.record.Error, 512)
			} else if member.State == "queued" || member.State == "running" || member.State == "canceling" {
				member.State, member.Error = "interrupted", errNodeOperationsClosed.Error()
			}
		}
		batch.recount()
		if err := m.persist(operation.record); err != nil {
			return err
		}
	}
	return nil
}

func batchSummary(record NodeOperation) *NodeBatchState {
	if record.Batch == nil {
		return nil
	}
	summary := *record.Batch
	summary.Members = nil
	if record.State == "running" || record.State == "canceling" {
		for _, member := range record.Batch.Members {
			member.Error = ""
			member.NodeSlug = boundedOperationText(member.NodeSlug, 128)
			summary.Members = append(summary.Members, member)
		}
	}
	return &summary
}
