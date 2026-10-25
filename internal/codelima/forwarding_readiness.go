package codelima

import "context"

// ApplicationForwardingRuntime removes runtime-owned application forwards on
// a stopped instance, before boot can expose a partially provisioned service.
// Implementations must reject running instances and preserve other settings.
type ApplicationForwardingRuntime interface {
	PrepareApplicationForwarding(context.Context, Node) error
}

func (c *LimaClient) PrepareApplicationForwarding(ctx context.Context, node Node) error {
	if err := validateSandboxName(node.SandboxName); err != nil {
		return err
	}
	// limactl edit rejects running instances; --tty=false prevents prompting or
	// implicitly starting the instance. The replacement covers inherited static
	// rules as well as discovery, while Node.Ports retains host:guest mappings.
	const expression = `.portForwards = [{"guestIP":"0.0.0.0","guestIPMustBeZero":false,"guestPortRange":[1,65535],"proto":"any","ignore":true}]`
	_, err := c.runDirect(ctx, limaCommandTimeout, "prepare daemon port forwarding", map[string]any{"sandbox_name": node.SandboxName}, c.binary(), "edit", "--tty=false", "--set", expression, node.SandboxName)
	return err
}

func applicationRoutesReady(node Node) bool {
	if !node.BootstrapCompleted {
		return false
	}
	// A transient cached stopped observation does not revoke an already-ready
	// guest that still answers its scan. The forwarder's stop grace owns that
	// decision. Explicit provisioning and failure always close application routes.
	return node.Status == NodeStatusRunning || node.Status == NodeStatusStopped
}
