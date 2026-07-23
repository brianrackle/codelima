package codelima

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
	"github.com/creack/pty"
	"gopkg.in/yaml.v3"
)

func TestRunInteractiveCommandPTYHelper(t *testing.T) {
	if os.Getenv("CODELIMA_INTERACTIVE_PTY_HELPER") != "1" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := runInteractiveCommand(ctx, `IFS= read -r value && printf 'received:%s\n' "$value"`, ShellStreams{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	cancel()
	if err != nil {
		t.Fatalf("interactive helper: %v", err)
	}
}

func TestRunInteractiveCommandKeepsPTYInForeground(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestRunInteractiveCommandPTYHelper$")
	command.Env = append(os.Environ(), "CODELIMA_INTERACTIVE_PTY_HELPER=1")
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = terminal.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})

	if _, err := io.WriteString(terminal, "foreground-input\n"); err != nil {
		t.Fatal(err)
	}

	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, _ := io.ReadAll(terminal)
		done <- result{output: output, err: command.Wait()}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("interactive helper failed: %v\noutput:\n%s", got.err, got.output)
		}
		if !bytes.Contains(got.output, []byte("received:foreground-input")) {
			t.Fatalf("interactive child did not read from its controlling PTY:\n%s", got.output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interactive child did not receive foreground PTY input")
	}
}

func TestDefaultServiceUsesLimaClient(t *testing.T) {
	t.Parallel()
	service := NewService(DefaultConfig(t.TempDir()), nil, strings.NewReader(""), io.Discard, io.Discard)
	client, ok := service.sandbox.(*LimaClient)
	if !ok {
		t.Fatalf("NewService() sandbox = %T, want *LimaClient", service.sandbox)
	}
	if client.Binary != "limactl" || client.MetadataRoot != service.cfg.MetadataRoot {
		t.Fatalf("default Lima client = %#v", client)
	}
}

