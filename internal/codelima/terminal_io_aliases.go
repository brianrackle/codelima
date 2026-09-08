//go:build darwin || linux

package codelima

import "github.com/brianrackle/codelima/internal/terminalio"

type ghosttyPTYWriter = terminalio.Writer

const (
	terminalPTYQueueMaxBytes     = terminalio.QueueMaxBytes
	terminalShutdownSignalGrace  = terminalio.ShutdownSignalGrace
	terminalShutdownReapDeadline = terminalio.ShutdownReapDeadline
	terminalShutdownPollInterval = terminalio.ShutdownPollInterval
)

var (
	errTerminalInputQueueFull   = terminalio.ErrQueueFull
	newGhosttyPTYWriter         = terminalio.NewWriter
	ghosttyPTYFileDescriptor    = terminalio.Descriptor
	ghosttyReadPTY              = terminalio.Read
	waitGhosttyPTYWritable      = terminalio.WaitWritable
	isGhosttyPTYWouldBlockError = terminalio.IsWouldBlock
	waitGhosttyPTYReadable      = terminalio.WaitReadable
	shutdownTerminalProcess     = terminalio.Shutdown
	signalTerminalProcessGroup  = terminalio.SignalProcessGroup
)
