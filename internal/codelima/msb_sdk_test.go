package codelima

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

type fakeSDKRuntime struct {
	sdkVersion     string
	runtimeVersion string
	versionErr     error
	installErr     error
	handles        map[string]*fakeSDKHandle
	list           []sdkSandboxHandle
	createSandbox  *fakeSDKSandbox
	startSandbox   *fakeSDKSandbox
	createdName    string
	createdConfig  microsandbox.SandboxConfig
	calls          []string
}

func newFakeSDKRuntime() *fakeSDKRuntime {
	return &fakeSDKRuntime{sdkVersion: requiredMicrosandboxVersion, runtimeVersion: requiredMicrosandboxVersion, handles: map[string]*fakeSDKHandle{}}
}

func (r *fakeSDKRuntime) Versions() (string, string, error) {
	return r.sdkVersion, r.runtimeVersion, r.versionErr
}
func (r *fakeSDKRuntime) EnsureInstalled(context.Context) error {
	r.calls = append(r.calls, "ensure-installed")
	return r.installErr
}
func (r *fakeSDKRuntime) List(context.Context) ([]sdkSandboxHandle, error) { return r.list, nil }
func (r *fakeSDKRuntime) Create(_ context.Context, name string, options ...microsandbox.SandboxOption) (sdkSandbox, error) {
	r.calls = append(r.calls, "create:"+name)
	r.createdName = name
	r.createdConfig = microsandbox.SandboxConfig{}
	for _, option := range options {
		option(&r.createdConfig)
	}
	return r.createSandbox, nil
}
func (r *fakeSDKRuntime) StartDetached(_ context.Context, name string) (sdkSandbox, error) {
	r.calls = append(r.calls, "start:"+name)
	return r.startSandbox, nil
}
func (r *fakeSDKRuntime) Get(_ context.Context, name string) (sdkSandboxHandle, error) {
	r.calls = append(r.calls, "get:"+name)
	handle, ok := r.handles[name]
	if !ok {
		return nil, &microsandbox.Error{Kind: microsandbox.ErrSandboxNotFound}
	}
	return handle, nil
}
func (r *fakeSDKRuntime) RemoveSnapshot(_ context.Context, name string) error {
	r.calls = append(r.calls, "remove-snapshot:"+name)
	return nil
}

type fakeSDKHandle struct {
	name    string
	status  microsandbox.SandboxStatus
	sandbox *fakeSDKSandbox
	calls   *[]string
}

func (h *fakeSDKHandle) record(call string) {
	if h.calls != nil {
		*h.calls = append(*h.calls, call)
	}
}
func (h *fakeSDKHandle) Name() string                       { return h.name }
func (h *fakeSDKHandle) Status() microsandbox.SandboxStatus { return h.status }
func (h *fakeSDKHandle) Connect(context.Context) (sdkSandbox, error) {
	h.record("connect:" + h.name)
	return h.sandbox, nil
}
func (h *fakeSDKHandle) Stop(context.Context, ...microsandbox.StopOption) error {
	h.record("stop-handle:" + h.name)
	return nil
}
func (h *fakeSDKHandle) Kill(context.Context, ...microsandbox.KillOption) error {
	h.record("kill-handle:" + h.name)
	return nil
}
func (h *fakeSDKHandle) Remove(context.Context) error {
	h.record("remove:" + h.name)
	return nil
}
func (h *fakeSDKHandle) Snapshot(_ context.Context, name string) error {
	h.record("snapshot:" + h.name + ":" + name)
	return nil
}

type fakeSDKSandbox struct {
	calls         []string
	copySource    string
	copyTarget    string
	attachCommand string
	execHandle    sdkExecHandle
	execConfig    microsandbox.ExecConfig
	sshServer     sdkSSHServer
	sshKeyPath    string
}

