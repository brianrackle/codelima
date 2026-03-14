package codelima

import (
	"errors"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

func TestLinkifiedSegmentsHyperlinkPathsAndURLs(t *testing.T) {
	t.Parallel()

	segments := linkifiedSegments("Workspace: /tmp/demo https://example.com/docs", vaxis.Style{})

	var hyperlinks []string
	for _, segment := range segments {
		if segment.Style.Hyperlink == "" {
			continue
		}
		hyperlinks = append(hyperlinks, segment.Style.Hyperlink)
	}

	if len(hyperlinks) != 2 {
		t.Fatalf("expected 2 hyperlinks, got %d (%v)", len(hyperlinks), hyperlinks)
	}
	if hyperlinks[0] != "file:///tmp/demo" {
		t.Fatalf("expected file hyperlink, got %q", hyperlinks[0])
	}
	if hyperlinks[1] != "https://example.com/docs" {
		t.Fatalf("expected URL hyperlink, got %q", hyperlinks[1])
	}
}

func TestScreenBufferHyperlinkAtReturnsCellHyperlink(t *testing.T) {
	t.Parallel()

	buffer := [][]vaxis.Cell{
		{
			{Style: vaxis.Style{}},
			{Style: vaxis.Style{Hyperlink: "https://example.com/auth"}},
		},
	}

	target, ok := screenBufferHyperlinkAt(buffer, 1, 0)
	if !ok {
		t.Fatalf("expected hyperlink lookup to succeed")
	}
	if target != "https://example.com/auth" {
		t.Fatalf("expected hyperlink target, got %q", target)
	}

	if _, ok := screenBufferHyperlinkAt(buffer, 0, 0); ok {
		t.Fatalf("expected empty cell lookup to fail")
	}
	if _, ok := screenBufferHyperlinkAt(buffer, 3, 0); ok {
		t.Fatalf("expected out-of-bounds column lookup to fail")
	}
	if _, ok := screenBufferHyperlinkAt(buffer, 0, 3); ok {
		t.Fatalf("expected out-of-bounds row lookup to fail")
	}
}

func TestExtractTerminalSelectionAcrossLines(t *testing.T) {
	t.Parallel()

	selection := tuiTerminalSelection{
		start: tuiPoint{col: 1, row: 0},
		end:   tuiPoint{col: 2, row: 1},
	}
	text := extractTerminalSelection("abc   \ndef   \nxyz   ", selection)
	if text != "bc\ndef" {
		t.Fatalf("expected multi-line selection, got %q", text)
	}
}

func TestTUIProgressWriterFlushesPendingLine(t *testing.T) {
	t.Parallel()

	lines := []string{}
	writer := newTUIProgressWriter(func(event vaxis.Event) {
		progress, ok := event.(tuiOperationProgressEvent)
		if !ok {
			t.Fatalf("unexpected event type %T", event)
		}
		lines = append(lines, progress.Line)
	})

	if _, err := writer.Write([]byte("line one\nline two")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	writer.Flush()

	if strings.Join(lines, "|") != "line one|line two" {
		t.Fatalf("unexpected progress lines: %v", lines)
	}
}

func TestCopyTextToClipboardWithFallsBackToTerminalClipboard(t *testing.T) {
	t.Parallel()

	pushed := ""
	err := copyTextToClipboardWith("copied text", func(text string) {
		pushed = text
	}, func(string) error {
		return errors.New("native clipboard unavailable")
	})
	if err != nil {
		t.Fatalf("copyTextToClipboardWith() error = %v", err)
	}
	if pushed != "copied text" {
		t.Fatalf("expected terminal clipboard fallback, got %q", pushed)
	}
}

func TestCopyTextToClipboardWithReturnsNativeErrorWithoutFallback(t *testing.T) {
	t.Parallel()

	err := copyTextToClipboardWith("copied text", nil, func(string) error {
		return errors.New("native clipboard unavailable")
	})
	if err == nil {
		t.Fatalf("expected native clipboard error without terminal fallback")
	}
}
