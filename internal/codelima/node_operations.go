package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/google/uuid"
)

const nodeOperationRetention = 7 * 24 * time.Hour

var (
	errNodeOperationCanceled = errors.New("operation canceled by user")
	errNodeOperationsClosed  = errors.New("daemon stopped before operation completed")
)

type NodeOperationRequest struct {
	Environment string           `json:"environment,omitempty"`
	Origin      string           `json:"origin,omitempty"`
	ID          string           `json:"id"`
	Kind        string           `json:"kind"`
	Node        string           `json:"node,omitempty"`
	Create      *NodeCreateInput `json:"create,omitempty"`
	Clone       *NodeCloneInput  `json:"clone,omitempty"`
}

func (r NodeOperationRequest) startsNode() bool {
	return r.Kind == "start" || r.Kind == "retry" || r.Kind == "restart" || r.Kind == "create" && r.Create != nil && r.Create.Start || r.Kind == "clone" && r.Clone != nil && r.Clone.Start
}

type NodeOperation struct {
	EnvironmentTest *EnvironmentTestState  `json:"environment_test,omitempty"`
	Progress        *NodeOperationProgress `json:"progress,omitempty"`
	ID              string                 `json:"id"`
	Request         NodeOperationRequest   `json:"request"`
	NodeID          string                 `json:"node_id,omitempty"`
	NodeSlug        string                 `json:"node_slug,omitempty"`
	State           string                 `json:"state"`
	CreatedAt       time.Time              `json:"created_at"`
	FinishedAt      *time.Time             `json:"finished_at,omitempty"`
	Error           string                 `json:"error,omitempty"`
	ErrorCode       int                    `json:"error_code,omitempty"`
	ErrorCategory   string                 `json:"error_category,omitempty"`
	Result          *NodeOperationResult   `json:"result,omitempty"`
	Output          []string               `json:"output,omitempty"`
	OutputStart     uint64                 `json:"output_start,omitempty"`
	OutputPartial   string                 `json:"output_partial,omitempty"`
	CurrentPhase    string                 `json:"current_phase,omitempty"`
	Phases          []NodeOperationPhase   `json:"phases,omitempty"`
}

type NodeOperationResult struct {
	NodeID   string     `json:"node_id"`
	NodeSlug string     `json:"node_slug"`
	Status   NodeStatus `json:"status"`
}

// A clone initially belongs to the source. It becomes a child start attempt
// only after the child is bound and startup's first phase has been persisted.
// CurrentPhase survives the bounded list projection; full phase bodies do not.
func (o NodeOperation) startsNode() bool {
	return o.Request.startsNode() && (o.Request.Kind != "clone" || o.CurrentPhase != "" && o.CurrentPhase != "clone")
}

type runningNodeOperation struct {
	record NodeOperation
	keys   []string
	cancel context.CancelCauseFunc
}

// nodeOperations owns execution, independently of any requesting connection.
// The daemon's process lock supplies single-writer ownership of these files;
// service liveness tokens still arbitrate with direct CLI/SDK operations.
type nodeOperations struct {
	cleanupCtx    context.Context
	cancelCleanup context.CancelFunc
	service       *Service
	onChange      func()
	dir           string
	mu            sync.Mutex
	closed        bool
	records       map[string]*runningNodeOperation
	workers       sync.WaitGroup
}

