package codelima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func awaitNodeOperation(ctx context.Context, record NodeOperation, get func(string) (NodeOperation, error)) (NodeOperation, error) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		switch record.State {
		case "succeeded":
			return record, nil
		case "failed", "canceled", "interrupted":
			return record, operationFailure(record)
		}
		select {
		case <-ctx.Done():
			return record, ctx.Err()
		case <-tick.C:
			updated, err := get(record.ID)
			if err != nil {
				return record, err
			}
			record = updated
		}
	}
}

// TUI services carry a daemon client; CLI services connect lazily. Both submit
// work and await a receipt; canceling the caller only ends this wait.
// Daemon/SDK services have neither and execute the existing service path.
func (s *Service) daemonNodeOperation(ctx context.Context, request NodeOperationRequest) (Node, error) {
	if request.Create != nil {
		input := *request.Create
		directory, err := s.resolveNodeDirectoryPath(input.Directory)
		if err != nil {
			return Node{}, err
		}
		input.Directory = directory
		request.Create = &input
	}
	client, writer := s.daemonClient, s.stdout
	request.Origin = "tui"
	if client == nil {
		request.Origin = "cli"
		var err error
		client, err = s.operationConnect(ctx)
		if err != nil {
			return Node{}, err
		}
		defer func() { _ = client.Close() }()
		// CLI result envelopes own stdout. Progress belongs on stderr; a TUI
		// supplies its own progress writer with its already-connected service.
		writer = s.stderr
	}
	request.ID = newID()
	var record NodeOperation
	if err := client.Call(ctx, "operation.submit", request, &record); err != nil {
		var rpcErr *daemon.RPCError
		if errors.As(err, &rpcErr) {
			return Node{}, fromDaemonError(err)
		}
		return Node{}, operationWaitError(request.ID, "admission was not confirmed", err)
	}
	if s.operationReceipt != nil {
		s.operationReceipt(record.ID)
	}
	output := nodeOperationPrinter{writer: writer}
	output.write(record)
	record, err := awaitNodeOperation(ctx, record, func(id string) (NodeOperation, error) {
		var updated NodeOperation
		err := client.Call(ctx, "operation.get", map[string]string{"id": id}, &updated)
		if err == nil {
			output.write(updated)
		}
		return updated, err
	})
	if err != nil {
		if record.State == "running" || record.State == "canceling" {
			return Node{}, operationWaitError(record.ID, "stopped waiting for operation", err)
		}
		return Node{}, fromDaemonError(err)
	}
	node, err := s.store.NodeByID(record.NodeID)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&node); err != nil {
		return Node{}, err
	}
	return node, nil
}

// A lost response is not evidence that work failed or never started. Retain
// the client-generated id even when admission itself may have succeeded.
func operationWaitError(id, message string, err error) error {
	return dependencyUnavailable(fmt.Sprintf("%s %s; inspect with codelima operation show %s before retrying", message, id, id), err, map[string]any{"operation_id": id})
}

type nodeOperationPrinter struct {
	writer   io.Writer
	next     uint64
	finished bool
}

func (p *nodeOperationPrinter) write(record NodeOperation) {
	if p.writer == nil || p.finished {
		return
	}
	p.next = max(p.next, record.OutputStart)
	for i, line := range record.Output {
		sequence := record.OutputStart + uint64(i)
		if sequence >= p.next {
			_, _ = fmt.Fprintln(p.writer, plainLogText(line))
			p.next = sequence + 1
		}
	}
	if record.State != "running" && record.State != "canceling" {
		p.finished = true
		if record.OutputPartial != "" {
			_, _ = fmt.Fprintln(p.writer, plainLogText(record.OutputPartial))
		}
	}
}
