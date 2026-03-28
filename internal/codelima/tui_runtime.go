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

	"git.sr.ht/~rockorager/vaxis"
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
}

type tuiOperationResult struct {
	Status       string
	PreferredKey string
	CloseNodeID  string
	ReloadData   bool
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