func (s *fakeSDKSandbox) Stop(context.Context, ...microsandbox.StopOption) error {
	s.calls = append(s.calls, "stop")
	return nil
}
func (s *fakeSDKSandbox) Kill(context.Context, ...microsandbox.KillOption) error {
	s.calls = append(s.calls, "kill")
	return nil
}
func (s *fakeSDKSandbox) Close() error { s.calls = append(s.calls, "close"); return nil }
func (s *fakeSDKSandbox) Detach(context.Context) error {
	s.calls = append(s.calls, "detach")
	return nil
}
func (s *fakeSDKSandbox) CopyFromHost(_ context.Context, source, target string) error {
	s.calls = append(s.calls, "copy")
	s.copySource, s.copyTarget = source, target
	return nil
}
func (s *fakeSDKSandbox) Mkdir(_ context.Context, path string) error {
	s.calls = append(s.calls, "mkdir:"+path)
	return nil
}
func (s *fakeSDKSandbox) Attach(_ context.Context, command string, args ...string) (int, error) {
	s.calls = append(s.calls, "attach")
	s.attachCommand = strings.Join(append([]string{command}, args...), " ")
	return 0, nil
}
func (s *fakeSDKSandbox) ExecStream(_ context.Context, command string, args []string, options ...microsandbox.ExecOption) (sdkExecHandle, error) {
	s.calls = append(s.calls, "exec:"+strings.Join(append([]string{command}, args...), " "))
	s.execConfig = microsandbox.ExecConfig{}
	for _, option := range options {
		option(&s.execConfig)
	}
	return s.execHandle, nil
}
func (s *fakeSDKSandbox) PrepareSSHServer(_ context.Context, path string) (sdkSSHServer, error) {
	s.calls = append(s.calls, "prepare-ssh")
	s.sshKeyPath = path
	return s.sshServer, nil
}

type fakeSDKExecHandle struct {
	events []*microsandbox.ExecEvent
	index  int
	sink   io.WriteCloser
	calls  []string
}

func (h *fakeSDKExecHandle) TakeStdin() io.WriteCloser { return h.sink }
func (h *fakeSDKExecHandle) Recv(context.Context) (*microsandbox.ExecEvent, error) {
	if h.index >= len(h.events) {
		return nil, errors.New("unexpected end of fake events")
	}
	event := h.events[h.index]
	h.index++
	return event, nil
}
func (h *fakeSDKExecHandle) Kill(context.Context) error {
	h.calls = append(h.calls, "kill")
	return nil
}
func (h *fakeSDKExecHandle) Close() error { h.calls = append(h.calls, "close"); return nil }

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	closed chan struct{}
}

func newSynchronizedBuffer() *synchronizedBuffer {
	return &synchronizedBuffer{closed: make(chan struct{})}
}
func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}
func (b *synchronizedBuffer) Close() error { close(b.closed); return nil }
func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type fakeSDKSSHServer struct{ calls []string }

func (s *fakeSDKSSHServer) ServeConnection(context.Context) error {
	s.calls = append(s.calls, "serve")
	return nil
}
func (s *fakeSDKSSHServer) Close(context.Context) error {
	s.calls = append(s.calls, "close")
	return nil
}

func TestSDKSandboxClientTypedLifecycleAndOptions(t *testing.T) {
	runtime := newFakeSDKRuntime()
	runtime.list = []sdkSandboxHandle{&fakeSDKHandle{name: "listed", status: microsandbox.SandboxStatusRunning}}
	created := &fakeSDKSandbox{}
	started := &fakeSDKSandbox{}
	runtime.createSandbox = created
	runtime.startSandbox = started
	client := &SDKSandboxClient{RuntimeCommands: defaultRuntimeCommandTemplates(), runtime: runtime}
	node := Node{SandboxName: "demo", Image: "image:12", VCPUs: 4, MemoryMiB: 6144, DiskMiB: 32768, WorkspaceMode: WorkspaceModeMounted, WorkspaceMountPath: "/host/work", GuestWorkspacePath: "/workspace", Ports: []string{"8080:80"}, NetPolicy: &NetPolicy{Default: "deny"}}
	if err := client.Create(context.Background(), node); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	config := runtime.createdConfig
	if runtime.createdName != "demo" || config.Image != "image:12" || config.CPUs != 4 || config.MemoryMiB != 6144 || config.OCIUpperSizeMiB != 32768 || config.Shell != "/bin/bash" || !config.Detached || config.Init == nil {
		t.Fatalf("Create() config = %#v", config)
	}
	if config.Volumes["/workspace"].Bind != "/host/work" || config.Ports[8080] != 80 || config.Network.DefaultEgress != microsandbox.PolicyActionDeny {
		t.Fatalf("Create() typed mounts/ports/network = %#v", config)
	}
	if got := strings.Join(created.calls, ","); got != "stop,close" {
		t.Fatalf("Create() ownership calls = %q", got)
	}
	if err := client.Start(context.Background(), node); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := strings.Join(started.calls, ","); got != "detach" {
		t.Fatalf("Start() ownership calls = %q", got)
	}
	observations, err := client.List(context.Background())
	if err != nil || len(observations) != 1 || observations[0].Status != "running" {
		t.Fatalf("List() = %#v, %v", observations, err)
	}
}

