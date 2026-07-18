package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const (
	MaxMessageSize  = 1 << 20
	ProtocolVersion = 2
	SessionVersion  = 2
)

type Request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type Event struct {
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

type RPCError struct {
	Category string         `json:"category"`
	Message  string         `json:"message"`
	Code     int            `json:"code,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func Error(category, message string, code int, fields map[string]any) error {
	return &RPCError{Category: category, Message: message, Code: code, Fields: fields}
}

type HelloParams struct {
	Version      string   `json:"version"`
	Protocol     int      `json:"protocol"`
	Capabilities []string `json:"capabilities,omitempty"`
	WantInput    bool     `json:"want_input,omitempty"`
}

type HelloResult struct {
	Version    string `json:"version"`
	Protocol   int    `json:"protocol"`
	ClientID   string `json:"client_id"`
	InputOwner bool   `json:"input_owner"`
}

type Status struct {
	Running       bool      `json:"running" yaml:"running"`
	PID           int       `json:"pid" yaml:"pid"`
	Version       string    `json:"version" yaml:"version"`
	Protocol      int       `json:"protocol" yaml:"protocol"`
	Identity      string    `json:"identity" yaml:"identity"`
	StartedAt     time.Time `json:"started_at" yaml:"started_at"`
	TerminalCount int       `json:"terminal_count" yaml:"terminal_count"`
	InputOwner    string    `json:"input_owner,omitempty" yaml:"input_owner,omitempty"`
}

type Identity struct {
	Token     string    `json:"token"`
	Version   string    `json:"version"`
	Protocol  int       `json:"protocol"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

type Paths struct {
	Dir      string
	Socket   string
	Client   string
	PID      string
	Identity string
	Session  string
	Lock     string
}

func HomePaths(home string) Paths {
	dir := filepath.Join(home, "_daemon")
	return Paths{
		Dir:      dir,
		Socket:   filepath.Join(dir, "daemon.sock"),
		Client:   filepath.Join(dir, "client.sock"),
		PID:      filepath.Join(dir, "daemon.pid"),
		Identity: filepath.Join(dir, "daemon.identity"),
		Session:  filepath.Join(dir, "session.json"),
		Lock:     filepath.Join(home, "_locks", "daemon.lock"),
	}
}

func DecodeResult(response Response, target any) error {
	if response.Error != nil {
		return response.Error
	}
	if target == nil || len(response.Result) == 0 || string(response.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		return fmt.Errorf("decode daemon result: %w", err)
	}
	return nil
}

func AsRPCError(err error) *RPCError {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &RPCError{Category: "Internal", Message: err.Error(), Code: 7}
}

type TerminalState struct {
	TerminalID string    `json:"terminal_id" yaml:"terminal_id"`
	TabID      string    `json:"tab_id" yaml:"tab_id"`
	Target     string    `json:"target" yaml:"target"`
	Kind       string    `json:"kind" yaml:"kind"`
	Label      string    `json:"label,omitempty" yaml:"label,omitempty"`
	CWD        string    `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	Argv       []string  `json:"argv" yaml:"argv"`
	CreatedAt  time.Time `json:"created_at" yaml:"created_at"`
	Cols       int       `json:"cols" yaml:"cols"`
	Rows       int       `json:"rows" yaml:"rows"`
}

type Session struct {
	Version   int             `json:"version"`
	Terminals []TerminalState `json:"terminals"`
}

type SnapshotCell struct {
	Grapheme      string `json:"grapheme"`
	Width         int    `json:"width"`
	FG            uint32 `json:"fg"`
	BG            uint32 `json:"bg"`
	FGDefault     bool   `json:"fg_default"`
	BGDefault     bool   `json:"bg_default"`
	Bold          bool   `json:"bold,omitempty"`
	Faint         bool   `json:"faint,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	Inverse       bool   `json:"inverse,omitempty"`
	Invisible     bool   `json:"invisible,omitempty"`
	Blink         bool   `json:"blink,omitempty"`
	Hyperlink     string `json:"hyperlink,omitempty"`
}

type Snapshot struct {
	Cols          int            `json:"cols"`
	Rows          int            `json:"rows"`
	Cells         []SnapshotCell `json:"cells"`
	CursorX       int            `json:"cursor_x"`
	CursorY       int            `json:"cursor_y"`
	CursorVisible bool           `json:"cursor_visible"`
	Generation    uint64         `json:"generation"`
	CapturesMouse bool           `json:"captures_mouse"`
}

type HandoffRuntime struct {
	TerminalID string `json:"terminal_id"`
	ChildPID   int    `json:"child_pid"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
	Replay     []byte `json:"replay"`
}

type HandoffManifest struct {
	Version       int              `json:"version"`
	BinaryVersion string           `json:"binary_version"`
	Token         string           `json:"token"`
	Session       Session          `json:"session"`
	Runtimes      []HandoffRuntime `json:"runtimes"`
}

type HandoffMessage struct {
	Type        string   `json:"type"`
	Token       string   `json:"token,omitempty"`
	TerminalIDs []string `json:"terminal_ids,omitempty"`
	PID         int      `json:"pid,omitempty"`
	Error       string   `json:"error,omitempty"`
}
