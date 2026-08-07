package codelima

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type tuiLinkRegion struct {
	rect   tuiRect
	target string
}

type tuiOperationState struct {
	ID            string
	Title         string
	DisplayStatus string
	SelectionKey  string
	EntryKeys     []string
	ResourceKeys  []string
	Lines         []string
	// completion is written once by the background goroutine before it posts
	// tuiOperationCompleteEvent. It makes "finished" re-derivable on the event
	// loop so a dropped completion event cannot latch the operation — and its
	// resource keys — as in progress forever (invariant I2).
	completion atomic.Pointer[tuiOperationCompleteEvent]
}

type tuiOperationResult struct {
	Status           string
	PreferredKey     string
	CloseNodeID      string
	ReloadData       bool
	ShowTerminalPane bool
	// Apply runs on the TUI event loop once the rest of the result has been
	// applied. Dialog flows use it for the follow-up overlay they used to open
	// inline, keeping the service call itself off the loop.
	Apply func(*vaxisTUIApp) error
}

type tuiOperationRequest struct {
	Title         string
	DisplayStatus string
	ResourceKeys  []string
	EntryKeys     []string
	Run           func(context.Context, *Service) (tuiOperationResult, error)
}

type tuiOperationProgressEvent struct {
	OperationID string
	Line        string
}

type tuiOperationCompleteEvent struct {
	OperationID string
	Result      tuiOperationResult
	Err         error
}

type tuiRefreshTickEvent struct{}

// tuiNodesChangedEvent asks the event loop to reload the node list because the
// daemon reported a lifecycle change. It carries no payload: the loop owns the
// reload, and the reload itself (startDataRefresh) runs off the loop. It is
// posted debounced, so a burst of pushes costs one reload.
type tuiNodesChangedEvent struct{}

// tuiNodeUsageEvent carries one node's live usage sample to the event loop,
// which owns the node records. Usage is a per-node value, not a list change:
// it is applied in place at push latency and never triggers a node.list
// reload, so the 1Hz sampler stays 1Hz-fresh while list reloads stay on
// push-on-change plus their slow safety net.
type tuiNodeUsageEvent struct {
	Usage nodeUsageEvent
}

// decodeDaemonNodeUsage decodes a node.usage_changed payload and rejects the
// samples that name no node. The decode itself goes through the one seam every
// event consumer uses (daemon.DecodeEventData); only the "which node is this
// about" precondition is local, because a sample with no node has nowhere to be
// applied.
func decodeDaemonNodeUsage(data any) (nodeUsageEvent, bool) {
	usage, ok := daemon.DecodeEventData[nodeUsageEvent](data)
	if !ok || strings.TrimSpace(usage.NodeID) == "" {
		return nodeUsageEvent{}, false
	}
	return usage, true
}

// applyNodeUsageSample writes a usage sample over a node's runtime observation.
// The whole sample is written, including its absences: the daemon publishes
// every reading it has each time, so a cleared reading must clear the display
// rather than leave the previous number standing.
func applyNodeUsageSample(u nodeUsageEvent, observation *RuntimeObservation) {
	if observation == nil {
		return
	}
	observation.CPUUsagePercent = u.CPUUsagePercent
	observation.MemoryUsedBytes = u.MemoryUsedBytes
	observation.MemoryTotalBytes = u.MemoryTotalBytes
	observation.DiskUsedBytes = u.DiskUsedBytes
	observation.DiskTotalBytes = u.DiskTotalBytes
	if u.SampledAt.IsZero() {
		observation.CPUUsageSampledAt = nil
		observation.ResourceUsageSampledAt = nil
		return
	}
	sampledAt := u.SampledAt
	observation.CPUUsageSampledAt = &sampledAt
	observation.ResourceUsageSampledAt = &sampledAt
}

// nodeUsageFromNode reads back the usage a node.list reply carried, in the same
// shape a push delivers it, so the two can be compared and merged.
func nodeUsageFromNode(node Node) nodeUsageEvent {
	usage := nodeUsageEvent{NodeID: node.ID}
	observation := node.LastRuntimeObservation
	if observation == nil {
		return usage
	}
	usage.CPUUsagePercent = observation.CPUUsagePercent
	usage.MemoryUsedBytes = observation.MemoryUsedBytes
	usage.MemoryTotalBytes = observation.MemoryTotalBytes
	usage.DiskUsedBytes = observation.DiskUsedBytes
	usage.DiskTotalBytes = observation.DiskTotalBytes
	switch {
	case observation.ResourceUsageSampledAt != nil:
		usage.SampledAt = *observation.ResourceUsageSampledAt
	case observation.CPUUsageSampledAt != nil:
		usage.SampledAt = *observation.CPUUsageSampledAt
	}
	return usage
}

