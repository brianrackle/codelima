package codelima

import "github.com/brianrackle/codelima/internal/codelima/daemon"

// TerminalMetadata includes native effects and daemon activity observations.
// It never authorizes paths, commands or host notifications.
type TerminalMetadata = daemon.TerminalMetadata
