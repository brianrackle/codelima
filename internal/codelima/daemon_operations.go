package codelima

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

// Recovery starts only after the daemon owns its process lock. It does not
// depend on a frontend opening Activity or sending another operation request.
func (h *daemonHost) startOwnedWorkers(ctx context.Context) {
	h.startActivityEvents(ctx)
	if _, err := h.nodeOperations(); err != nil {
		h.service.log().Error("operation recovery could not start", "error", err)
	}
}

func (h *daemonHost) nodeOperations() (*nodeOperations, error) {
	h.operationsMu.Lock()
	defer h.operationsMu.Unlock()
	if h.operationsClosed {
		return nil, errNodeOperationsClosed
	}
	if h.operations == nil && h.operationsErr == nil {
		h.operations, h.operationsErr = newNodeOperations(h.service, func() { h.broadcast(daemon.EventNodeOperationChanged, nil) })
	}
	return h.operations, h.operationsErr
}

func (h *daemonHost) handleNodeOperation(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	// Serialize admission against daemon replacement, not against another
	// operation's runtime. Once admitted, the manager owns its context.
	if err := h.enterTerminalMutation(); err != nil {
		return nil, err
	}
	defer h.leaveTerminalMutation()
	operations, err := h.nodeOperations()
	if err != nil {
		return nil, toDaemonError(err)
	}
	switch method {
	case "operation.submit":
		var request NodeOperationRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, toDaemonError(invalidArgument("invalid operation request", nil))
		}
		record, err := operations.Submit(ctx, request)
		return record, toDaemonError(err)
	case "operation.list":
		var params struct {
			Before string `json:"before"`
			Limit  int    `json:"limit"`
		}
		if len(raw) != 0 {
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, toDaemonError(invalidArgument("invalid operation page", nil))
			}
		}
		page, err := operations.ListPage(params.Before, params.Limit)
		return page, toDaemonError(err)
	case "operation.latest":
		var params struct {
			Node string `json:"node"`
		}
		if err := json.Unmarshal(raw, &params); err != nil || strings.TrimSpace(params.Node) == "" {
			return nil, toDaemonError(invalidArgument("node is required", nil))
		}
		node, err := h.service.store.NodeByIDOrSlug(params.Node)
		if err != nil {
			return nil, toDaemonError(err)
		}
		record, err := operations.LatestStart(node.ID)
		return record, toDaemonError(err)
	default:
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &params); err != nil || strings.TrimSpace(params.ID) == "" {
			return nil, toDaemonError(invalidArgument("operation id is required", nil))
		}
		if method == "operation.cancel" {
			if err := operations.Cancel(params.ID); err != nil {
				return nil, toDaemonError(err)
			}
		}
		record, err := operations.Get(params.ID)
		return record, toDaemonError(err)
	}
}

func (h *daemonHost) closeNodeOperations() error {
	h.operationsMu.Lock()
	h.operationsClosed = true
	operations := h.operations
	h.operationsMu.Unlock()
	if operations == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return operations.Close(ctx)
}

func (h *daemonHost) activeNodeOperations() bool {
	h.operationsMu.Lock()
	operations := h.operations
	h.operationsMu.Unlock()
	return operations != nil && operations.Active()
}

// Existing synchronous RPC callers wait for the durable operation receipt.
// Losing this request connection stops waiting; it does not cancel execution.
func (h *daemonHost) runLegacyNodeOperation(ctx context.Context, kind, node string) (Node, error) {
	raw, _ := json.Marshal(NodeOperationRequest{ID: newID(), Kind: kind, Node: node})
	result, err := h.handleNodeOperation(ctx, "operation.submit", raw)
	if err != nil {
		return Node{}, err
	}
	operation := result.(NodeOperation)
	operations, err := h.nodeOperations()
	if err != nil {
		return Node{}, err
	}
	operation, err = awaitNodeOperation(ctx, operation, operations.Get)
	if err != nil {
		return Node{}, toDaemonError(err)
	}
	return h.service.store.NodeByID(operation.NodeID)
}

// Keep protocol errors typed at the RPC boundary, including failures stored
// after the original connection has gone away.
func operationFailure(record NodeOperation) error {
	message := record.Error
	if message == "" {
		message = "operation " + record.State
	}
	code := record.ErrorCode
	if code == 0 {
		code = ExitExternalFailure
	}
	return daemon.Error(coalesce(record.ErrorCategory, "ExternalCommandFailed"), message, code, map[string]any{"operation_id": record.ID, "state": record.State})
}