type tuiRefreshCompleteEvent struct {
	Nodes []Node
	// PreferredKey is the tree selection to restore once the reload lands. It
	// is empty for the periodic tick and set for reloads requested by a
	// finished background operation.
	PreferredKey string
	Err          error
}

type tuiClipboardEvent struct {
	TargetKey string
	Text      string
}

type tuiProgressWriter struct {
	post        func(vaxis.Event)
	operationID string
	mu          sync.Mutex
	pending     string
}

func newTUIProgressWriter(post func(vaxis.Event), operationID string) *tuiProgressWriter {
	return &tuiProgressWriter{post: post, operationID: operationID}
}

func (w *tuiProgressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.pending += string(p)
	for {
		index := strings.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimRight(w.pending[:index], "\r")
		w.pending = w.pending[index+1:]
		if line == "" {
			continue
		}
		w.post(tuiOperationProgressEvent{OperationID: w.operationID, Line: line})
	}

	return len(p), nil
}

func (w *tuiProgressWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	line := strings.TrimSpace(strings.TrimRight(w.pending, "\r"))
	w.pending = ""
	if line == "" {
		return
	}
	w.post(tuiOperationProgressEvent{OperationID: w.operationID, Line: line})
}

var tuiLinkPattern = regexp.MustCompile(`https?://[^\s]+|/[^\s]+`)

func linkifiedSegments(text string, baseStyle vaxis.Style) []vaxis.Segment {
	matches := tuiLinkPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return []vaxis.Segment{{Text: text, Style: baseStyle}}
	}

	segments := make([]vaxis.Segment, 0, len(matches)*2+1)
	last := 0
	for _, match := range matches {
		if match[0] > last {
			segments = append(segments, vaxis.Segment{
				Text:  text[last:match[0]],
				Style: baseStyle,
			})
		}

		token := text[match[0]:match[1]]
		display, target := normalizeLinkToken(token)
		style := baseStyle
		if target != "" {
			style.Hyperlink = target
			style.UnderlineStyle = vaxis.UnderlineSingle
		}
		segments = append(segments, vaxis.Segment{
			Text:  display,
			Style: style,
		})
		last = match[1]
	}

	if last < len(text) {
		segments = append(segments, vaxis.Segment{
			Text:  text[last:],
			Style: baseStyle,
		})
	}

	return segments
}

func normalizeLinkToken(token string) (display string, target string) {
	display = token
	trimmed := strings.TrimRight(token, ".,;:)]}>")
	suffix := token[len(trimmed):]
	if suffix != "" {
		display = trimmed + suffix
	}

	switch {
	case strings.HasPrefix(trimmed, "http://"), strings.HasPrefix(trimmed, "https://"):
		return display, trimmed
	case filepath.IsAbs(trimmed):
		return display, fileHyperlink(trimmed)
	default:
		return display, ""
	}
}

func fileHyperlink(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func screenBufferHyperlinkAt(buffer [][]vaxis.Cell, col, row int) (string, bool) {
	if row < 0 || row >= len(buffer) {
		return "", false
	}
	if col < 0 || col >= len(buffer[row]) {
		return "", false
	}

	target := buffer[row][col].Hyperlink
	if target == "" {
		return "", false
	}
	return target, true
}

// Reflects into unexported vaxis internals (screenNext.buf[row][col].Style.Hyperlink); guarded by TestVaxisHyperlinkReflectionStillValid (tui_reflection_canary_test.go) until the upstream accessor proposed in plan §0.8 exists.
func renderedHyperlinkAt(vx *vaxis.Vaxis, col, row int) (string, bool) {
	if vx == nil {
		return "", false
	}

	value := reflect.ValueOf(vx)
	if !value.IsValid() || value.IsNil() {
		return "", false
	}

	screen := value.Elem().FieldByName("screenNext")
	if !screen.IsValid() || screen.IsNil() {
		return "", false
	}

	buffer := screen.Elem().FieldByName("buf")
	if !buffer.IsValid() || buffer.Kind() != reflect.Slice || row < 0 || row >= buffer.Len() {
		return "", false
	}

	rowValue := buffer.Index(row)
	if rowValue.Kind() != reflect.Slice || col < 0 || col >= rowValue.Len() {
		return "", false
	}

	cellStyle := rowValue.Index(col).FieldByName("Style")
	if !cellStyle.IsValid() {
		return "", false
	}

	target := cellStyle.FieldByName("Hyperlink")
	if !target.IsValid() || target.Kind() != reflect.String || target.String() == "" {
		return "", false
	}

	return target.String(), true
}

func openHyperlink(target string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}

	command := exec.Command(opener, target)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open link: %w", err)
	}
	return nil
}

func renderedTextWidth(vx *vaxis.Vaxis, text string) int {
	if vx != nil {
		return vx.RenderedWidth(text)
	}

	width := 0
	for _, character := range vaxis.Characters(text) {
		if character.Width > 0 {
			width += character.Width
			continue
		}
		width++
	}
	return width
}
