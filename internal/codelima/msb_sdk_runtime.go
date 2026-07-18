package codelima

import (
	"context"
	"io"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

type sdkRuntime interface {
	EnsureInstalled(context.Context) error
	Versions() (string, string, error)
	List(context.Context) ([]sdkSandboxHandle, error)
	Create(context.Context, string, ...microsandbox.SandboxOption) (sdkSandbox, error)
	StartDetached(context.Context, string) (sdkSandbox, error)
	Get(context.Context, string) (sdkSandboxHandle, error)
	RemoveSnapshot(context.Context, string) error
}

type sdkSandboxHandle interface {
	Name() string
	Status() microsandbox.SandboxStatus
	Connect(context.Context) (sdkSandbox, error)
	Stop(context.Context, ...microsandbox.StopOption) error
	Kill(context.Context, ...microsandbox.KillOption) error
	Remove(context.Context) error
	Snapshot(context.Context, string) error
}

type sdkSandbox interface {
	Stop(context.Context, ...microsandbox.StopOption) error
	Kill(context.Context, ...microsandbox.KillOption) error
	Close() error
	Detach(context.Context) error
	CopyFromHost(context.Context, string, string) error
	Mkdir(context.Context, string) error
	Attach(context.Context, string, ...string) (int, error)
	ExecStream(context.Context, string, []string, ...microsandbox.ExecOption) (sdkExecHandle, error)
	PrepareSSHServer(context.Context, string) (sdkSSHServer, error)
}

type sdkExecHandle interface {
	TakeStdin() io.WriteCloser
	Recv(context.Context) (*microsandbox.ExecEvent, error)
	Kill(context.Context) error
	Close() error
}

type sdkSSHServer interface {
	ServeConnection(context.Context) error
	Close(context.Context) error
}

type officialSDKRuntime struct{}

func (officialSDKRuntime) EnsureInstalled(ctx context.Context) error {
	return microsandbox.EnsureInstalled(ctx)
}

func (officialSDKRuntime) Versions() (string, string, error) {
	sdkVersion := microsandbox.SDKVersion()
	runtimeVersion, err := microsandbox.RuntimeVersion()
	return sdkVersion, runtimeVersion, err
}

func (officialSDKRuntime) List(ctx context.Context) ([]sdkSandboxHandle, error) {
	handles, err := microsandbox.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]sdkSandboxHandle, 0, len(handles))
	for _, handle := range handles {
		result = append(result, officialSDKHandle{handle})
	}
	return result, nil
}

func (officialSDKRuntime) Create(ctx context.Context, name string, options ...microsandbox.SandboxOption) (sdkSandbox, error) {
	sandbox, err := microsandbox.CreateSandbox(ctx, name, options...)
	if err != nil {
		return nil, err
	}
	return officialSDKSandbox{sandbox}, nil
}

func (officialSDKRuntime) StartDetached(ctx context.Context, name string) (sdkSandbox, error) {
	sandbox, err := microsandbox.StartSandboxDetached(ctx, name)
	if err != nil {
		return nil, err
	}
	return officialSDKSandbox{sandbox}, nil
}

func (officialSDKRuntime) Get(ctx context.Context, name string) (sdkSandboxHandle, error) {
	handle, err := microsandbox.GetSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return officialSDKHandle{handle}, nil
}

func (officialSDKRuntime) RemoveSnapshot(ctx context.Context, name string) error {
	return microsandbox.Snapshot.Remove(ctx, name, true)
}

type officialSDKHandle struct{ *microsandbox.SandboxHandle }

func (h officialSDKHandle) Connect(ctx context.Context) (sdkSandbox, error) {
	sandbox, err := h.SandboxHandle.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return officialSDKSandbox{sandbox}, nil
}

func (h officialSDKHandle) Snapshot(ctx context.Context, name string) error {
	_, err := h.SandboxHandle.Snapshot(ctx, name)
	return err
}

type officialSDKSandbox struct{ *microsandbox.Sandbox }

func (s officialSDKSandbox) CopyFromHost(ctx context.Context, sourcePath, targetPath string) error {
	return s.Sandbox.FS().CopyFromHost(ctx, sourcePath, targetPath)
}

func (s officialSDKSandbox) Mkdir(ctx context.Context, path string) error {
	return s.Sandbox.FS().Mkdir(ctx, path)
}

func (s officialSDKSandbox) ExecStream(ctx context.Context, command string, args []string, options ...microsandbox.ExecOption) (sdkExecHandle, error) {
	handle, err := s.Sandbox.ExecStream(ctx, command, args, options...)
	if err != nil {
		return nil, err
	}
	return officialSDKExecHandle{handle}, nil
}

func (s officialSDKSandbox) PrepareSSHServer(ctx context.Context, authorizedKeysPath string) (sdkSSHServer, error) {
	server, err := s.Sandbox.SSH().PrepareServer(ctx, microsandbox.WithSSHAuthorizedKeysPath(authorizedKeysPath))
	if err != nil {
		return nil, err
	}
	return server, nil
}

type officialSDKExecHandle struct{ *microsandbox.ExecHandle }

func (h officialSDKExecHandle) TakeStdin() io.WriteCloser { return h.ExecHandle.TakeStdin() }
