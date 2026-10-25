package codelima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestForwardingReadinessHoldsApplicationsWithoutSuppressingTelemetry(t *testing.T) {
	h := newForwardingHarness(t, "starting")
	h.peer.observations = []forwardingGuestObservation{{Memory: guestResourceUsage{UsedBytes: 10, TotalBytes: 20}}}
	node, bootstrap := h.node, BootstrapState{}
	node.Status, node.LifecycleState = NodeStatusProvisioning, NodeStatusProvisioning
	node.BootstrapCompleted = false
	if err := h.service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatal(err)
	}
	h.forwarder.reconcile(context.Background())
	if len(h.forwarder.Snapshot()["routes"].([]map[string]any)) != 0 {
		t.Fatal("provisioning VM published application routes")
	}
	if got := forwardingNodeSnapshot(h.forwarder, node.SandboxName)["health"]; got != "healthy" {
		t.Fatalf("readiness suppressed the guest probe: %v", got)
	}
	if h.peer.next != 1 {
		t.Fatal("provisioning suppressed telemetry")
	}
	node.Status, node.LifecycleState = NodeStatusRunning, ""
	node.BootstrapCompleted = true
	if err := h.service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatal(err)
	}
	h.forwarder.reconcile(context.Background())
	if len(h.forwarder.Snapshot()["routes"].([]map[string]any)) != 1 {
		t.Fatal("ready VM did not publish its listener")
	}
	node.Status, node.LifecycleState = NodeStatusFailed, NodeStatusFailed
	if err := h.service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatal(err)
	}
	h.forwarder.reconcile(context.Background())
	if len(h.forwarder.Snapshot()["routes"].([]map[string]any)) != 0 {
		t.Fatal("failed VM kept application routes")
	}
}

type forwardingSandbox struct {
	*fakeSandbox
	prepareErr error
}

func (s *forwardingSandbox) PrepareApplicationForwarding(_ context.Context, node Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "prepare-forwarding "+node.SandboxName)
	return s.prepareErr
}

func TestNodeForwardingMigrationPrecedesBootAndPersists(t *testing.T) {
	service, workspace := newTestService(t)
	node, err := service.NodeCreate(context.Background(), NodeCreateInput{Directory: workspace, Slug: "migration", Ports: []string{"15432:5432"}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &forwardingSandbox{fakeSandbox: service.sandbox.(*fakeSandbox)}
	service.sandbox = runtime
	runtime.calls = nil
	started, err := service.NodeStart(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !started.DaemonPortForwarding {
		t.Fatal("migration not recorded")
	}
	loaded, err := service.store.NodeByID(node.ID)
	if err != nil || !loaded.DaemonPortForwarding || !slices.Equal(loaded.Ports, node.Ports) {
		t.Fatalf("persisted migration = %+v: %v", loaded, err)
	}
	prepare := slices.Index(runtime.calls, "prepare-forwarding "+node.SandboxName)
	start := slices.IndexFunc(runtime.calls, func(call string) bool { return strings.HasPrefix(call, "start ") })
	if prepare < 0 || start <= prepare {
		t.Fatalf("migration/boot order: %v", runtime.calls)
	}
	// Re-entering start on an already-running VM cannot edit or restart it.
	runtime.calls = nil
	if _, err := service.NodeStart(context.Background(), node.ID); err != nil {
		t.Fatal(err)
	}
	for _, call := range runtime.calls {
		if strings.HasPrefix(call, "prepare-forwarding ") || strings.HasPrefix(call, "start ") {
			t.Fatalf("running VM mutated: %s", call)
		}
	}
}

func TestNodeForwardingMigrationFailureDoesNotBootOrMarkManaged(t *testing.T) {
	service, workspace := newTestService(t)
	node, err := service.NodeCreate(context.Background(), NodeCreateInput{Directory: workspace, Slug: "migration-failure"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &forwardingSandbox{fakeSandbox: service.sandbox.(*fakeSandbox), prepareErr: errors.New("edit failed")}
	service.sandbox = runtime
	runtime.calls = nil
	if _, err := service.NodeStart(context.Background(), node.ID); !errors.Is(err, runtime.prepareErr) {
		t.Fatalf("start error = %v", err)
	}
	loaded, err := service.store.NodeByID(node.ID)
	if err != nil || loaded.DaemonPortForwarding {
		t.Fatalf("failed migration marked managed: %+v %v", loaded, err)
	}
	for _, call := range runtime.calls {
		if strings.HasPrefix(call, "start ") {
			t.Fatal("booted after failed migration")
		}
	}
}

func TestLimaForwardingMigrationUsesLiteralArguments(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "limactl")
	// One argument per line verifies yq syntax is not reinterpreted by a shell.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := &LimaClient{Binary: binary}
	if err := c.PrepareApplicationForwarding(context.Background(), Node{SandboxName: "example"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary + ".args")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(args) != 5 || args[0] != "edit" || args[1] != "--tty=false" || args[2] != "--set" || !strings.Contains(args[3], `"ignore":true`) || args[4] != "example" {
		t.Fatalf("arguments: %q", args)
	}
}
