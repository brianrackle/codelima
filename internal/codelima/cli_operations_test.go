package codelima

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
)

type cliProgressSandbox struct{ SandboxClient }

func (s cliProgressSandbox) Shell(_ context.Context, _ Node, _ guestIdentity, _ []string, _ string, _ bool, streams ShellStreams) error {
	if streams.Stdout != nil {
		_, err := fmt.Fprintln(streams.Stdout, "CLI-PROGRESS")
		return err
	}
	return nil
}

func testCLIOperationServer(t *testing.T, service *Service, version string) (string, *daemonHost) {
	t.Helper()
	home := newDaemonTestRoot(t, "cli-op-")
	host := newDaemonHost(service)
	server := daemon.NewServer(daemon.Config{Home: home, Version: version, Handler: host})
	host.broadcast = server.Broadcast
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = host.Close() })
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		_, err := daemonclient.Ping(context.Background(), home, version)
		return err == nil
	}, "CLI operation daemon")
	return home, host
}

func TestCLIOperationCreateResolvesCallerDirectoryAndRecordsReceipt(t *testing.T) {
	s, workspace := newTestService(t)
	s.sandbox = cliProgressSandbox{SandboxClient: s.sandbox}
	home, host := testCLIOperationServer(t, s, Version)
	var stdout, stderr bytes.Buffer
	frontend := s.withIO(&stdout, &stderr)
	configureCLIOperations(frontend, []string{"node", "create"})
	frontend.operationConnect = func(ctx context.Context) (*daemonclient.Client, error) {
		return daemonclient.Dial(ctx, daemonclient.Options{Home: home, Version: Version})
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatch(context.Background(), frontend, []string{"node", "create", "--slug", "cli-created", "--directory", relative})
	if err != nil {
		t.Fatal(err)
	}
	node := result.(Node)
	operations, err := host.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	page, err := operations.ListPage("", 1)
	if err != nil || len(page.History) != 1 {
		t.Fatal(page, err)
	}
	receipt, err := operations.Get(page.History[0].ID)
	if err != nil || receipt.State != "succeeded" || receipt.NodeID != node.ID || receipt.Request.Create.Directory != workspace || !filepath.IsAbs(receipt.Request.Create.Directory) {
		t.Fatal("caller directory or durable result was lost", receipt, err)
	}
	if stdout.Len() != 0 {
		t.Fatal("lifecycle progress polluted result stdout", stdout.String())
	}
	if _, err := frontend.NodeStart(context.Background(), node.ID); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "CLI-PROGRESS") {
		t.Fatal("CLI did not keep progress on stderr", stdout.String(), stderr.String())
	}
	_, err = frontend.NodeStart(context.Background(), "does-not-exist")
	if !IsNotFound(err) || exitCodeForError(err) != ExitNotFound {
		t.Fatal("RPC error lost CLI type/code", err)
	}
}

func TestDaemonStartupRecoveryPreservesResponsiveOperationCapableDaemon(t *testing.T) {
	s, _ := newTestService(t)
	home, _ := testCLIOperationServer(t, s, "prior-version")
	err := recoverDaemonBeforeStart(context.Background(), home)
	if err == nil || !strings.Contains(err.Error(), "daemon update") {
		t.Fatal("startup recovery should require explicit live update", err)
	}
	if _, err := daemonclient.Ping(context.Background(), home, "prior-version"); err != nil {
		t.Fatal("automatic recovery stopped the responsive daemon", err)
	}
}

