package codelima

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"
)

// Guest-controlled labels are bounded plain text, never host control strings,
// shell commands, automatic path navigation or desktop notifications.
func terminalMetadataText(text string, limit int) string {
	var clean []rune
	for _, r := range text {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || unicode.In(r, unicode.Cf) {
			continue
		}
		if len(clean) >= limit {
			return strings.TrimSpace(string(clean)) + "…"
		}
		clean = append(clean, r)
	}
	return strings.TrimSpace(string(clean))
}

func terminalMetadataBadge(metadata TerminalMetadata) string {
	var labels []string
	if metadata.ProgressState != 0 {
		labels = append(labels, fmt.Sprintf("%d%%", max(0, min(100, metadata.Progress))))
	}
	if metadata.NotificationTitle != "" || metadata.NotificationBody != "" {
		labels = append(labels, "notice")
	}
	return strings.Join(labels, " · ")
}

func (a *vaxisTUIApp) terminalMetadata(target string) TerminalMetadata {
	term, ok := a.sessions.SessionTerminal(target)
	if !ok {
		return TerminalMetadata{}
	}
	receiver, ok := term.(interface{ Metadata() TerminalMetadata })
	if !ok {
		return TerminalMetadata{}
	}
	return receiver.Metadata()
}

func (a *vaxisTUIApp) recordTerminalNotices() {
	if a.sessions == nil {
		return
	}
	if a.terminalNotices == nil {
		a.terminalNotices = make(map[string]string)
	}
	for key := range a.terminalNotices {
		if _, ok := a.sessions.sessions[key]; !ok {
			delete(a.terminalNotices, key)
		}
	}
	for key := range a.sessions.sessions {
		term, ok := a.sessions.SessionTerminal(key)
		if !ok {
			continue
		}
		receiver, ok := term.(interface{ Metadata() TerminalMetadata })
		if !ok {
			continue
		}
		metadata := receiver.Metadata()
		message := terminalMetadataText(metadata.NotificationTitle, 120)
		if body := terminalMetadataText(metadata.NotificationBody, 512); body != "" {
			if message != "" {
				message += ": "
			}
			message += body
		}
		if message == "" || message == a.terminalNotices[key] {
			continue
		}
		a.terminalNotices[key] = message
		if a.messages == nil {
			a.messages = newTUIMessageLog(tuiMessageLogDefaultCap)
		}
		a.messages.Append(slog.LevelInfo, "terminal "+key+": "+message)
	}
}