func newNodeOperations(service *Service, onChange func()) (*nodeOperations, error) {
	localService := *service
	localService.daemonClient = nil
	localService.operationConnect = nil
	localService.operationPhase = nil
	localService.operationReceipt = nil
	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	loaded := false
	defer func() {
		if !loaded {
			cancelCleanup()
		}
	}()
	m := &nodeOperations{service: &localService, dir: filepath.Join(service.cfg.MetadataRoot, "_operations"), records: make(map[string]*runningNodeOperation), cleanupCtx: cleanupCtx, cancelCleanup: cancelCleanup, onChange: onChange}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		data, _, clipped, err := readNodeLogTail(filepath.Join(m.dir, file.Name()), 256<<10)
		if err != nil || clipped {
			return nil, fmt.Errorf("read operation %s: %w", id, errors.Join(err, errors.New("operation record unavailable or oversized")))
		}
		var record NodeOperation
		if err := json.Unmarshal(data, &record); err != nil || record.ID != id {
			return nil, fmt.Errorf("invalid operation record %s", id)
		}
		if operationExpired(record, service.now()) {
			if err := os.Remove(filepath.Join(m.dir, file.Name())); err != nil {
				return nil, err
			}
			continue
		}
		if record.State == "running" || record.State == "canceling" {
			record.State, record.Error = "interrupted", errNodeOperationsClosed.Error()
			record.FinishedAt = nil
			finishNodeOperationPhases(&record, "interrupted", nil)
			if err := m.persist(record); err != nil {
				return nil, err
			}
		}
		m.records[id] = &runningNodeOperation{record: record}
	}
	var recovery []*runningNodeOperation
	for _, operation := range m.records {
		test := operation.record.EnvironmentTest
		if test == nil || !test.CleanupPending {
			continue
		}
		if err := m.service.validateEnvironmentTestPlan(test.Plan); err != nil {
			return nil, err
		}
		if operation.record.NodeID != test.Plan.Node.ID || operation.record.Request.Kind != "test-environment" {
			return nil, errors.New("test cleanup receipt has inconsistent node ownership")
		}
		if test.Outcome == "" {
			test.Outcome, test.Error = "interrupted", errNodeOperationsClosed.Error()
		}
		operation.record.State, operation.record.FinishedAt = "running", nil
		if err := m.persist(operation.record); err != nil {
			return nil, err
		}
		operation.keys = environmentTestKeys(test)
		recovery = append(recovery, operation)
	}
	loaded = true
	for _, operation := range recovery {
		ctx, cancel := context.WithCancelCause(context.Background())
		operation.cancel = cancel
		m.workers.Add(1)
		go func() { defer m.workers.Done(); m.runEnvironmentTest(ctx, operation, true) }()
	}
	return m, nil
}

func operationExpired(record NodeOperation, now time.Time) bool {
	if record.EnvironmentTest != nil && record.EnvironmentTest.CleanupPending {
		return false
	}
	if record.State == "running" || record.State == "canceling" {
		return false
	}
	at := record.CreatedAt
	if record.FinishedAt != nil {
		at = *record.FinishedAt
	}
	return now.Sub(at) > nodeOperationRetention
}

func (m *nodeOperations) persist(record NodeOperation) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > 256<<10 {
		return fmt.Errorf("operation record exceeds 256 KiB")
	}
	return atomicWriteFile(filepath.Join(m.dir, record.ID+".json"), data, 0o600)
}

func cloneNodeOperation(record NodeOperation) NodeOperation {
	// Wire values contain nested node/recipe slices and maps. Queries must not
	// hand their mutable storage to callers or retain caller-owned request data.
	data, _ := json.Marshal(record)
	var result NodeOperation
	_ = json.Unmarshal(data, &result)
	return result
}

func (m *nodeOperations) normalize(request NodeOperationRequest) (NodeOperationRequest, Node, []string, error) {
	if request.Origin != "" && request.Origin != "tui" && request.Origin != "cli" {
		return request, Node{}, nil, invalidArgument("operation origin must be tui or cli", nil)
	}
	if (request.Kind != "create" && request.Create != nil) || (request.Kind != "clone" && request.Clone != nil) || (request.Kind != "test-environment" && request.Environment != "") {
		return request, Node{}, nil, invalidArgument("operation inputs do not match its kind", nil)
	}
	switch request.Kind {
	case "test-environment":
		if request.Environment == "" || request.Node != "" {
			return request, Node{}, nil, invalidArgument("environment is required and real nodes cannot be test targets", nil)
		}
		return request, Node{}, nil, nil
	case "start", "retry", "restart", "stop", "delete", "clone":
		query := request.Node
		if request.Kind == "clone" {
			if request.Clone == nil {
				return request, Node{}, nil, invalidArgument("clone input is required", nil)
			}
			query = request.Clone.SourceNode
		}
		node, err := m.service.store.NodeByIDOrSlug(query)
		if err != nil {
			return request, Node{}, nil, err
		}
		// A create reserves its slug before the node has an ID. Keep that key
		// on existing-node mutations to exclude competitors during first boot.
		keys := []string{"node:" + node.ID, "slug:" + node.Slug}
		if request.Clone != nil {
			keys = append(keys, "slug:"+request.Clone.NodeSlug)
		}
		return request, node, keys, nil
	case "create":
		if request.Create == nil {
			return request, Node{}, nil, invalidArgument("create input is required", nil)
		}
		return request, Node{Slug: request.Create.Slug}, []string{"slug:" + request.Create.Slug}, nil
	default:
		return request, Node{}, nil, invalidArgument("unknown lifecycle operation", nil)
	}
}

