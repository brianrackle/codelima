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

// isDefaultShellTitle recognizes common shell names and user[@host]: /path
// prompt titles. OSC titles have no source tag, so keep unfamiliar formats
// and titles with task separators rather than guessing a program's intent.
func isDefaultShellTitle(title string) bool {
	switch strings.TrimPrefix(title, "-") {
	case "", "shell", "sh", "bash", "dash", "zsh", "fish", "ksh", "ash", "csh", "tcsh":
		return true
	}
	identity, directory, ok := strings.Cut(title, ":")
	if !ok || identity == "" || strings.ContainsAny(title, "|·") {
		return false
	}
	for part := range strings.SplitSeq(identity, "@") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' {
				return false
			}
		}
	}
	if strings.Count(identity, "@") > 1 {
		return false
	}
	directory = strings.TrimSpace(directory)
	// A URI is an application title, not the shell's user/path convention.
	return (strings.HasPrefix(directory, "/") && !strings.HasPrefix(directory, "//")) ||
		directory == "~" || strings.HasPrefix(directory, "~/")
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
