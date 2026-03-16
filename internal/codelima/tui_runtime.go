package codelima

import (
	"fmt"
	"io"
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

type tuiPoint struct {
	col int
	row int
}

type tuiTerminalSelection struct {
	nodeID  string
	start   tuiPoint
	end     tuiPoint
	dragged bool
}

type tuiOperationState struct {
	Title string
	Lines []string
}

type tuiOperationResult struct {
	Status        string
	PreferredKey  string
	CloseNodeID   string
	ReloadData    bool
	ReloadPatches bool
}

type tuiOperationProgressEvent struct {
	Line string
}

type tuiOperationCompleteEvent struct {
	Result tuiOperationResult
	Err    error
}

type tuiProgressWriter struct {
	post    func(vaxis.Event)
	mu      sync.Mutex
	pending string
}

func newTUIProgressWriter(post func(vaxis.Event)) *tuiProgressWriter {
	return &tuiProgressWriter{post: post}
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
		w.post(tuiOperationProgressEvent{Line: line})
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
	w.post(tuiOperationProgressEvent{Line: line})
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

func copyTextToClipboard(text string, push func(string)) error {
	return copyTextToClipboardWith(text, push, writeSystemClipboard)
}

func copyTextToClipboardWith(text string, push func(string), nativeCopy func(string) error) error {
	if push != nil {
		push(text)
	}
	if nativeCopy == nil {
		return nil
	}
	if err := nativeCopy(text); err != nil && push == nil {
		return err
	}
	return nil
}

func writeSystemClipboard(text string) error {
	command, err := clipboardCommand()
	if err != nil {
		return err
	}

	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("clipboard stdin: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start clipboard command: %w", err)
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("write clipboard text: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("close clipboard stdin: %w", err)
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("copy clipboard text: %w", err)
	}
	return nil
}

func clipboardCommand() (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("pbcopy"), nil
	case "windows":
		return exec.Command("cmd", "/c", "clip"), nil
	default:
		for _, candidate := range [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		} {
			if _, err := exec.LookPath(candidate[0]); err != nil {
				continue
			}
			return exec.Command(candidate[0], candidate[1:]...), nil
		}
		return nil, fmt.Errorf("no clipboard command available")
	}
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

func normalizedSelection(selection tuiTerminalSelection) (tuiPoint, tuiPoint) {
	start := selection.start
	end := selection.end
	if end.row < start.row || (end.row == start.row && end.col < start.col) {
		start, end = end, start
	}
	return start, end
}

func extractTerminalSelection(snapshot string, selection tuiTerminalSelection) string {
	lines := strings.Split(snapshot, "\n")
	start, end := normalizedSelection(selection)
	if len(lines) == 0 || start.row >= len(lines) {
		return ""
	}
	if end.row >= len(lines) {
		end.row = len(lines) - 1
	}

	parts := make([]string, 0, end.row-start.row+1)
	for row := start.row; row <= end.row; row++ {
		line := []rune(lines[row])
		if len(line) == 0 {
			parts = append(parts, "")
			continue
		}

		from := 0
		to := len(line)
		if row == start.row {
			from = clampInt(start.col, 0, len(line)-1)
		}
		if row == end.row {
			to = clampInt(end.col+1, 0, len(line))
		}
		if from > to {
			from, to = to, from
		}
		parts = append(parts, strings.TrimRight(string(line[from:to]), " "))
	}

	return strings.Join(parts, "\n")
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
