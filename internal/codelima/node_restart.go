package codelima

import "context"

// NodeRestart confirms the VM is stopped before starting it under the same
// lifecycle claim. Completed bootstrap stays frozen; failed bootstrap follows
// ordinary start semantics. NodeRetry is the separate explicit resume action.
func (s *Service) NodeRestart(ctx context.Context, value string) (_ Node, err error) {
	if s.daemonClient != nil || s.operationConnect != nil {
		return s.daemonNodeOperation(ctx, NodeOperationRequest{Kind: "restart", Node: value})
	}
	const op = "node.restart"
	s.log().Debug("operation started", "op", op, "node", value)
	defer s.logOperation(op, s.now(), &err)
	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}
	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}
	token, err := s.claimNodeOperation(op, node.ID)
	if err != nil {
		return Node{}, err
	}
	defer token.release()
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	if err := s.advanceOperationPhase("stop", "confirm the VM has stopped before booting", false); err != nil {
		return Node{}, err
	}
	stopService := s
	if s.operationOutput != nil {
		stopService = s.withIO(operationOutputTee(s.stdout, s.operationOutput), operationOutputTee(s.stderr, s.operationOutput))
	}
	if _, err := stopService.nodeStopClaimed(ctx, node); err != nil {
		return Node{}, err
	}
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	if err := s.advanceOperationPhase("prepare", "start the stopped VM", false); err != nil {
		return Node{}, err
	}
	return s.nodeStartClaimed(ctx, node, false, op)
}
