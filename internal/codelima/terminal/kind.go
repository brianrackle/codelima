package terminal

// TerminalKind classifies what a managed terminal runs. It is the discriminator
// for the single shell-launch contract (Service.TerminalLaunchSpec): a
// ProjectHostShell is an interactive login shell rooted at the project's host
// workspace, while a NodeShell re-enters the codelima binary as
// `codelima shell <node>` so that every managed terminal enters the VM through
// CodeLima — the invariant TMUX_PLAN and AGENT_MONITORING_PLAN both depend on.
// Room is deliberately left for a future AgentShell (IMPROVEMENT_PLAN Part E,
// Track 1 §1.1).
type TerminalKind int

const (
	// ProjectHostShell is an interactive login shell on the host, rooted at a
	// project's workspace directory.
	ProjectHostShell TerminalKind = iota
	// NodeShell re-enters the codelima binary to open a shell inside a node's VM.
	NodeShell
)

// String renders the kind for logs and error messages. Unknown values render as
// "unknown".
func (k TerminalKind) String() string {
	switch k {
	case ProjectHostShell:
		return "project-host-shell"
	case NodeShell:
		return "node-shell"
	default:
		return "unknown"
	}
}
