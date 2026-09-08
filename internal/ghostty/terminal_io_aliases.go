//go:build darwin || linux

package ghostty

import "github.com/brianrackle/codelima/internal/terminalio"

type ghosttyPTYWriter = terminalio.Writer

var (
	newGhosttyPTYWriter         = terminalio.NewWriter
	ghosttyPTYFileDescriptor    = terminalio.Descriptor
	ghosttyReadPTY              = terminalio.Read
	ghosttyWriteAllToPTY        = terminalio.WriteAll
	waitGhosttyPTYWritable      = terminalio.WaitWritable
	isGhosttyPTYWouldBlockError = terminalio.IsWouldBlock
	waitGhosttyPTYReadable      = terminalio.WaitReadable
	isGhosttyPTYClosedReadError = terminalio.IsClosedReadError
	shutdownTerminalProcess     = terminalio.Shutdown
)
