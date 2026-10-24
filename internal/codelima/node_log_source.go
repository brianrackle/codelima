package codelima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/brianrackle/codelima/internal/terminalstate"
)

type nodeLogSource string

const (
	nodeLogEvents    nodeLogSource = "events"
	nodeLogBootstrap nodeLogSource = "bootstrap"
	nodeLogLima      nodeLogSource = "lima"
	nodeLogSerial    nodeLogSource = "serial"
	nodeLogByteLimit               = 2 << 20
	nodeLogLineLimit               = 5000
)

var nodeLogSources = [...]nodeLogSource{nodeLogEvents, nodeLogBootstrap, nodeLogLima, nodeLogSerial}

type nodeLogLine struct {
	Offset            int64
	Timestamp         time.Time
	Level, Kind, Text string
}

type nodeLogSnapshot struct {
	NodeID             string
	Source             nodeLogSource
	Path               string
	Lines              []nodeLogLine
	Missing, Truncated bool
}

// NodeLogSource reads local diagnostic files without probing or starting Lima.
// It deliberately has a bounded tail; the original path remains available to
// an external pager when the operator needs older output.
func (s *Service) NodeLogSource(ctx context.Context, value string, source nodeLogSource) (nodeLogSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nodeLogSnapshot{}, err
	}
	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return nodeLogSnapshot{}, err
	}
	path, err := s.nodeLogPath(node, source)
	if err != nil {
		return nodeLogSnapshot{}, err
	}
	result := nodeLogSnapshot{NodeID: node.ID, Source: source, Path: path}
	data, offset, missing, err := readNodeLogTail(path, nodeLogByteLimit)
	if err != nil {
		return result, err
	}
	result.Missing, result.Truncated = missing, offset > 0
	for raw := range bytes.SplitSeq(data, []byte{'\n'}) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		line := nodeLogLine{Offset: offset, Level: "info"}
		offset += int64(len(raw) + 1)
		if len(raw) == 0 {
			continue
		}
		switch source {
		case nodeLogEvents:
			var event Event
			if json.Unmarshal(raw, &event) != nil || event.Type == "" {
				continue
			}
			line.Timestamp, line.Kind = event.Timestamp, event.Type
			line.Level = eventLogLevel(event)
			line.Text = event.Message
			if len(event.Fields) > 0 {
				fields, _ := json.Marshal(event.Fields)
				line.Text = strings.TrimSpace(line.Text + " " + string(fields))
			}
		case nodeLogLima:
			var entry struct {
				Time    time.Time `json:"time"`
				Level   string    `json:"level"`
				Message string    `json:"msg"`
			}
			if json.Unmarshal(raw, &entry) == nil && entry.Message != "" {
				line.Timestamp, line.Text = entry.Time, entry.Message
				if entry.Level != "" {
					line.Level = strings.ToLower(entry.Level)
				}
			} else {
				line.Text = string(raw)
			}
		default:
			line.Text = string(raw)
		}
		line.Text = plainLogText(line.Text)
		line.Kind = plainLogText(line.Kind)
		line.Level = plainLogText(line.Level)
		result.Lines = append(result.Lines, line)
	}
	if len(result.Lines) > nodeLogLineLimit {
		result.Lines = result.Lines[len(result.Lines)-nodeLogLineLimit:]
		result.Truncated = true
	}
	return result, nil
}

func (s *Service) nodeLogPath(node Node, source nodeLogSource) (string, error) {
	switch source {
	case nodeLogEvents:
		return s.store.nodeEventsPath(node.ID), nil
	case nodeLogBootstrap:
		return filepath.Join(s.store.nodeDir(node.ID), "bootstrap.log"), nil
	case nodeLogLima, nodeLogSerial:
		lima, ok := s.sandbox.(*LimaClient)
		if !ok {
			return "", dependencyUnavailable("Lima log location is unavailable", nil, nil)
		}
		home, err := lima.resolvedLimaHome()
		if err != nil {
			return "", err
		}
		if node.SandboxName == "" || node.SandboxName == "." || node.SandboxName == ".." || filepath.Base(node.SandboxName) != node.SandboxName {
			return "", invalidArgument("invalid Lima instance name", nil)
		}
		name := "ha.stderr.log"
		if source == nodeLogSerial {
			name = "serial.log"
		}
		return filepath.Join(home, node.SandboxName, name), nil
	default:
		return "", invalidArgument("unknown node log source", map[string]any{"source": source})
	}
}

func readNodeLogTail(path string, limit int) ([]byte, int64, bool, error) {
	// Open nonblocking before checking the descriptor: a replaced FIFO must
	// never leave a diagnostic reader waiting for a writer.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, true, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("log is not a regular file: %s", path)
	}
	offset := max(int64(0), info.Size()-int64(limit))
	data := make([]byte, min(int64(limit), info.Size()))
	n, err := file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, false, err
	}
	data = data[:n]
	if offset > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			offset += int64(newline + 1)
			data = data[newline+1:]
		} else {
			offset += int64(len(data))
			data = nil
		}
	}
	return data, offset, false, nil
}

func eventLogLevel(event Event) string {
	if level, ok := event.Fields["level"].(string); ok {
		return strings.ToLower(level)
	}
	switch {
	case strings.Contains(event.Type, "failed"), strings.Contains(event.Type, "error"):
		return "error"
	case strings.Contains(event.Type, "warning"), strings.Contains(event.Type, "degraded"):
		return "warn"
	default:
		return "info"
	}
}

func plainLogText(text string) string {
	var boundary terminalstate.VTBoundary
	var out strings.Builder
	for _, r := range strings.ToValidUTF8(text, "�") {
		ground := boundary.Ground()
		boundary.Feed([]byte(string(r)))
		if ground && boundary.Ground() {
			switch {
			case r == '\t':
				out.WriteString("    ")
			case r == '\n' || r == '\r':
				out.WriteByte(' ')
			case unicode.IsPrint(r), r == '\u200c', r == '\u200d':
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}
