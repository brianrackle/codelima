package daemon

// TerminalAttachParams binds a raw output cursor to a single physical client.
// After acknowledges bytes written to the terminal by the previous read.
type TerminalAttachParams struct {
	TerminalID   string `json:"terminal_id"`
	AttachmentID string `json:"attachment_id,omitempty"`
	After        uint64 `json:"after,omitempty"`
}

type TerminalAttachResult struct {
	AttachmentID string `json:"attachment_id"`
	Data         []byte `json:"data,omitempty"`
	Through      uint64 `json:"through"`
	Exited       bool   `json:"exited,omitempty"`
}