func (m *nodeOperations) Submit(ctx context.Context, request NodeOperationRequest) (NodeOperation, error) {
	if err := ctx.Err(); err != nil {
		return NodeOperation{}, err
	}
	if data, err := json.Marshal(request); err != nil || len(data) > 64<<10 {
		return NodeOperation{}, invalidArgument("operation request exceeds 64 KiB", nil)
	}
	request = cloneNodeOperation(NodeOperation{Request: request}).Request
	parsed, err := uuid.Parse(request.ID)
	if err != nil {
		return NodeOperation{}, invalidArgument("operation id must be a UUID", nil)
	}
	request.ID = parsed.String()
	// Retry delivery by ID before resolving a target: a successful delete or
	// rename must not turn a lost response into a second operation.
	m.mu.Lock()
	previous, repeated, err := m.repeatedRequestLocked(request)
	m.mu.Unlock()
	if repeated || err != nil {
		return previous, err
	}
	request, node, keys, err := m.normalize(request)
	if err != nil {
		return NodeOperation{}, err
	}
	var environmentTest *EnvironmentTestState
	if request.Kind == "test-environment" {
		environmentTest, err = m.service.prepareEnvironmentTest(ctx, request.Environment)
		if err != nil {
			return NodeOperation{}, err
		}
		node = environmentTest.Plan.Node
		keys = environmentTestKeys(environmentTest)
	}
	m.mu.Lock()
	changed := false
	defer func() {
		m.mu.Unlock()
		if changed && m.onChange != nil {
			m.onChange()
		}
	}()
	if m.closed {
		return NodeOperation{}, errNodeOperationsClosed
	}
	if previous, repeated, err := m.repeatedRequestLocked(request); repeated || err != nil {
		return previous, err
	}
	if err := m.pruneLocked(); err != nil {
		return NodeOperation{}, err
	}
	active := 0
	for _, other := range m.records {
		if other.cancel == nil {
			continue
		}
		active++
		if operationConflicts(other.keys, keys) {
			return NodeOperation{}, preconditionFailed("node already has an active "+other.record.Request.Kind+" operation", map[string]any{"operation_id": other.record.ID})
		}
	}
	if active >= 16 {
		return NodeOperation{}, preconditionFailed("16 lifecycle operations are already running", nil)
	}
	record := NodeOperation{ID: request.ID, Request: request, NodeID: node.ID, NodeSlug: node.Slug, State: "running", CreatedAt: m.service.now(), EnvironmentTest: environmentTest}
	record.Phases = initialNodeOperationPhases(request.Kind)
	if (request.Kind == "create" || request.Kind == "clone") && request.startsNode() {
		record.Phases = append(record.Phases, initialNodeOperationPhases("start")...)
	}
	if err := m.persist(record); err != nil {
		return NodeOperation{}, err
	}
	operationCtx, cancel := context.WithCancelCause(context.Background())
	running := &runningNodeOperation{record: record, keys: keys, cancel: cancel}
	m.records[record.ID] = running
	changed = true
	m.workers.Add(1)
	go m.run(operationCtx, running)
	return cloneNodeOperation(record), nil
}

