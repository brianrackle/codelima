package codelima

import (
	"context"
	"log/slog"
	"time"

	"go.rockorager.dev/vaxis"
)

type tuiLifecycleState struct {
	nodeID                                  string
	loaded, inFlight                        bool
	operation                               NodeOperation
	err                                     error
	serial                                  uint64
	deadline                                time.Time
	cancel                                  context.CancelFunc
	queuedNode, queuedOperation, queuedMode string
}

type tuiLifecycleDataEvent struct {
	nodeID             string
	serial, generation uint64
	operation          NodeOperation
	err                error
}

func (a *vaxisTUIApp) lifecycleVisible() bool {
	return a.view == tuiViewNodes && a.nodeDetail
}

func (a *vaxisTUIApp) cancelLifecycleLoad() {
	if a.lifecycle.cancel != nil {
		a.lifecycle.cancel()
		a.lifecycle.cancel = nil
	}
	a.lifecycle.serial++
	a.lifecycle.inFlight = false
}

func (a *vaxisTUIApp) cancelHiddenLifecycleLoad() {
	if !a.lifecycleVisible() {
		a.cancelLifecycleLoad()
	}
	if !a.lifecycleVisible() || a.lifecycle.queuedNode != a.state.selectedEntry().node.ID {
		a.cancelQueuedAttachOnNavigation()
	}
}

