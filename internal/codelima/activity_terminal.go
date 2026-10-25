package codelima

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

// One snapshot publisher owns this state. Restored metadata is seeded before
// new PTY output is admitted; ordinary frame publication never performs I/O.
type terminalActivityState struct {
	metadata TerminalMetadata
	nodeSlug string
}

func (s *terminalActivityState) observe(events *daemonActivityEvents, state daemon.TerminalState, snapshot daemon.Snapshot) {
	if events == nil || snapshot.Stale {
		return
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	if events.closed || events.handoff {
		return
	}
	previous, current := s.metadata, snapshot.Metadata
	s.metadata = current
	target, err := terminal.ParseTargetKey(state.Target)
	if err != nil || target.Kind != terminal.TargetNode {
		return
	}
	label := terminalMetadataText(current.Title, 120)
	if isDefaultShellTitle(label) {
		label = coalesce(terminalMetadataText(state.Label, 120), "shell")
	}
	appendNotice := func(text string) {
		events.enqueueLocked(activityMessageRequest{Source: "terminal", NodeID: target.ID, NodeSlug: s.nodeSlug, Text: text, Action: activityOpenTerminal, TerminalID: state.TerminalID})
	}
	if current.BellCount > previous.BellCount {
		count, detail := current.BellCount-previous.BellCount, "bell"
		if count > 1 {
			detail = fmt.Sprintf("%d bells", count)
		}
		appendNotice(label + " · " + detail)
	}
	notice := terminalNoticeText(current)
	if current.NotificationCount > previous.NotificationCount {
		count := current.NotificationCount - previous.NotificationCount
		if notice == "" {
			notice = "notification"
		}
		if count > 1 {
			notice = fmt.Sprintf("%d notifications · latest: %s", count, notice)
		}
		appendNotice(label + " · " + notice)
	} else if current.NotificationCount == 0 && previous.NotificationCount == 0 && notice != "" && notice != terminalNoticeText(previous) {
		// Older checkpoints have no counter. A changed visible notice is still
		// useful evidence; a lower counter after recovery is only a new baseline.
		appendNotice(label + " · " + notice)
	}
}

func terminalNoticeText(metadata TerminalMetadata) string {
	parts := []string{terminalMetadataText(metadata.NotificationTitle, 120), terminalMetadataText(metadata.NotificationBody, 512)}
	if parts[0] == "" {
		return parts[1]
	}
	if parts[1] == "" {
		return parts[0]
	}
	return strings.Join(parts, ": ")
}

func terminalActivityBaseline(ctx context.Context, term daemonTerminal) (TerminalMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return TerminalMetadata{}, fmt.Errorf("terminal notification baseline: %w", err)
		}
		result := term.Snapshot()
		if result.Err == nil && !result.Snapshot.Stale {
			return result.Snapshot.Metadata, nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func (h *daemonHost) terminalActivitySlug(state daemon.TerminalState) string {
	if target, err := terminal.ParseTargetKey(state.Target); err == nil {
		if node, err := h.service.store.NodeByID(target.ID); err == nil {
			return node.Slug
		}
	}
	return terminalMetadataText(state.Label, 128)
}
