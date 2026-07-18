package codelima

import (
	"errors"
	"testing"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

func TestResolveSDKNodeConfigTranslatesMountPortsAndPolicy(t *testing.T) {
	project := Project{WorkspacePath: "/host/work", DefaultImage: "image:12"}
	node := Node{
		SandboxName:        "demo-node",
		Image:              "image:12",
		VCPUs:              6,
		MemoryMiB:          8192,
		DiskMiB:            30720,
		Ports:              []string{"3000:3000", "8080:80"},
		NetPolicy:          &NetPolicy{Default: "deny", Allow: []string{"api.example.com", "public"}},
		WorkspaceMode:      WorkspaceModeMounted,
		WorkspaceMountPath: "/host/work",
		GuestWorkspacePath: "/workspace",
	}
	config, err := resolveSDKNodeConfig(project, node)
	if err != nil {
		t.Fatalf("resolveSDKNodeConfig() error = %v", err)
	}
	if config.name != "demo-node" || config.image != "image:12" {
		t.Fatalf("resolveSDKNodeConfig() identity = %#v", config)
	}
	if config.vcpus != 6 || config.memory != 8192 || config.disk != 30720 {
		t.Fatalf("resolveSDKNodeConfig() resources = %#v", config)
	}
	if got := config.mounts["/workspace"].Bind; got != "/host/work" {
		t.Fatalf("mount bind = %q", got)
	}
	if config.ports[3000] != 3000 || config.ports[8080] != 80 || len(config.ports) != 2 {
		t.Fatalf("ports = %#v", config.ports)
	}
	if config.network == nil || config.network.DefaultEgress != microsandbox.PolicyActionDeny {
		t.Fatalf("network = %#v", config.network)
	}
	if len(config.network.Rules) != 2 || config.network.Rules[0].Destination != "api.example.com" || config.network.Rules[1].Destination != "public" {
		t.Fatalf("network rules = %#v", config.network.Rules)
	}
}

func TestRuntimeValidation(t *testing.T) {
	t.Parallel()
	if _, err := validatePorts([]string{"3000:3000", "3000:8080"}); err == nil {
		t.Fatal("expected duplicate host port error")
	}
	if _, err := validatePorts([]string{"bad"}); err == nil {
		t.Fatal("expected malformed port error")
	}
	if err := validateSandboxName("bad name"); err == nil {
		t.Fatal("expected invalid sandbox name error")
	}
	if _, err := sdkNetworkConfig(&NetPolicy{Default: "maybe"}); err == nil {
		t.Fatal("expected invalid net policy error")
	}
}

func TestMapSDKErrorCategories(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category string
	}{
		{name: "not found", err: &microsandbox.Error{Kind: microsandbox.ErrSandboxNotFound}, category: "NotFound"},
		{name: "already exists", err: &microsandbox.Error{Kind: microsandbox.ErrSandboxAlreadyExists}, category: "PreconditionFailed"},
		{name: "invalid", err: &microsandbox.Error{Kind: microsandbox.ErrInvalidArgument}, category: "InvalidArgument"},
		{name: "library", err: &microsandbox.Error{Kind: microsandbox.ErrLibraryNotLoaded}, category: "DependencyUnavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := mapSDKError("test", test.err, nil)
			var appErr *AppError
			if !errors.As(err, &appErr) || appErr.Category != test.category {
				t.Fatalf("mapSDKError() = %#v, want %s", err, test.category)
			}
		})
	}
}

func TestCloneSnapshotNameIsBounded(t *testing.T) {
	name := cloneSnapshotName("abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz")
	if len(name) != 128 {
		t.Fatalf("cloneSnapshotName() length = %d", len(name))
	}
}