func (a *vaxisTUIApp) startLifecycleLoad() {
	if !a.lifecycleVisible() {
		a.cancelHiddenLifecycleLoad()
		return
	}
	id := a.state.selectedEntry().node.ID
	if id == "" {
		return
	}
	s := &a.lifecycle
	if s.nodeID != id {
		a.cancelLifecycleLoad()
		*s = tuiLifecycleState{nodeID: id, serial: s.serial}
	}
	if a.service == nil || a.service.daemonClient == nil {
		return
	}
	if s.inFlight && time.Now().Before(s.deadline) {
		return
	}
	a.cancelLifecycleLoad()
	s.inFlight, s.deadline = true, time.Now().Add(6*time.Second)
	serial, generation, client := s.serial, a.managerGeneration, a.service.daemonClient
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.cancel = cancel
	load := func() tuiLifecycleDataEvent {
		defer cancel()
		event := tuiLifecycleDataEvent{nodeID: id, serial: serial, generation: generation}
		event.err = fromDaemonError(client.Call(ctx, "operation.latest", map[string]string{"node": id}, &event.operation))
		if IsNotFound(event.err) {
			event.err = nil
		}
		return event
	}
	if a.postEvent == nil {
		a.applyLifecycleData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyLifecycleData(event tuiLifecycleDataEvent) {
	s := &a.lifecycle
	if event.serial != s.serial {
		return
	}
	s.inFlight = false
	if !a.lifecycleVisible() || event.generation != a.managerGeneration || event.nodeID != s.nodeID || event.nodeID != a.state.selectedEntry().node.ID {
		return
	}
	if event.operation.ID != "" && event.operation.NodeID != event.nodeID {
		return
	}
	s.loaded, s.err = true, event.err
	if event.err == nil {
		s.operation = event.operation
	}
	a.finishQueuedLifecycleAttach()
}

func (a *vaxisTUIApp) activeStartOperation() (NodeOperation, bool) {
	id := a.state.selectedEntry().node.ID
	for _, operation := range a.activity.page.Running {
		if operation.NodeID != id || !operation.Request.startsNode() {
			continue
		}
		if a.lifecycle.nodeID == id && a.lifecycle.operation.ID == operation.ID {
			record := a.lifecycle.operation
			if record.State != "running" && record.State != "canceling" {
				return NodeOperation{}, false
			}
			if operation.State == "canceling" {
				record.State = "canceling"
			}
			return record, true
		}
		return operation, true
	}
	record := a.lifecycle.operation
	if a.lifecycle.nodeID == id && record.NodeID == id && (record.State == "running" || record.State == "canceling") && record.Request.startsNode() {
		return record, true
	}
	return NodeOperation{}, false
}

func (a *vaxisTUIApp) nodeStarting() bool {
	_, active := a.activeStartOperation()
	return active || !a.lifecycleReceiptFailed() && a.state.selectedEntry().node.Status == NodeStatusProvisioning
}

func (a *vaxisTUIApp) lifecycleReceiptFailed() bool {
	id := a.state.selectedEntry().node.ID
	record := a.lifecycle.operation
	return a.lifecycle.nodeID == id && record.NodeID == id && (record.State == "failed" || record.State == "interrupted")
}

func (a *vaxisTUIApp) nodeLifecycleFailed() bool {
	return a.state.selectedEntry().node.Status == NodeStatusFailed || a.lifecycleReceiptFailed()
}

func (a *vaxisTUIApp) clearQueuedLifecycleAttach() {
	a.lifecycle.queuedNode, a.lifecycle.queuedOperation, a.lifecycle.queuedMode = "", "", ""
}

func (a *vaxisTUIApp) cancelQueuedAttachOnNavigation() {
	if a.lifecycle.queuedOperation == "" {
		return
	}
	a.clearQueuedLifecycleAttach()
	a.setStatus(slog.LevelInfo, "Queued attachment canceled after leaving this node")
}

func (a *vaxisTUIApp) finishQueuedLifecycleAttach() {
	s := &a.lifecycle
	if s.queuedOperation == "" {
		return
	}
	if !a.lifecycleVisible() || s.queuedNode != a.state.selectedEntry().node.ID {
		a.cancelQueuedAttachOnNavigation()
		return
	}
	if !s.loaded || s.err != nil {
		return
	}
	if s.operation.ID != s.queuedOperation {
		a.clearQueuedLifecycleAttach()
		a.setStatus(slog.LevelInfo, "Queued attachment canceled: a different startup is now current")
		return
	}
	switch s.operation.State {
	case "failed", "canceled", "canceling", "interrupted":
		a.clearQueuedLifecycleAttach()
		a.setStatus(slog.LevelWarn, "Queued attachment canceled: startup did not finish successfully")
	case "succeeded":
		node := a.state.selectedEntry().node
		observation := node.LastRuntimeObservation
		if node.Status != NodeStatusRunning || !node.BootstrapCompleted || observation == nil || !observation.Exists || observation.Status != ObservationRunning || a.overlay != nil {
			return
		}
		for _, operation := range a.activity.page.Running {
			if operation.NodeID == node.ID && operation.ID != s.queuedOperation {
				a.clearQueuedLifecycleAttach()
				a.setStatus(slog.LevelInfo, "Queued attachment canceled: this node has another operation")
				return
			}
		}
		mode := s.queuedMode
		a.clearQueuedLifecycleAttach()
		if err := a.startShellMode(mode, false, false, ""); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
	}
}

func (a *vaxisTUIApp) handleLifecycleKey(key vaxis.Key) bool {
	if !a.lifecycleVisible() {
		return false
	}
	if key.Matches(vaxis.KeyEsc) && a.lifecycle.queuedOperation != "" {
		a.clearQueuedLifecycleAttach()
		a.setStatus(slog.LevelInfo, "Queued attachment canceled; startup continues")
		return true
	}
	if !a.nodeStarting() {
		return a.handleFailureKey(key)
	}
	operation, active := a.activeStartOperation()
	switch {
	case key.MatchString("x"):
		if !active || operation.ID == "" || operation.State == "canceling" {
			a.setStatus(slog.LevelWarn, "Wait for an active startup receipt before queuing attachment")
			return true
		}
		a.lifecycle.queuedNode, a.lifecycle.queuedOperation, a.lifecycle.queuedMode = operation.NodeID, operation.ID, a.managerSettings().Shell.Mode
		if a.lifecycle.operation.ID != operation.ID {
			// Activity can discover a new admission before the detail query.
			// Revoke any earlier in-flight reply before binding this intent.
			a.cancelLifecycleLoad()
			a.lifecycle.loaded = false
			a.startLifecycleLoad()
		}
		a.setStatus(slog.LevelInfo, "Attachment queued for this startup · Esc cancels the queue")
	case key.MatchString("s"):
		if operation.State == "canceling" {
			a.setStatus(slog.LevelInfo, "Cancellation is in progress; waiting for VM shutdown")
			return true
		}
		if !active {
			a.setStatus(slog.LevelWarn, "No daemon-owned startup is available to cancel")
			return true
		}
		id := operation.ID
		cancel := func(map[string]string) error {
			a.clearQueuedLifecycleAttach()
			a.requestOperationCancel(id)
			return nil
		}
		if a.managerSettings().ConfirmStop != "never" {
			a.overlay = newTUIDialog("Cancel startup", "Cancel start", []string{"Stop the active command and shut down this VM.", "Partially executed bootstrap commands rerun on the next ordinary start."}, nil, cancel)
		} else {
			_ = cancel(nil)
		}
	case key.MatchString("T"):
		if err := a.startShellMode(a.managerSettings().Shell.Mode, true, false, ""); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
	case key.MatchString("c"), key.MatchString("d"):
		a.setStatus(slog.LevelWarn, "Wait for startup to finish or cancel it before cloning or deleting")
	default:
		return false
	}
	return true
}
