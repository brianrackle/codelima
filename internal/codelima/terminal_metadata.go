package codelima

import "github.com/brianrackle/codelima/internal/codelima/daemon"

// TerminalMetadata is copied from native effects. It is presentation state,
// never authority to open a path, execute a command, or notify the host.
type TerminalMetadata = daemon.TerminalMetadata