func TestDefaultServiceUsesSDKAndRequiresMatchingRuntime(t *testing.T) {
	service := NewService(DefaultConfig(t.TempDir()), nil, strings.NewReader(""), io.Discard, io.Discard)
	if _, ok := service.sandbox.(*SDKSandboxClient); !ok {
		t.Fatalf("NewService() sandbox = %T, want *SDKSandboxClient", service.sandbox)
	}
	runtime := newFakeSDKRuntime()
	runtime.runtimeVersion = "0.6.5"
	client := &SDKSandboxClient{runtime: runtime}
	_, err := client.Version(context.Background())
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Category != "DependencyUnavailable" {
		t.Fatalf("Version(mismatch) error = %#v", err)
	}
}

func TestSDKSandboxClientEnsuresPinnedRuntimeBeforeVersionCheck(t *testing.T) {
	runtime := newFakeSDKRuntime()
	client := &SDKSandboxClient{runtime: runtime}
	if _, err := client.Version(context.Background()); err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if len(runtime.calls) != 1 || runtime.calls[0] != "ensure-installed" {
		t.Fatalf("Version() calls = %v", runtime.calls)
	}

	runtime.installErr = errors.New("download failed")
	if _, err := client.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "install microsandbox SDK runtime") {
		t.Fatalf("Version(install failure) error = %v", err)
	}
}

func TestSDKSandboxClientRejectsInteractiveAttachWithoutProcessTTY(t *testing.T) {
	client := &SDKSandboxClient{RuntimeCommands: defaultRuntimeCommandTemplates(), runtime: newFakeSDKRuntime()}
	err := client.Shell(context.Background(), Node{SandboxName: "demo"}, nil, "", true, ShellStreams{
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
	})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Category != "PreconditionFailed" {
		t.Fatalf("Shell(non-TTY interactive) error = %#v", err)
	}
}

