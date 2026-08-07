package codelima

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

func (a *vaxisTUIApp) startOperation(request tuiOperationRequest) error {
	if strings.TrimSpace(request.Title) == "" {
		return fmt.Errorf("operation title is required")
	}
	if request.Run == nil {
		return fmt.Errorf("operation runner is required")
	}
	if conflict := a.conflictingOperation(request.ResourceKeys); conflict != nil {
		return fmt.Errorf("%s is already in progress", strings.ToLower(conflict.Title))
	}

	if a.postEvent == nil {
		result, err := request.Run(a.ctx, a.service)
		if err != nil {
			return err
		}
		return a.applyOperationResult(result.PreferredKey, result)
	}

	if a.operations == nil {
		a.operations = map[string]*tuiOperationState{}
	}

	operationID := newID()
	operation := &tuiOperationState{
		ID:            operationID,
		Title:         request.Title,
		DisplayStatus: request.DisplayStatus,
		SelectionKey:  a.state.selectedEntry().key(),
		EntryKeys:     append([]string(nil), request.EntryKeys...),
		ResourceKeys:  append([]string(nil), request.ResourceKeys...),
		Lines:         []string{"waiting for command output..."},
	}
	a.operations[operationID] = operation
	a.operationOrder = append(a.operationOrder, operationID)

	go func() {
		progress := newTUIProgressWriter(a.postEvent, operationID)
		service := a.service.withIO(progress, progress)
		result, err := request.Run(a.ctx, service)
		progress.Flush()
		completion := tuiOperationCompleteEvent{OperationID: operationID, Result: result, Err: err}
		operation.completion.Store(&completion)
		a.postCompletion(completion)
	}()

	return nil
}

func (a *vaxisTUIApp) applyOperationResult(selectedKey string, result tuiOperationResult) error {
	if result.CloseNodeID != "" {
		a.sessions.CloseNode(result.CloseNodeID)
	}
	if result.ReloadData {
		// The reload is a daemon RPC or a store read; it runs off the event
		// loop and applies through tuiRefreshCompleteEvent (invariant I1).
		a.startDataRefresh(selectedKey)
	}
	if result.ShowTerminalPane {
		a.state.treePaneMode = tuiTreePaneModeTerminal
	}
	if result.Status != "" {
		a.setStatus(slog.LevelInfo, result.Status)
	}
	if result.Apply != nil {
		return result.Apply(a)
	}
	return nil
}

// conflictingOperation reports the active operation that owns one of
// resourceKeys. Operations that already recorded a completion are skipped:
// their event may still be in flight (or lost), and a finished operation must
// never keep blocking a retry.
func (a *vaxisTUIApp) conflictingOperation(resourceKeys []string) *tuiOperationState {
	if len(resourceKeys) == 0 {
		return nil
	}

	for _, operationID := range a.operationOrder {
		operation := a.operations[operationID]
		if operation == nil || operation.completion.Load() != nil {
			continue
		}
		if operationConflicts(operation.ResourceKeys, resourceKeys) {
			return operation
		}
	}

	return nil
}

// reapCompletedOperations applies every background operation that finished but
// whose tuiOperationCompleteEvent never reached the loop. vaxis silently drops
// posted events when its queue is full — precisely when the loop is busy — and
// a lost completion would otherwise leave the operation latched forever.
func (a *vaxisTUIApp) reapCompletedOperations() {
	for _, operationID := range slices.Clone(a.operationOrder) {
		operation := a.operations[operationID]
		if operation == nil {
			continue
		}
		if completion := operation.completion.Load(); completion != nil {
			a.finishOperation(*completion)
		}
	}
}

func operationConflicts(active []string, requested []string) bool {
	if len(active) == 0 || len(requested) == 0 {
		return false
	}

	activeKeys := map[string]bool{}
	for _, key := range active {
		if strings.TrimSpace(key) == "" {
			continue
		}
		activeKeys[key] = true
	}
	for _, key := range requested {
		if activeKeys[key] {
			return true
		}
	}
	return false
}

func (a *vaxisTUIApp) appendOperationLine(operationID string, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	operation := a.operations[operationID]
	if operation == nil {
		return
	}

	operation.Lines = append(operation.Lines, line)
	if len(operation.Lines) > 200 {
		operation.Lines = operation.Lines[len(operation.Lines)-200:]
	}
}

func (a *vaxisTUIApp) finishOperation(event tuiOperationCompleteEvent) {
	operation, tracked := a.operations[event.OperationID]
	if !tracked {
		// reapCompletedOperations already applied this completion (or the
		// posted event arrived twice); replaying it would repeat its status,
		// its reload, and its message-ring entries.
		return
	}
	delete(a.operations, event.OperationID)
	for index, operationID := range a.operationOrder {
		if operationID != event.OperationID {
			continue
		}
		a.operationOrder = append(a.operationOrder[:index], a.operationOrder[index+1:]...)
		break
	}

	// Retain the finished operation's output in the message ring at its true
	// level before it is discarded, so failed background operations stay
	// inspectable in the messages view (ADR 59).
	a.retainOperationMessages(operation, event)

	if event.Err != nil {
		a.setStatus(slog.LevelError, event.Err.Error())
	} else if err := a.applyOperationResult(a.resultSelectionKey(operation, event.Result), event.Result); err != nil {
		a.setStatus(slog.LevelError, err.Error())
	}
}

const tuiRetainedOperationOutputLines = 40

// retainOperationMessages copies a finished operation's summary and captured
// output into the message ring. Success records at info, failure at error; the
// per-operation output is capped to its tail so one noisy operation cannot evict
// the entire ring.
func (a *vaxisTUIApp) retainOperationMessages(operation *tuiOperationState, event tuiOperationCompleteEvent) {
	if operation == nil {
		return
	}

	title := strings.TrimSpace(operation.Title)
	if title == "" {
		title = "background operation"
	}

	level := slog.LevelInfo
	summary := title + " completed"
	if event.Err != nil {
		level = slog.LevelError
		summary = title + " failed: " + event.Err.Error()
	} else if status := strings.TrimSpace(event.Result.Status); status != "" {
		summary = title + ": " + status
	}

	a.pushMessage(level, summary)
	for _, line := range retainedOperationOutput(operation.Lines) {
		a.pushMessage(level, title+" | "+line)
	}
}

func retainedOperationOutput(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "waiting for command output..." {
			continue
		}
		filtered = append(filtered, line)
	}
	if len(filtered) > tuiRetainedOperationOutputLines {
		filtered = filtered[len(filtered)-tuiRetainedOperationOutputLines:]
	}
	return filtered
}

func (a *vaxisTUIApp) resultSelectionKey(operation *tuiOperationState, result tuiOperationResult) string {
	if a.state == nil {
		return result.PreferredKey
	}

	currentKey := a.state.selectedEntry().key()
	if operation == nil {
		if currentKey != "" {
			return currentKey
		}
		return result.PreferredKey
	}

	if currentKey != "" && currentKey != operation.SelectionKey {
		return currentKey
	}

	if result.PreferredKey != "" {
		return result.PreferredKey
	}
	if currentKey != "" {
		return currentKey
	}
	return operation.SelectionKey
}
