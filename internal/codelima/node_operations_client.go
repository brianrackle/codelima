package codelima

import (
	"context"
	"fmt"
	"io"
	"time"
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

// TUI services carry a daemon client. The same public lifecycle methods then
// submit work and await a receipt; canceling the view only ends this wait.
// Daemon/SDK services have no client and execute the existing service path.
func (s *Service) daemonNodeOperation(ctx context.Context, request NodeOperationRequest) (Node, error) {
	request.ID = newID()
	var record NodeOperation
	if err := s.daemonClient.Call(ctx, "operation.submit", request, &record); err != nil {
		return Node{}, err
	}
	output := nodeOperationPrinter{writer: s.stdout}
	output.write(record)
	record, err := awaitNodeOperation(ctx, record, func(id string) (NodeOperation, error) {
		var updated NodeOperation
		err := s.daemonClient.Call(ctx, "operation.get", map[string]string{"id": id}, &updated)
		if err == nil {
			output.write(updated)
		}
		return updated, err
	})
	if err != nil {
		return Node{}, err
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
