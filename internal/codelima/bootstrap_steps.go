package codelima

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type BootstrapStepState string

const (
	BootstrapStepPending     BootstrapStepState = "pending"
	BootstrapStepRunning     BootstrapStepState = "running"
	BootstrapStepSucceeded   BootstrapStepState = "succeeded"
	BootstrapStepFailed      BootstrapStepState = "failed"
	BootstrapStepCanceled    BootstrapStepState = "canceled"
	BootstrapStepInterrupted BootstrapStepState = "interrupted"
	BootstrapStepUnrecorded  BootstrapStepState = "unrecorded"
)

// BootstrapStep keeps the frozen origin separate from evidence of the most
// recent execution. A clone inherits the disk and the original execution's
// node ID; it must not claim that the child executed those commands itself.
type BootstrapStep struct {
	Kind            string             `json:"kind" yaml:"kind"`
	SourceKind      string             `json:"source_kind" yaml:"source_kind"`
	SourceID        string             `json:"source_id,omitempty" yaml:"source_id,omitempty"`
	SourceName      string             `json:"source_name" yaml:"source_name"`
	Command         string             `json:"command" yaml:"command"`
	State           BootstrapStepState `json:"state" yaml:"state"`
	Attempts        uint64             `json:"attempts" yaml:"attempts"`
	AttemptID       string             `json:"attempt_id,omitempty" yaml:"attempt_id,omitempty"`
	ExecutionNodeID string             `json:"execution_node_id,omitempty" yaml:"execution_node_id,omitempty"`
	StartedAt       *time.Time         `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty" yaml:"finished_at,omitempty"`
	DurationMillis  *int64             `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
	ExitCode        *int               `json:"exit_code,omitempty" yaml:"exit_code,omitempty"`
	Error           string             `json:"error,omitempty" yaml:"error,omitempty"`
}

func appendBootstrapSteps(steps []BootstrapStep, commands []string, kind, sourceKind, sourceID, sourceName string) []BootstrapStep {
	for _, command := range commands {
		if strings.TrimSpace(command) == "" {
			continue
		}
		steps = append(steps, BootstrapStep{Kind: kind, SourceKind: sourceKind, SourceID: sourceID, SourceName: sourceName, Command: command, State: BootstrapStepPending})
	}
	return steps
}

func (s *Service) resolveConfigurationBootstrapSteps(c Configuration) ([]BootstrapStep, error) {
	var steps []BootstrapStep
	for _, slug := range c.Environments {
		env, err := s.store.EnvironmentConfigByIDOrSlug(slug)
		if err != nil {
			return nil, err
		}
		if env.DeletedAt != nil {
			return nil, notFound("environment not found", map[string]any{"query": slug})
		}
		steps = appendBootstrapSteps(steps, env.BootstrapCommands, "bootstrap", "environment", env.ID, env.Slug)
	}
	return appendBootstrapSteps(steps, c.BootstrapCommands, "bootstrap", "template", c.ID, c.Slug), nil
}

// Stored command arrays remain the execution authority for old metadata and
// deliberate operator repairs. Never reuse success evidence after commands
// change, or guess old environment provenance from a current template.
func (b *BootstrapState) ensureSteps() {
	var fallback []BootstrapStep
	fallback = appendBootstrapSteps(fallback, b.InstallCommands, "bootstrap", "agent", "", b.AgentProfileName)
	fallback = appendBootstrapSteps(fallback, b.BootstrapCommands, "bootstrap", "frozen", "", "source unrecorded")
	fallback = appendBootstrapSteps(fallback, []string{b.ValidationCommand}, "validation", "agent", "", b.AgentProfileName)
	matching := len(fallback) == len(b.Steps)
	if matching {
		for i, step := range fallback {
			if step.Kind != b.Steps[i].Kind || step.Command != b.Steps[i].Command {
				matching = false
				break
			}
		}
	}
	if matching {
		return
	}
	if b.Completed {
		for i := range fallback {
			fallback[i].State = BootstrapStepUnrecorded
		}
	}
	b.Steps = fallback
}

func (b *BootstrapState) interruptRunningSteps() {
	for i := range b.Steps {
		step := &b.Steps[i]
		if step.State != BootstrapStepRunning {
			continue
		}
		step.State = BootstrapStepInterrupted
		step.FinishedAt, step.DurationMillis, step.ExitCode = nil, nil, nil
		step.Error = "execution ended before completion was recorded"
	}
}

func (b *BootstrapState) prepareSteps(resume bool) (int, error) {
	b.ensureSteps()
	b.interruptRunningSteps()
	start := 0
	if resume {
		start = -1
		for i, step := range b.Steps {
			if step.State == BootstrapStepFailed || step.State == BootstrapStepInterrupted {
				start = i
				break
			}
		}
		if start < 0 {
			return 0, preconditionFailed("no recorded failed step; start the node to rerun its incomplete bootstrap", nil)
		}
		if b.Completed && b.Steps[start].Kind != "validation" {
			return 0, metadataCorruption("completed bootstrap contains a failed bootstrap step", nil, nil)
		}
		for i := range start {
			if b.Steps[i].Kind == "bootstrap" && b.Steps[i].State != BootstrapStepSucceeded && !b.Completed {
				return 0, preconditionFailed("earlier bootstrap steps lack success records; start the node to rerun the full bootstrap", nil)
			}
		}
	}
	return start, nil
}

// Reset only when guest execution is about to begin. A rejected runtime or
// host-port preflight must preserve the preceding failure for an explicit retry.
func (b *BootstrapState) resetSteps(start int) {
	for i := start; i < len(b.Steps); i++ {
		step := &b.Steps[i]
		if b.Completed && step.Kind == "bootstrap" {
			continue
		}
		step.State = BootstrapStepPending
		step.AttemptID, step.ExecutionNodeID, step.Error = "", "", ""
		step.StartedAt, step.FinishedAt, step.DurationMillis, step.ExitCode = nil, nil, nil, nil
	}
}

func (s *Service) runRecordedBootstrapStep(ctx context.Context, node Node, b *BootstrapState, index int) error {
	step := &b.Steps[index]
	if err := ctx.Err(); err != nil {
		return err
	}
	started := s.now()
	step.State, step.AttemptID, step.ExecutionNodeID = BootstrapStepRunning, newID(), node.ID
	step.Attempts++
	step.StartedAt = &started
	if err := s.withLocks(ctx, nil, []string{node.ID}, func() error {
		if err := s.store.SaveNode(node, *b); err != nil {
			return err
		}
		return s.store.AppendNodeEvent(node.ID, Event{Timestamp: started, Type: "node.bootstrap.step.started", Fields: bootstrapStepEventFields(*step, index)})
	}); err != nil {
		return err
	}
	commandCtx := ctx
	if step.Kind == "bootstrap" {
		var cancel context.CancelFunc
		commandCtx, cancel = context.WithTimeout(ctx, bootstrapGuestCommandTimeout)
		defer cancel()
	}
	phase := fmt.Sprintf("%s step %d · attempt %s", step.Kind, index+1, step.AttemptID)
	runErr := s.runLoggedGuestCommand(commandCtx, node, step.Command, phase)
	finished := s.now()
	duration := max(int64(0), finished.Sub(started).Milliseconds())
	step.FinishedAt, step.DurationMillis = &finished, &duration
	step.State = BootstrapStepSucceeded
	if runErr != nil {
		step.State = BootstrapStepFailed
		if ctx.Err() != nil {
			step.State = BootstrapStepCanceled
		}
		step.Error = runErr.Error()
		if len(step.Error) > 4096 {
			step.Error = strings.ToValidUTF8(step.Error[:4096], "�")
		}
		var exit interface{ ExitCode() int }
		if errors.As(runErr, &exit) && exit.ExitCode() >= 0 {
			code := exit.ExitCode()
			step.ExitCode = &code
		}
	} else {
		code := 0
		step.ExitCode = &code
	}
	// Cancellation must not erase the attempt's outcome. The operation still
	// owns the liveness token while this short, independently bounded write runs.
	saveErr := s.withRollbackLocks(nil, []string{node.ID}, func() error {
		if err := s.store.SaveNode(node, *b); err != nil {
			return err
		}
		fields := bootstrapStepEventFields(*step, index)
		fields["duration_ms"] = duration
		if step.Error != "" {
			fields["error"] = step.Error
		}
		if step.ExitCode != nil {
			fields["exit_code"] = *step.ExitCode
		}
		typeName := "node.bootstrap.step." + string(step.State)
		if step.State == BootstrapStepFailed {
			typeName = "node.bootstrap.failed"
		}
		return s.store.AppendNodeEvent(node.ID, Event{Timestamp: finished, Type: typeName, Fields: fields})
	})
	return errors.Join(runErr, saveErr)
}

func bootstrapStepEventFields(step BootstrapStep, index int) map[string]any {
	return map[string]any{"step": index + 1, "kind": step.Kind, "source_kind": step.SourceKind, "source_id": step.SourceID, "source": step.SourceName, "attempt_id": step.AttemptID, "attempt": step.Attempts}
}