func TestCLIOperationConnectionIsLazyAndOnlyForLifecycleCommands(t *testing.T) {
	for _, command := range []string{"create", "start", "retry", "stop", "clone", "delete"} {
		s, _ := newTestService(t)
		configureCLIOperations(s, []string{"node", command})
		if s.operationConnect == nil {
			t.Fatal("lifecycle command did not configure durable admission", command)
		}
		calls := 0
		s.operationConnect = func(context.Context) (*daemonclient.Client, error) {
			calls++
			return nil, errors.New("must not connect while showing help")
		}
		var stdout bytes.Buffer
		s.stdout = &stdout
		if _, err := dispatch(context.Background(), s, []string{"node", command, "--help"}); err != nil || calls != 0 || stdout.Len() == 0 {
			t.Fatal("help connected or stopped writing stdout", command, calls, err)
		}
	}
	for _, args := range [][]string{{"node", "list"}, {"node", "show", "n"}, {"daemon", "run"}, {"configuration", "create"}, nil} {
		s, _ := newTestService(t)
		configureCLIOperations(s, args)
		if s.operationConnect != nil {
			t.Fatal("read, daemon or metadata command configured a lifecycle client", args)
		}
	}
}

func TestCLIOperationRoutingLeavesSDKInlineAndPreservesConnectionErrors(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	if s.operationConnect != nil {
		t.Fatal("SDK service unexpectedly depends on a daemon")
	}
	configureCLIOperations(s, []string{"node", "start", node.ID})
	want := dependencyUnavailable("daemon disabled", nil, nil)
	s.operationConnect = func(context.Context) (*daemonclient.Client, error) { return nil, want }
	_, err := dispatch(context.Background(), s, []string{"node", "start", node.ID})
	if !errors.Is(err, want) || exitCodeForError(err) != ExitDependencyUnavailable {
		t.Fatal("CLI bypassed admission or lost its typed dependency error", err)
	}
	for _, call := range s.sandbox.(*fakeSandbox).calls {
		if call == "start "+node.SandboxName {
			t.Fatal("CLI fell back to inline runtime work")
		}
	}
}

func TestCLIOperationInspectAndCancelAfterCallerExit(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	s.sandbox = outputOperationSandbox{SandboxClient: s.sandbox}
	home, host := testCLIOperationServer(t, s, Version)
	configureCLIOperations(s, []string{"node", "start"})
	s.operationConnect = func(ctx context.Context) (*daemonclient.Client, error) {
		return daemonclient.Dial(ctx, daemonclient.Options{Home: home, Version: Version})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.NodeStart(ctx, node.ID); done <- err }()
	operations, err := host.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	var page NodeOperationsPage
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		page, err = operations.ListPage("", 1)
		return err == nil && len(page.Running) == 1
	}, "CLI admitted start")
	id := page.Running[0].ID
	cancel()
	err = <-done
	var appErr *AppError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &appErr) || appErr.Fields["operation_id"] != id || !strings.Contains(err.Error(), "operation show "+id) {
		t.Fatal("detached caller lost its receipt or inspection guidance", err)
	}
	result, err := dispatch(context.Background(), s, []string{"operation"})
	if err != nil || len(result.(NodeOperationsPage).Running) != 1 {
		t.Fatal("default listing cannot observe surviving work", result, err)
	}
	result, err = dispatch(context.Background(), s, []string{"operation", "show", id})
	if err != nil || result.(NodeOperation).ID != id || result.(NodeOperation).State != "running" {
		t.Fatal("show cannot inspect surviving work", result, err)
	}
	if _, err := dispatch(context.Background(), s, []string{"operation", "cancel", id}); err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		record, err := operations.Get(id)
		return err == nil && record.State == "canceled"
	}, "CLI explicit cancellation finished")
	if _, err := dispatch(context.Background(), s, []string{"operation", "show", newID()}); !IsNotFound(err) {
		t.Fatal("show lost typed missing-receipt error", err)
	}
	for _, args := range [][]string{{"operation", "list", "--limit", "0"}, {"operation", "list", "--limit", "101"}, {"operation", "show"}, {"operation", "cancel"}} {
		if _, err := dispatch(context.Background(), s, args); exitCodeForError(err) != ExitInvalidArgument {
			t.Fatal("invalid operation arguments accepted", args, err)
		}
	}
}