func TestLimaClientNestedVirtualizationSupportIsHostAware(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		goos      string
		goarch    string
		available bool
		want      bool
	}{
		{name: "supported darwin arm64", goos: "darwin", goarch: "arm64", available: true, want: true},
		{name: "unsupported darwin arm64", goos: "darwin", goarch: "arm64", available: false, want: false},
		{name: "linux arm64", goos: "linux", goarch: "arm64", available: true, want: false},
		{name: "darwin amd64", goos: "darwin", goarch: "amd64", available: true, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := NewLimaClient(t.TempDir())
			client.GOOS = test.goos
			client.GOARCH = test.goarch
			client.nestedVirtualizationProbe = func() bool { return test.available }
			if got := client.supportsNestedVirtualization(); got != test.want {
				t.Fatalf("supportsNestedVirtualization() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestParseLimaVersionAndSupportedRange(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		output string
		want   string
	}{
		{output: "limactl version 2.1.1\n", want: "2.1.1"},
		{output: "limactl version v2.8.0\n", want: "2.8.0"},
	} {
		got, err := parseLimaVersion(test.output)
		if err != nil {
			t.Fatalf("parseLimaVersion(%q) error = %v", test.output, err)
		}
		if got != test.want {
			t.Fatalf("parseLimaVersion(%q) = %q, want %q", test.output, got, test.want)
		}
	}

	for _, unsupported := range []string{"", "limactl dev", "2.0.9", "3.0.0"} {
		version, err := parseLimaVersion(unsupported)
		if err == nil {
			err = validateLimaVersion(version)
		}
		if err == nil {
			t.Fatalf("version %q unexpectedly supported", unsupported)
		}
	}
	if err := validateLimaVersion("2.1.0"); err != nil {
		t.Fatalf("validateLimaVersion(2.1.0) error = %v", err)
	}
	if err := validateLimaVersion("2.9.4"); err != nil {
		t.Fatalf("validateLimaVersion(2.9.4) error = %v", err)
	}
}

func TestValidateLimaHomeAcceptsLocalSocketFilesystem(t *testing.T) {
	t.Parallel()
	client := NewLimaClient(t.TempDir())
	client.GOOS = "test"
	client.LimaHome = filepath.Join(t.TempDir(), "lima")
	client.UnixSocketProbe = func(string) error { return nil }
	if err := client.validateLimaHome("demo"); err != nil {
		t.Fatalf("validateLimaHome() error = %v", err)
	}
	entries, err := os.ReadDir(client.LimaHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("socket probe artifacts = %#v", entries)
	}
}

func TestValidateLimaHomeRejectsInstanceSocketPathOverflow(t *testing.T) {
	t.Parallel()
	client := NewLimaClient(t.TempDir())
	client.GOOS = "linux"
	client.LimaHome = filepath.Join(t.TempDir(), strings.Repeat("x", 90))
	err := client.validateLimaHome("instance")
	if err == nil || !strings.Contains(err.Error(), "Unix-socket path limit") {
		t.Fatalf("validateLimaHome() error = %v", err)
	}
	if exists(client.LimaHome) {
		t.Fatal("path-overflow validation mutated LIMA_HOME")
	}
}

func TestValidateLimaHomeRejectsUnsupportedSocketFilesystem(t *testing.T) {
	t.Parallel()
	client := NewLimaClient(t.TempDir())
	client.GOOS = "test"
	client.LimaHome = filepath.Join(t.TempDir(), "lima")
	client.UnixSocketProbe = func(string) error { return errors.New("operation not supported") }
	err := client.validateLimaHome("demo")
	if err == nil || !strings.Contains(err.Error(), "filesystem must support Unix sockets") {
		t.Fatalf("validateLimaHome() error = %v", err)
	}
}

func TestParseLimaListJSONLines(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`{"name":"alpha","status":"Running","dir":"/safe/.lima/alpha","hostname":"lima-alpha","sshConfigFile":"/safe/.lima/alpha/ssh.config","sshAddress":"127.0.0.1","sshLocalPort":60022,"limaVersion":"2.1.1","LimaHome":"/safe/.lima","extra":true}`,
		`{"name":"beta","status":"Stopped","dir":"/safe/.lima/beta"}`,
	}, "\n")
	got, err := parseLimaList([]byte(input))
	if err != nil {
		t.Fatalf("parseLimaList() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[0].Status != ObservationRunning || got[1].Status != ObservationStopped {
		t.Fatalf("parseLimaList() = %#v", got)
	}
	if got[0].SSHConfigFile != "/safe/.lima/alpha/ssh.config" || got[0].LimaHome != "/safe/.lima" || got[0].SSHPort != 60022 {
		t.Fatalf("parseLimaList() SSH metadata = %#v", got[0])
	}

	for _, malformed := range []string{
		`{"status":"Running"}`,
		`{"name":"bad","status":"Unexpected"}`,
		`{"name":`,
	} {
		if _, err := parseLimaList([]byte(malformed)); err == nil {
			t.Fatalf("parseLimaList(%q) unexpectedly succeeded", malformed)
		}
	}
}

func TestRenderLimaTemplateAppliesRuntimeInvariants(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	node := Node{
		ID: "node-id", SandboxName: "demo", Image: "template:ubuntu",
		VCPUs: 6, MemoryMiB: 6144, DiskMiB: 32768,
		WorkspaceMode: WorkspaceModeMounted, WorkspaceMountPath: workspace, GuestWorkspacePath: "/workspace",
		Ports: []string{"8080:80", "3000:3000"},
	}
	base := []byte(`minimumLimaVersion: 2.0.0
images:
  - location: https://example.invalid/ubuntu.img
    arch: aarch64
mounts:
  - location: /Users/example
containerd:
  system: true
  user: true
ssh:
  overVsock: true
video:
  display: default
audio:
  device: default
`)
	data, err := renderLimaTemplate(base, node, "darwin", "arm64", true)
	if err != nil {
		t.Fatalf("renderLimaTemplate() error = %v", err)
	}
	var rendered map[string]any
	if err := yaml.Unmarshal(data, &rendered); err != nil {
		t.Fatalf("rendered YAML error = %v\n%s", err, data)
	}
	if rendered["cpus"] != 6 || rendered["memory"] != "6144MiB" || rendered["disk"] != "32768MiB" {
		t.Fatalf("resources = %#v", rendered)
	}
	if rendered["vmType"] != "vz" || rendered["mountType"] != "virtiofs" || rendered["arch"] != "aarch64" {
		t.Fatalf("platform selection = %#v", rendered)
	}
	if rendered["nestedVirtualization"] != true {
		t.Fatalf("nested virtualization = %#v, want true", rendered["nestedVirtualization"])
	}
	mounts, ok := rendered["mounts"].([]any)
	if !ok || len(mounts) != 1 {
		t.Fatalf("mounts = %#v", rendered["mounts"])
	}
	mount := mounts[0].(map[string]any)
	if mount["location"] != resolvedWorkspace || mount["mountPoint"] != "/workspace" || mount["writable"] != true {
		t.Fatalf("mount = %#v", mount)
	}
	containerd := rendered["containerd"].(map[string]any)
	if containerd["system"] != false || containerd["user"] != false {
		t.Fatalf("containerd = %#v", containerd)
	}
	sshSettings := rendered["ssh"].(map[string]any)
	if sshSettings["overVsock"] != false {
		t.Fatalf("ssh = %#v", sshSettings)
	}
	portForwards := rendered["portForwards"].([]any)
	if len(portForwards) != 3 {
		t.Fatalf("portForwards = %#v", portForwards)
	}
	first := portForwards[0].(map[string]any)
	last := portForwards[2].(map[string]any)
	if first["hostPort"] != 8080 || first["guestPort"] != 80 || first["static"] != true {
		t.Fatalf("first port forward = %#v", first)
	}
	if last["ignore"] != true || last["proto"] != "any" {
		t.Fatalf("ignore-all rule = %#v", last)
	}
}

func TestRenderLimaTemplateCopyModeHasNoMounts(t *testing.T) {
	t.Parallel()
	node := Node{ID: "n", SandboxName: "copy", Image: "template:ubuntu", VCPUs: 2, MemoryMiB: 4096, DiskMiB: 20480, WorkspaceMode: WorkspaceModeCopy}
	data, err := renderLimaTemplate([]byte("images: [{location: https://example.invalid/image}]\nmounts: [{location: /host}]\n"), node, "linux", "amd64", true)
	if err != nil {
		t.Fatalf("renderLimaTemplate() error = %v", err)
	}
	var rendered map[string]any
	if err := yaml.Unmarshal(data, &rendered); err != nil {
		t.Fatal(err)
	}
	if mounts := rendered["mounts"].([]any); len(mounts) != 0 {
		t.Fatalf("copy-mode mounts = %#v", mounts)
	}
	if rendered["vmType"] != "qemu" {
		t.Fatalf("linux vmType = %#v", rendered["vmType"])
	}
	if rendered["nestedVirtualization"] != false {
		t.Fatalf("linux nested virtualization = %#v, want false", rendered["nestedVirtualization"])
	}
}

func TestParseLimaSSHConfigRestrictsPaths(t *testing.T) {
	t.Parallel()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instanceDir := filepath.Join(home, "demo")
	if err := os.MkdirAll(instanceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(home, "_config", "user")
	if err := os.MkdirAll(filepath.Dir(identity), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(instanceDir, "ssh.config")
	config := "Host lima-demo\n  IdentityFile \"" + identity + "\"\n  User lima\n  Hostname 127.0.0.1\n  Port 60022\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := parseLimaSSHConfig(configPath, home)
	if err != nil {
		t.Fatalf("parseLimaSSHConfig() error = %v", err)
	}
	if got.User != "lima" || got.Host != "127.0.0.1" || got.Port != 60022 || got.IdentityFile != identity {
		t.Fatalf("parseLimaSSHConfig() = %#v", got)
	}

	outside := filepath.Join(t.TempDir(), "ssh.config")
	if err := os.WriteFile(outside, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseLimaSSHConfig(outside, home); err == nil {
		t.Fatal("outside SSH config unexpectedly accepted")
	}
}

func TestLimaCommandErrorMapping(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		stderr   string
		category ErrorCategory
	}{
		{stderr: "instance demo does not exist", category: CategoryNotFound},
		{stderr: "instance demo already exists", category: CategoryPreconditionFailed},
		{stderr: "invalid template", category: CategoryInvalidArgument},
	} {
		err := mapLimaCommandError("test", errors.New("exit status 1"), []byte(test.stderr), map[string]any{"sandbox_name": "demo"})
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Category != test.category {
			t.Fatalf("mapLimaCommandError(%q) = %#v", test.stderr, err)
		}
	}
}

func TestWriteRenderedLimaTemplateIsPrivate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "node", "instance.lima.yaml")
	if err := writeRenderedLimaTemplate(path, []byte("cpus: 2\n")); err != nil {
		t.Fatalf("writeRenderedLimaTemplate() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("template mode = %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, []byte("cpus: 2\n")) {
		t.Fatalf("template data = %q", data)
	}
}

func TestLimaClientLifecycleWithFakeLimactl(t *testing.T) {
	t.Parallel()
	home := testutil.TempDir(t, "lima-")
	binary := writeFakeLimactl(t, home)
	client := NewLimaClient(home)
	client.Binary = binary
	client.LimaHome = filepath.Join(home, "lima")
	client.UnixSocketProbe = func(string) error { return nil }
	client.RuntimeCommands = defaultRuntimeCommandTemplates()
	var stdout bytes.Buffer
	client.Stdout = &stdout
	node := Node{
		ID: "node-id", SandboxName: "demo", Image: "template:ubuntu", VCPUs: 2, MemoryMiB: 4096, DiskMiB: 20480,
		WorkspaceMode: WorkspaceModeCopy, GuestWorkspacePath: "/workspace",
	}
	ctx := context.Background()
	if version, err := client.Version(ctx); err != nil || version != requiredLimaVersion {
		t.Fatalf("Version() = %q, %v", version, err)
	}
	if err := client.Create(ctx, node); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	templatePath := filepath.Join(home, "nodes", node.ID, "instance.lima.yaml")
	if info, err := os.Stat(templatePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("rendered template = %v, %v", info, err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("internal limactl output leaked to application stdout: %q", stdout.String())
	}
	if err := client.Start(ctx, node); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	var shellOutput bytes.Buffer
	if err := client.Shell(ctx, node, []string{"printf", "hello"}, "/workspace", false, ShellStreams{Stdout: &shellOutput}); err != nil {
		t.Fatalf("Shell() error = %v", err)
	}
	if shellOutput.String() != "shell-ok\n" {
		t.Fatalf("Shell() output = %q", shellOutput.String())
	}
	if err := client.CopyToGuest(ctx, node, home, "/workspace", true); err != nil {
		t.Fatalf("CopyToGuest() error = %v", err)
	}
	if err := client.Stop(ctx, node); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	clone := node
	clone.ID = "clone-id"
	clone.SandboxName = "clone"
	if err := client.Clone(ctx, node, clone); err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	if err := client.Delete(ctx, clone); err != nil {
		t.Fatalf("Delete(clone) error = %v", err)
	}
	if err := client.Delete(ctx, node); err != nil {
		t.Fatalf("Delete(node) error = %v", err)
	}
	observations, err := client.List(ctx)
	if err != nil || len(observations) != 0 {
		t.Fatalf("List() after delete = %#v, %v", observations, err)
	}
	logData, err := os.ReadFile(binary + ".log")
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	for _, command := range []string{"template copy", "validate", "create", "start", "shell", "copy", "stop", "clone", "delete"} {
		if !strings.Contains(logText, command) {
			t.Fatalf("fake limactl log lacks %q:\n%s", command, logText)
		}
	}
	if !strings.Contains(logText, "sudo -H -- printf hello") {
		t.Fatalf("guest command did not preserve root execution:\n%s", logText)
	}
	if strings.Contains(logText, "--nested-virt") {
		t.Fatalf("unsupported host enabled nested virtualization:\n%s", logText)
	}
}

func TestLimaClientAutomaticallyEnablesNestedVirtualization(t *testing.T) {
	t.Parallel()
	home := testutil.TempDir(t, "n-")
	binary := writeFakeLimactl(t, home)
	client := NewLimaClient(home)
	client.Binary = binary
	client.GOOS = "darwin"
	client.GOARCH = "arm64"
	client.LimaHome = filepath.Join(filepath.Dir(filepath.Dir(home)), filepath.Base(home))
	t.Cleanup(func() {
		if err := os.RemoveAll(client.LimaHome); err != nil {
			t.Errorf("remove short Lima home: %v", err)
		}
	})
	client.UnixSocketProbe = func(string) error { return nil }
	client.nestedVirtualizationProbe = func() bool { return true }
	node := Node{
		ID: "node-id", SandboxName: "nested", Image: "template:ubuntu",
		VCPUs: 2, MemoryMiB: 4096, DiskMiB: 20480, WorkspaceMode: WorkspaceModeCopy,
	}
	ctx := context.Background()
	if err := client.Create(ctx, node); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	data, err := os.ReadFile(client.templatePath(node))
	if err != nil {
		t.Fatal(err)
	}
	var rendered map[string]any
	if err := yaml.Unmarshal(data, &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered["nestedVirtualization"] != true {
		t.Fatalf("nested virtualization = %#v, want true", rendered["nestedVirtualization"])
	}
	if err := client.Start(ctx, node); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	logData, err := os.ReadFile(binary + ".log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "start -y nested --nested-virt") {
		t.Fatalf("supported host start omitted --nested-virt:\n%s", logData)
	}
}

func TestDoctorReportsAutomaticNestedVirtualization(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		available bool
		want      string
	}{
		{name: "supported", available: true, want: "nested virtualization is enabled automatically"},
		{name: "unsupported", available: false, want: "nested virtualization is unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := testutil.TempDir(t, "doctor-")
			home := filepath.Join(root, "home")
			binary := writeFakeLimactl(t, root)
			client := NewLimaClient(home)
			client.Binary = binary
			client.GOOS = "darwin"
			client.GOARCH = "arm64"
			client.LimaHome = filepath.Join(home, "lima")
			client.nestedVirtualizationProbe = func() bool { return test.available }

			cfg := DefaultConfig(home)
			service := NewService(cfg, client, strings.NewReader(""), io.Discard, io.Discard)
			report, err := service.Doctor(context.Background(), false)
			if err != nil {
				t.Fatalf("Doctor() error = %v", err)
			}
			for _, check := range report.Checks {
				if check.Name == "lima_driver" {
					if !strings.Contains(check.Message, test.want) {
						t.Fatalf("lima_driver message = %q, want %q", check.Message, test.want)
					}
					return
				}
			}
			t.Fatal("Doctor() omitted lima_driver check")
		})
	}
}

func TestLimaCreateDoesNotDeletePreExistingInstance(t *testing.T) {
	t.Parallel()
	home := testutil.TempDir(t, "lima-")
	binary := writeFakeLimactl(t, home)
	if err := os.WriteFile(binary+".state", []byte("demo Stopped\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewLimaClient(home)
	client.Binary = binary
	client.LimaHome = filepath.Join(home, "lima")
	client.UnixSocketProbe = func(string) error { return nil }
	node := Node{
		ID: "node-id", SandboxName: "demo", Image: "template:ubuntu",
		VCPUs: 2, MemoryMiB: 4096, DiskMiB: 20480, WorkspaceMode: WorkspaceModeCopy,
	}
	err := client.Create(context.Background(), node)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Create() error = %v", err)
	}
	logData, err := os.ReadFile(binary + ".log")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "create ") || strings.Contains(string(logData), "delete ") {
		t.Fatalf("pre-existing instance was mutated:\n%s", logData)
	}
}

func TestLimaCloneDoesNotDeletePreExistingTarget(t *testing.T) {
	t.Parallel()
	home := testutil.TempDir(t, "lima-")
	binary := writeFakeLimactl(t, home)
	if err := os.WriteFile(binary+".state", []byte("source Stopped\ntarget Stopped\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewLimaClient(home)
	client.Binary = binary
	client.LimaHome = filepath.Join(home, "lima")
	client.UnixSocketProbe = func(string) error { return nil }
	source := Node{SandboxName: "source"}
	target := Node{SandboxName: "target"}
	err := client.Clone(context.Background(), source, target)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Clone() error = %v", err)
	}
	logData, err := os.ReadFile(binary + ".log")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "clone ") || strings.Contains(string(logData), "delete ") {
		t.Fatalf("pre-existing target was mutated:\n%s", logData)
	}
}

func TestLimaObservationCacheUsesWatchEvents(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	binary := writeFakeLimactl(t, home)
	if err := os.WriteFile(binary+".state", []byte("demo Stopped\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewLimaClient(home)
	client.Binary = binary
	client.RuntimeCommands = defaultRuntimeCommandTemplates()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.StartObservation(ctx); err != nil {
		t.Fatalf("StartObservation() error = %v", err)
	}
	t.Cleanup(func() { _ = client.StopObservation() })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		observations, err := client.List(context.Background())
		if err == nil {
			if observation, ok := findObservation(observations, "demo"); ok && observation.Status == ObservationRunning {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	observations, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	observation, ok := findObservation(observations, "demo")
	if !ok || observation.Status != ObservationRunning {
		t.Fatalf("watched observation = %#v", observations)
	}
	logData, err := os.ReadFile(binary + ".log")
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(logData), "list --json"); count != 1 {
		t.Fatalf("limactl list calls = %d, want one startup reconciliation; log:\n%s", count, logData)
	}
	if err := client.StopObservation(); err != nil {
		t.Fatalf("StopObservation() error = %v", err)
	}
}

func TestNativeLimaTemplateValidation(t *testing.T) {
	if os.Getenv("CODELIMA_NATIVE_LIMA") != "1" {
		t.Skip("set CODELIMA_NATIVE_LIMA=1 through make test-lima-native")
	}
	binary, err := exec.LookPath("limactl")
	if err != nil {
		t.Fatalf("limactl is unavailable: %v", err)
	}
	client := NewLimaClient(t.TempDir())
	client.Binary = binary
	template, err := client.resolveTemplate(context.Background(), "template:ubuntu")
	if err != nil {
		t.Fatalf("resolve native template: %v", err)
	}
	workspace := t.TempDir()
	node := Node{
		ID: "native-validation", SandboxName: "native-validation", Image: "template:ubuntu",
		VCPUs: 2, MemoryMiB: 4096, DiskMiB: 20480,
		WorkspaceMode: WorkspaceModeMounted, WorkspaceMountPath: workspace, GuestWorkspacePath: "/workspace",
		Ports: []string{"18080:8080"},
	}
	data, err := renderLimaTemplate(template, node, runtime.GOOS, runtime.GOARCH, false)
	if err != nil {
		t.Fatalf("render native template: %v", err)
	}
	path := client.templatePath(node)
	if err := writeRenderedLimaTemplate(path, data); err != nil {
		t.Fatalf("write native template: %v", err)
	}
	if _, err := client.runDirect(context.Background(), time.Minute, "validate native Lima template", nil, binary, "validate", path); err != nil {
		t.Fatalf("native limactl validate: %v\n%s", err, data)
	}
}

func writeFakeLimactl(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "limactl-fake")
	const script = `#!/bin/sh
set -eu
state="$0.state"
log="$0.log"
printf '%s\n' "$*" >> "$log"
cmd="${1:-}"
if [ "$cmd" = "--version" ]; then
  printf 'limactl version 2.1.0\n'
  exit 0
fi
shift || true
case "$cmd" in
  template)
    printf '%s\n' 'minimumLimaVersion: 2.0.0' 'images:' '  - location: https://example.invalid/ubuntu.img' '    arch: aarch64' 'mounts: []'
    ;;
  validate)
    test -f "$1"
    ;;
  list)
    if [ -f "$state" ]; then
      while read -r name status; do
        [ -n "$name" ] || continue
        printf '{"name":"%s","status":"%s","dir":"%s/%s","sshConfigFile":"%s/%s/ssh.config","LimaHome":"%s","limaVersion":"2.1.0"}\n' "$name" "$status" "$0.home" "$name" "$0.home" "$name" "$0.home"
      done < "$state"
    fi
    ;;
  create)
    shift
    test "$1" = "--name"
    printf '%s Stopped\n' "$2" >> "$state"
    ;;
  start|stop)
    shift
    name="$1"
    status=Running
    [ "$cmd" = stop ] && status=Stopped
    awk -v name="$name" -v status="$status" '{ if ($1 == name) print $1, status; else print $0 }' "$state" > "$state.next"
    mv "$state.next" "$state"
    ;;
  delete)
    shift
    name="$1"
    awk -v name="$name" '$1 != name { print $0 }' "$state" > "$state.next"
    mv "$state.next" "$state"
    ;;
  clone)
    shift
    source="$1"
    target="$2"
    grep -q "^$source " "$state"
    printf '%s Stopped\n' "$target" >> "$state"
    ;;
  copy)
    ;;
  shell)
    printf 'shell-ok\n'
    ;;
  watch)
    printf '%s\n' '{"instance":"demo","event":{"time":"2026-07-20T12:00:00Z","status":{"running":true}}}'
    while :; do sleep 60; done
    ;;
  *)
    printf 'invalid fake command: %s\n' "$cmd" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