func TestSDKSandboxClientCopyAttachAndStreamingExec(t *testing.T) {
	runtime := newFakeSDKRuntime()
	sandbox := &fakeSDKSandbox{}
	runtime.handles["demo"] = &fakeSDKHandle{name: "demo", status: microsandbox.SandboxStatusRunning, sandbox: sandbox}
	client := &SDKSandboxClient{RuntimeCommands: defaultRuntimeCommandTemplates(), runtime: runtime, attachReady: func(ShellStreams) error { return nil }}
	node := Node{SandboxName: "demo"}
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "value.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	sandbox.execHandle = &fakeSDKExecHandle{events: []*microsandbox.ExecEvent{
		{Kind: microsandbox.ExecEventExited, ExitCode: 0},
		{Kind: microsandbox.ExecEventDone},
	}}
	if err := client.CopyToGuest(context.Background(), node, source, "/guest/target", true); err != nil {
		t.Fatalf("CopyToGuest() error = %v", err)
	}
	if sandbox.copySource != filepath.Join(source, "nested", "value.txt") || sandbox.copyTarget != "/guest/target/nested/value.txt" {
		t.Fatalf("CopyToGuest() = %q -> %q", sandbox.copySource, sandbox.copyTarget)
	}
	if got := strings.Join(sandbox.calls, ","); !strings.Contains(got, "mkdir:/guest/target") || !strings.Contains(got, "mkdir:/guest/target/nested") || !strings.Contains(got, "exec:ln -s README.md /guest/target/link") {
		t.Fatalf("CopyToGuest() calls = %q", got)
	}
	if err := client.Shell(context.Background(), node, []string{"/bin/bash", "-l"}, "/workspace/it's-here", true, ShellStreams{}); err != nil {
		t.Fatalf("Shell(interactive) error = %v", err)
	}
	if !strings.Contains(sandbox.attachCommand, `cd -- '/workspace/it'"'"'s-here' && exec '/bin/bash' '-l'`) {
		t.Fatalf("Attach command = %q", sandbox.attachCommand)
	}

	sink := newSynchronizedBuffer()
	execHandle := &fakeSDKExecHandle{sink: sink, events: []*microsandbox.ExecEvent{
		{Kind: microsandbox.ExecEventStdout, Data: []byte("out")},
		{Kind: microsandbox.ExecEventStderr, Data: []byte("err")},
		{Kind: microsandbox.ExecEventExited, ExitCode: 0},
		{Kind: microsandbox.ExecEventDone},
	}}
	sandbox.execHandle = execHandle
	var stdout, stderr bytes.Buffer
	if err := client.Shell(context.Background(), node, []string{"sh", "-c", "cat"}, "/workspace", false, ShellStreams{Stdin: strings.NewReader("input"), Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatalf("Shell(streaming) error = %v", err)
	}
	<-sink.closed
	if stdout.String() != "out" || stderr.String() != "err" || sink.String() != "input" || sandbox.execConfig.Cwd != "/workspace" || !sandbox.execConfig.StdinPipe {
		t.Fatalf("streamed stdout=%q stderr=%q stdin=%q config=%#v", stdout.String(), stderr.String(), sink.String(), sandbox.execConfig)
	}
	if got := strings.Join(execHandle.calls, ","); got != "close" {
		t.Fatalf("exec handle calls = %q", got)
	}
}

func TestSDKSandboxClientCloneCleanupAndSSHHelper(t *testing.T) {
	runtime := newFakeSDKRuntime()
	sourceCalls := []string{}
	runtime.handles["source"] = &fakeSDKHandle{name: "source", status: microsandbox.SandboxStatusStopped, calls: &sourceCalls}
	target := &fakeSDKSandbox{}
	runtime.createSandbox = target
	client := &SDKSandboxClient{RuntimeCommands: defaultRuntimeCommandTemplates(), runtime: runtime}
	if err := client.Clone(context.Background(), Node{SandboxName: "source"}, Node{SandboxName: "target", Image: "image:12", VCPUs: 2, MemoryMiB: 4096, DiskMiB: 20480}); err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	if got := strings.Join(sourceCalls, ","); got != "snapshot:source:clone-target" {
		t.Fatalf("source calls = %q", got)
	}
	if runtime.createdConfig.Snapshot != "clone-target" || strings.Join(target.calls, ",") != "stop,close" || runtime.calls[len(runtime.calls)-1] != "remove-snapshot:clone-target" {
		t.Fatalf("clone config/calls = %#v %#v %#v", runtime.createdConfig, target.calls, runtime.calls)
	}

	keyPath := filepath.Join(t.TempDir(), "id.pub")
	if err := os.WriteFile(keyPath, []byte("ssh-ed25519 test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &fakeSDKSSHServer{}
	sshSandbox := &fakeSDKSandbox{sshServer: server}
	runtime.handles["ssh-node"] = &fakeSDKHandle{name: "ssh-node", status: microsandbox.SandboxStatusRunning, sandbox: sshSandbox}
	if err := sdkSSHServeWithRuntime(context.Background(), runtime, "ssh-node", keyPath); err != nil {
		t.Fatalf("sdkSSHServeWithRuntime() error = %v", err)
	}
	if sshSandbox.sshKeyPath != keyPath || strings.Join(server.calls, ",") != "serve,close" || strings.Join(sshSandbox.calls, ",") != "prepare-ssh,close" {
		t.Fatalf("SSH helper sandbox=%#v server=%#v", sshSandbox, server)
	}
}
