package terminal

// TerminalKind classifies what a managed terminal runs. It is the discriminator
// for the single shell-launch contract (Service.TerminalLaunchSpec): a
// NodeHostShell is an interactive login shell rooted at the node's host
// directory, while a NodeShell re-enters the codelima binary as
// `codelima shell <node>` so that every managed terminal enters the VM through
// CodeLima — the invariant TMUX_PLAN and AGENT_MONITORING_PLAN both depend on.
// Room is deliberately left for a future AgentShell (IMPROVEMENT_PLAN Part E,
// Track 1 §1.1).
type TerminalKind int

const (
	// NodeHostShell is an interactive login shell on the host, rooted at a
	// node's directory.
	NodeHostShell TerminalKind = iota
	// NodeShell re-enters the codelima binary to open a shell inside a node's VM.
	NodeShell
)

// ProjectHostShell is a source-compatibility alias for pre-v3 callers. New
// protocol values and runtime state always use node-host-shell.
const ProjectHostShell = NodeHostShell

// String renders the kind for logs and error messages. Unknown values render as
// "unknown".
func (k TerminalKind) String() string {
	switch k {
	case NodeHostShell:
		return "node-host-shell"
	case NodeShell:
		return "node-shell"
	default:
		return "unknown"
	}
}
