package codelima

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type tuiCreateAdmissionEvent struct {
	form       *tuiCreateNodeForm
	request    NodeOperationRequest
	record     NodeOperation
	err        error
	definitive bool
}

type tuiCreateIntent struct {
	operationID string
	nodeID      string
	generation  uint64
	attach      bool
	mode        string
	inFlight    bool
	record      NodeOperation
}

type tuiCreateIntentEvent struct {
	intent *tuiCreateIntent
	record NodeOperation
	err    error
}

func (f *tuiCreateNodeForm) submit() error {
	if f.pending {
		return nil
	}
	if f.request == nil {
		if err := f.validationError(); err != "" {
			return errors.New(err)
		}
		ports, _ := validatePorts(parseCommaSeparatedValues(f.value("ports")))
		f.request = &NodeOperationRequest{ID: newID(), Kind: "create", Origin: "tui", Create: &NodeCreateInput{
			Slug: f.value("slug"), Directory: f.value("directory"), Configuration: f.value("configuration"),
			WorkspaceMode: f.value("workspace_mode"), ImportHostAuth: boolPointer(f.value("import_host_auth") == hostAuthImportOn),
			Ports: ports, Start: f.value("start") == "true",
		}}
		f.attach = f.value("attach") == "true"
	}
	f.pending = true
	request := *f.request
	input := *request.Create
	request.Create = &input
	ctx, cancel := f.workContext()
	service := f.app.service
	submit := func() tuiCreateAdmissionEvent {
		defer cancel()
		e := tuiCreateAdmissionEvent{form: f, request: request}
		// Resolve relative paths in the requesting frontend, before crossing
		// the daemon boundary. Preserve this payload if the reply is lost.
		input.Directory, e.err = service.resolveNodeDirectoryPath(input.Directory)
		if e.err != nil {
			e.definitive = true
			return e
		}
		if service.daemonClient != nil {
			e.err = service.daemonClient.Call(ctx, "operation.submit", request, &e.record)
			var rpcError *daemon.RPCError
			e.definitive = errors.As(e.err, &rpcError)
			e.err = fromDaemonError(e.err)
		} else {
			// Direct SDK/test mode has no daemon-owned admission service.
			node, err := service.NodeCreate(ctx, input)
			e.err, e.definitive = err, true
			e.record = NodeOperation{ID: request.ID, NodeID: node.ID, NodeSlug: node.Slug, Request: request, State: "succeeded"}
		}
		return e
	}
	if f.app.postEvent == nil {
		f.app.applyCreateAdmission(submit())
		return nil
	}
	go func() { f.app.postCompletion(submit()) }()
	return nil
}

func (a *vaxisTUIApp) applyCreateAdmission(e tuiCreateAdmissionEvent) {
	f := e.form
	f.pending = false
	if !f.visible() {
		if e.err != nil {
			a.setFooterStatus(slog.LevelWarn, "Creation admission: "+e.err.Error()+" · receipt "+e.request.ID)
		}
		return
	}
	if e.err != nil {
		if e.definitive {
			f.request = nil
			f.dialog.Error = e.err.Error()
			f.loadData()
		} else {
			f.request = &e.request
			f.dialog.Error = "Admission unconfirmed · " + e.request.ID + " · Ctrl+s retries this same request"
		}
		return
	}
	a.overlay = nil
	a.setFooterStatus(slog.LevelInfo, "Accepted creation of "+e.request.Create.Slug+" · continues after quit")
	a.createIntent = &tuiCreateIntent{operationID: e.record.ID, generation: a.managerGeneration, attach: f.attach, mode: a.managerSettings().Shell.Mode, record: e.record}
	a.applyCreateIntent(tuiCreateIntentEvent{intent: a.createIntent, record: e.record})
}

// The transient jump belongs only to this frontend and its current navigation.
// The durable operation survives closing the form, leaving the view or quitting.
func (a *vaxisTUIApp) pollCreateIntent() {
	i := a.createIntent
	if i == nil {
		return
	}
	if i.generation != a.managerGeneration {
		a.createIntent = nil
		return
	}
	if i.nodeID != "" {
		if !a.refreshInFlight {
			a.startDataRefresh(terminal.NodeTarget(i.nodeID).String())
		}
		return
	}
	if i.inFlight || a.service == nil || a.service.daemonClient == nil {
		return
	}
	i.inFlight = true
	ctx, cancel := (&tuiCreateNodeForm{app: a}).workContext()
	client, id := a.service.daemonClient, i.operationID
	load := func() tuiCreateIntentEvent {
		defer cancel()
		e := tuiCreateIntentEvent{intent: i}
		e.err = client.Call(ctx, "operation.get", map[string]string{"id": id}, &e.record)
		return e
	}
	if a.postEvent == nil {
		a.applyCreateIntent(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyCreateIntent(e tuiCreateIntentEvent) {
	i := a.createIntent
	if i == nil || i != e.intent {
		return
	}
	i.inFlight = false
	if i.generation != a.managerGeneration {
		a.createIntent = nil
		return
	}
	if e.err != nil {
		return
	}
	if e.record.ID != i.operationID {
		return
	}
	i.record = e.record
	if e.record.NodeID == "" {
		if e.record.State != "running" && e.record.State != "canceling" {
			a.createIntent = nil
			a.setFooterStatus(slog.LevelError, fmt.Sprintf("Creation %s: %s · see Activity", e.record.State, e.record.Error))
		}
		return
	}
	i.nodeID = e.record.NodeID
	a.fleet.setFilter("")
	// The new directory may be outside the original launch scope.
	a.treeWorkspaceRoot = ""
	a.fleet.groupByDir = a.managerSettings().GroupByDirectory
	a.startDataRefresh(terminal.NodeTarget(i.nodeID).String())
}

func (a *vaxisTUIApp) finishCreateNodeJump() {
	i := a.createIntent
	if i == nil || i.nodeID == "" {
		return
	}
	if i.generation != a.managerGeneration {
		a.createIntent = nil
		return
	}
	if a.state.selectedEntry().node.ID != i.nodeID {
		return
	}
	a.createIntent = nil
	a.openNodeDetail(tuiDetailOverview)
	a.lifecycle.nodeID, a.lifecycle.operation, a.lifecycle.loaded = i.nodeID, i.record, true
	if i.attach {
		a.lifecycle.queuedNode, a.lifecycle.queuedOperation, a.lifecycle.queuedMode = i.nodeID, i.operationID, i.mode
		a.finishQueuedLifecycleAttach()
	}
}
