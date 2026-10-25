package codelima

import (
	"context"
	"fmt"
	"time"
)

// Phase times describe this operation only. Skipped work and unrecorded legacy
// operations have no invented duration; bootstrap attempts remain authoritative
// for individual command execution and provenance.
type NodeOperationPhase struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	State      string     `json:"state"`
	Detail     string     `json:"detail,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func initialNodeOperationPhases(kind string) []NodeOperationPhase {
	if kind == "test-environment" {
		phases := append(initialNodeOperationPhases("create"), initialNodeOperationPhases("start")...)
		return append(phases, NodeOperationPhase{ID: "cleanup", Label: "Delete test VM", State: "pending"})
	}
	if kind == "start" || kind == "retry" {
		return []NodeOperationPhase{
			{ID: "prepare", Label: "Prepare", State: "pending"},
			{ID: "boot", Label: "Boot", State: "pending"},
			{ID: "credentials", Label: "Host credentials", State: "pending"},
			{ID: "workspace", Label: "Workspace", State: "pending"},
			{ID: "bootstrap", Label: "Bootstrap", State: "pending"},
			{ID: "validation", Label: "Validate agent", State: "pending"},
			{ID: "ready", Label: "Ready", State: "pending"},
		}
	}
	labels := map[string]string{"create": "Create VM", "stop": "Stop VM", "delete": "Delete VM", "clone": "Clone VM"}
	if label := labels[kind]; label != "" {
		return []NodeOperationPhase{{ID: kind, Label: label, State: "pending"}}
	}
	return nil
}

func (s *Service) advanceOperationPhase(id, detail string, skipped bool) error {
	if s.operationPhase == nil {
		return nil
	}
	if err := s.operationPhase(id, detail, skipped); err != nil {
		return &nodeOperationProgressError{err: fmt.Errorf("record lifecycle phase %s: %w", id, err)}
	}
	return nil
}

type nodeOperationProgressError struct{ err error }

func (e *nodeOperationProgressError) Error() string { return e.err.Error() }
func (e *nodeOperationProgressError) Unwrap() error { return e.err }

// Advancing is a service execution boundary: the previous phase's work has
// returned successfully. Repeating the current phase updates detail without
// restarting its clock. Persist before the next side effect begins.
func (m *nodeOperations) advancePhase(operation *runningNodeOperation, id, detail string, skipped bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if operation.record.State != "running" && !(id == "cleanup" && operation.record.EnvironmentTest != nil && operation.record.State == "canceling") {
		return context.Canceled
	}
	index := -1
	for i, phase := range operation.record.Phases {
		if phase.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("unknown operation phase %q", id)
	}
	now := m.service.now()
	for i := range operation.record.Phases {
		previous := &operation.record.Phases[i]
		if i != index && previous.State == "running" {
			previous.State, previous.FinishedAt = "succeeded", &now
		}
	}
	phase := &operation.record.Phases[index]
	phase.Detail = boundedOperationText(detail, 1024)
	if skipped {
		phase.State, phase.StartedAt, phase.FinishedAt = "skipped", nil, nil
	} else {
		if phase.State != "running" {
			phase.StartedAt = &now
		}
		phase.State, phase.FinishedAt = "running", nil
	}
	operation.record.CurrentPhase = id
	return m.persist(operation.record)
}

func finishNodeOperationPhases(record *NodeOperation, state string, finished *time.Time) {
	for i := range record.Phases {
		phase := &record.Phases[i]
		if phase.State != "running" {
			continue
		}
		phase.State, phase.FinishedAt = state, finished
		if state == "interrupted" {
			phase.FinishedAt = nil
		}
	}
}