// Publish the created identity before boot can make additional side effects.
// The slug admission already excludes competing operations on the new node.
func (m *nodeOperations) bindCreatedNode(operation *runningNodeOperation, node Node) error {
	m.mu.Lock()
	operation.record.NodeID, operation.record.NodeSlug = node.ID, node.Slug
	operation.keys = append(operation.keys, "node:"+node.ID)
	err := m.persist(operation.record)
	m.mu.Unlock()
	if m.onChange != nil {
		m.onChange()
	}
	return err
}

func (m *nodeOperations) repeatedRequestLocked(request NodeOperationRequest) (NodeOperation, bool, error) {
	previous := m.records[request.ID]
	if previous == nil || operationExpired(previous.record, m.service.now()) {
		return NodeOperation{}, false, nil
	}
	prior := previous.record.Request
	prior.Origin, request.Origin = "", ""
	if !reflect.DeepEqual(prior, request) {
		return NodeOperation{}, true, preconditionFailed("operation id already belongs to another request", nil)
	}
	return cloneNodeOperation(previous.record), true, nil
}

func (m *nodeOperations) pruneLocked() error {
	for id, operation := range m.records {
		if !operationExpired(operation.record, m.service.now()) {
			continue
		}
		if err := os.Remove(filepath.Join(m.dir, id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		delete(m.records, id)
	}
	return nil
}

func (m *nodeOperations) run(ctx context.Context, operation *runningNodeOperation) {
	defer m.workers.Done()
	if operation.record.Request.Kind == "test-environment" {
		m.runEnvironmentTest(ctx, operation, false)
		return
	}
	output := newNodeOperationOutput(m, operation)
	request := operation.record.Request
	service := m.service.withIO(m.service.stdout, m.service.stderr)
	if request.Kind == "start" || request.Kind == "retry" || request.Kind == "restart" {
		// Bootstrap owns explicit streams on every runtime implementation.
		// Tee that source once; Lima also mirrors it to its diagnostic writers.
		service.operationOutput = output
	} else {
		service = m.service.withIO(operationOutputTee(m.service.stdout, output), operationOutputTee(m.service.stderr, output))
	}
	nodeID := operation.record.NodeID
	cleanupNodeID := ""
	if request.Kind == "start" || request.Kind == "retry" || request.Kind == "restart" {
		cleanupNodeID = nodeID
	}
	var node Node
	var err error
	service.operationPhase = func(id, detail string, skipped bool) error {
		return m.advancePhase(operation, id, detail, skipped)
	}
	firstPhase := request.Kind
	if firstPhase == "start" || firstPhase == "retry" {
		firstPhase = "prepare"
	} else if firstPhase == "restart" {
		firstPhase = "stop"
	}
	if err = service.advanceOperationPhase(firstPhase, "", false); err == nil {
		switch request.Kind {
		case "start":
			node, err = service.NodeStart(ctx, nodeID)
		case "retry":
			node, err = service.NodeRetry(ctx, nodeID)
		case "restart":
			node, err = service.NodeRestart(ctx, nodeID)
		case "stop":
			node, err = service.NodeStop(ctx, nodeID)
		case "delete":
			node, err = service.NodeDelete(ctx, nodeID)
		case "create", "clone":
			if request.Kind == "create" {
				node, err = service.createNode(ctx, *request.Create)
			} else {
				input := *request.Clone
				input.SourceNode = nodeID
				node, err = service.cloneNode(ctx, input)
			}
			if err == nil && request.startsNode() {
				nodeID = node.ID
				err = m.bindCreatedNode(operation, node)
				if err == nil {
					// Creation/clone tokens have been released and any source
					// restoration has completed. Reserve only the child for
					// cancellation cleanup, never the clone's original source.
					cleanupNodeID = nodeID
					startup := m.service.withIO(m.service.stdout, m.service.stderr)
					startup.operationPhase, startup.operationOutput = service.operationPhase, output
					err = startup.advanceOperationPhase("prepare", "start the created VM", false)
					if err == nil {
						node, err = startup.startCommittedNode(ctx, node)
					}
				}
			}
		}
	}
	err = errors.Join(err, output.Close())
	m.mu.Lock()
	cause := context.Cause(ctx)
	phaseState := "succeeded"
	if err != nil {
		phaseState = "failed"
	}
	if errors.Is(cause, errNodeOperationCanceled) {
		phaseState = "canceled"
	}
	if errors.Is(cause, errNodeOperationsClosed) {
		phaseState = "interrupted"
	}
	executionFinished := m.service.now()
	finishNodeOperationPhases(&operation.record, phaseState, &executionFinished)
	stopFailed := false
	if errors.Is(cause, errNodeOperationCanceled) && cleanupNodeID != "" {
		// The start released its liveness token. Keep this manager's resource
		// reservation while stopping the VM on an independent bounded context.
		m.mu.Unlock()
		stopCtx, cancel := context.WithTimeout(context.Background(), limaCommandTimeout)
		stopped, stopErr := m.service.NodeStop(stopCtx, cleanupNodeID)
		cancel()
		if stopErr == nil {
			node = stopped
		} else {
			stopFailed = true
			err = errors.Join(err, fmt.Errorf("cancel could not stop VM: %w", stopErr))
		}
		m.mu.Lock()
	}
	defer func() {
		m.mu.Unlock()
		if m.onChange != nil {
			m.onChange()
		}
	}()
	finished := m.service.now()
	operation.record.FinishedAt = &finished
	operation.record.State = "succeeded"
	if err != nil {
		operation.record.State = "failed"
		operation.record.Error = err.Error()
		wireError := daemon.AsRPCError(toDaemonError(err))
		operation.record.ErrorCode, operation.record.ErrorCategory = wireError.Code, wireError.Category
	}
	if errors.Is(cause, errNodeOperationCanceled) && !stopFailed {
		operation.record.State = "canceled"
	}
	if errors.Is(cause, errNodeOperationsClosed) {
		operation.record.State, operation.record.Error = "interrupted", cause.Error()
		operation.record.FinishedAt = nil
	}
	if node.ID != "" {
		operation.record.NodeID, operation.record.NodeSlug = node.ID, node.Slug
		operation.record.Result = &NodeOperationResult{NodeID: node.ID, NodeSlug: node.Slug, Status: node.Status}
	}
	operation.cancel = nil
	if len(operation.record.Error) > 4096 {
		operation.record.Error = strings.ToValidUTF8(operation.record.Error[:4096], "�")
	}
	if saveErr := m.persist(operation.record); saveErr != nil {
		operation.record.State = "failed"
		operation.record.Error = errors.Join(err, saveErr).Error()
		operation.record.ErrorCode, operation.record.ErrorCategory = ExitInternalFailure, "Internal"
		m.service.log().Error("operation completion could not be persisted", "operation", request.ID, "error", saveErr)
	}
}

func (m *nodeOperations) Get(id string) (NodeOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	operation := m.records[id]
	if operation == nil || operationExpired(operation.record, m.service.now()) {
		return NodeOperation{}, notFound("operation not found", map[string]any{"operation_id": id})
	}
	return cloneNodeOperation(operation.record), nil
}

// Node detail needs the newest start attempt, independent of Activity's cursor
// and completion ordering. A later stop/delete must not hide its failure.
func (m *nodeOperations) LatestStart(nodeID string) (NodeOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *NodeOperation
	for _, operation := range m.records {
		record := &operation.record
		if record.NodeID != nodeID || operationExpired(*record, m.service.now()) || !record.startsNode() {
			continue
		}
		if latest == nil || record.CreatedAt.After(latest.CreatedAt) || record.CreatedAt.Equal(latest.CreatedAt) && record.ID > latest.ID {
			latest = record
		}
	}
	if latest == nil {
		return NodeOperation{}, notFound("no retained start operation for node", map[string]any{"node_id": nodeID})
	}
	return cloneNodeOperation(*latest), nil
}

func (m *nodeOperations) List() []NodeOperation {
	m.mu.Lock()
	defer m.mu.Unlock()
	var records []NodeOperation
	for _, operation := range m.records {
		if !operationExpired(operation.record, m.service.now()) {
			// Listing is a summary projection. Do not serialize and clone every
			// retained command/output body on each frontend's polling tick.
			record := operation.record
			record.Progress = summarizeNodeOperationProgress(record)
			createStart := record.Request.Kind == "create" && record.Request.startsNode()
			cloneStart := record.Request.Kind == "clone" && record.Request.startsNode()
			record.Request = NodeOperationRequest{ID: record.ID, Kind: record.Request.Kind, Origin: record.Request.Origin}
			if createStart {
				// Keep the startup contract without copying the full create input.
				record.Request.Create = &NodeCreateInput{Start: true}
			}
			if cloneStart {
				record.Request.Clone = &NodeCloneInput{Start: true}
			}
			record.Result = nil
			record.Output, record.OutputPartial = nil, ""
			record.Phases = nil
			if record.EnvironmentTest != nil {
				summary := *record.EnvironmentTest
				summary.Plan = nil
				record.EnvironmentTest = &summary
			}
			record.NodeSlug = boundedOperationText(record.NodeSlug, 1024)
			record.Error = boundedOperationText(record.Error, 4096)
			if record.FinishedAt != nil {
				finished := *record.FinishedAt
				record.FinishedAt = &finished
			}
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		left, right := nodeOperationHistoryTime(records[i]), nodeOperationHistoryTime(records[j])
		if left.Equal(right) {
			return records[i].ID > records[j].ID
		}
		return left.After(right)
	})
	return records
}

func nodeOperationHistoryTime(record NodeOperation) time.Time {
	if record.FinishedAt != nil {
		return *record.FinishedAt
	}
	return record.CreatedAt
}

type NodeOperationsPage struct {
	Running []NodeOperation `json:"running"`
	History []NodeOperation `json:"history"`
	Next    string          `json:"next,omitempty"`
}

func (m *nodeOperations) ListPage(before string, limit int) (NodeOperationsPage, error) {
	if limit < 0 || limit > 100 {
		return NodeOperationsPage{}, invalidArgument("operation page limit must be between 1 and 100", nil)
	}
	if limit == 0 {
		limit = 50
	}
	m.mu.Lock()
	err := m.pruneLocked()
	m.mu.Unlock()
	if err != nil {
		return NodeOperationsPage{}, err
	}
	var page NodeOperationsPage
	var history []NodeOperation
	for _, record := range m.List() {
		// Always include every running operation on each history page.
		if record.State == "running" || record.State == "canceling" {
			page.Running = append(page.Running, record)
		} else {
			history = append(history, record)
		}
	}
	start := 0
	if before != "" {
		start = -1
		for i, record := range history {
			if record.ID == before {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return NodeOperationsPage{}, notFound("operation page cursor expired; reload history", nil)
		}
	}
	end := min(len(history), start+limit)
	page.History = history[start:end]
	for len(page.History) > 1 {
		data, err := json.Marshal(page)
		if err != nil {
			return NodeOperationsPage{}, err
		}
		if len(data) < daemon.MaxMessageSize/2-1024 {
			break
		}
		end--
		page.History = history[start:end]
	}
	if end < len(history) {
		page.Next = history[end-1].ID
	}
	return page, nil
}

func boundedOperationText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "�") + "…"
}

func (m *nodeOperations) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, operation := range m.records {
		if operation.cancel != nil {
			return true
		}
	}
	return false
}

func (m *nodeOperations) Cancel(id string) error {
	m.mu.Lock()
	changed := false
	defer func() {
		m.mu.Unlock()
		if changed && m.onChange != nil {
			m.onChange()
		}
	}()
	operation := m.records[id]
	if operation == nil {
		return notFound("operation not found", nil)
	}
	if operation.cancel == nil {
		return nil
	}
	previous := operation.record.State
	operation.record.State = "canceling"
	if err := m.persist(operation.record); err != nil {
		operation.record.State = previous
		return err
	}
	operation.cancel(errNodeOperationCanceled)
	changed = true
	return nil
}

func (m *nodeOperations) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.cancelCleanup()
	for _, operation := range m.records {
		if operation.cancel != nil {
			operation.cancel(errNodeOperationsClosed)
		}
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
