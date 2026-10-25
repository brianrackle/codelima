package codelima

import (
	"strings"
	"time"
)

// A selected-node projection of frozen execution evidence, not a guest probe.
// Only the two known version commands prove login-user agent execution. A
// custom profile's configured check is useful but makes no such claim.
type fleetAgentCheck struct {
	Name       string             `json:"name"`
	State      BootstrapStepState `json:"state"`
	LoginUser  bool               `json:"login_user,omitempty"`
	Inherited  bool               `json:"inherited,omitempty"`
	FinishedAt *time.Time         `json:"finished_at,omitempty"`
	ExitCode   *int               `json:"exit_code,omitempty"`
}

func fleetAgentName(profile string) string {
	if profile == "codex-cli" {
		return "codex"
	}
	return boundedOperationText(plainLogText(profile), 48)
}

func configuredFleetAgentChecks(node Node) []fleetAgentCheck {
	var checks []fleetAgentCheck
	add := func(name string) {
		if name == "" {
			return
		}
		for _, check := range checks {
			if check.Name == name {
				return
			}
		}
		checks = append(checks, fleetAgentCheck{Name: name, State: BootstrapStepUnrecorded})
	}
	for _, environment := range node.Environments {
		if environment == "codex" || environment == "claude-code" {
			add(environment)
		}
	}
	add(fleetAgentName(node.AgentProfileName))
	return checks
}

func summarizeFleetAgentChecks(node Node, bootstrap BootstrapState, claimed bool, now time.Time) []fleetAgentCheck {
	checks := configuredFleetAgentChecks(node)
	if bootstrap.AgentProfileName != node.AgentProfileName {
		return checks
	}
	// A command edit invalidates historical success, including legacy records
	// that only know bootstrap completed. This repairs only the local copy.
	bootstrap.ensureSteps()
	for _, step := range bootstrap.Steps {
		name, loginUser := "", true
		switch strings.TrimSpace(step.Command) {
		case defaultCodexValidationCommand:
			name = "codex"
		case defaultClaudeCodeValidationCommand:
			name = "claude-code"
		default:
			if step.Kind == "validation" {
				name, loginUser = fleetAgentName(bootstrap.AgentProfileName), false
			}
		}
		if name == "" {
			continue
		}
		check := projectFleetAgentCheck(name, loginUser, node.ID, step, claimed, now)
		index := -1
		for i, existing := range checks {
			if existing.Name == name {
				index = i
				break
			}
		}
		if index < 0 {
			// At most two known agents plus the node's single custom profile.
			checks = append(checks, check)
			continue
		}
		previous := checks[index].State
		// Later checks normally supersede earlier ones, even when a new
		// validation is pending. An unattempted suffix must not conceal the
		// actual failed, interrupted, canceled or active prerequisite check.
		if check.State == BootstrapStepPending && (previous == BootstrapStepFailed || previous == BootstrapStepCanceled || previous == BootstrapStepInterrupted || previous == BootstrapStepRunning) {
			continue
		}
		checks[index] = check
	}
	return checks
}

func projectFleetAgentCheck(name string, loginUser bool, nodeID string, step BootstrapStep, claimed bool, now time.Time) fleetAgentCheck {
	check := fleetAgentCheck{Name: name, LoginUser: loginUser, State: BootstrapStepUnrecorded}
	if step.State == BootstrapStepPending {
		check.State = BootstrapStepPending
		return check
	}
	if step.Attempts == 0 || step.AttemptID == "" || step.ExecutionNodeID == "" || step.StartedAt == nil || step.StartedAt.IsZero() || step.StartedAt.After(now) {
		return check
	}
	if step.FinishedAt != nil && (step.FinishedAt.Before(*step.StartedAt) || step.FinishedAt.After(now)) {
		return check
	}
	switch step.State {
	case BootstrapStepSucceeded:
		if step.FinishedAt == nil || step.ExitCode == nil || *step.ExitCode != 0 || step.Error != "" {
			return check
		}
	case BootstrapStepRunning, BootstrapStepFailed, BootstrapStepCanceled, BootstrapStepInterrupted:
	default:
		return check
	}
	check.State, check.Inherited = step.State, step.ExecutionNodeID != nodeID
	if check.State == BootstrapStepRunning && (!claimed || check.Inherited) {
		check.State = BootstrapStepInterrupted
	}
	if step.FinishedAt != nil {
		finished := *step.FinishedAt
		check.FinishedAt = &finished
	}
	if step.ExitCode != nil {
		exit := *step.ExitCode
		check.ExitCode = &exit
	}
	return check
}
