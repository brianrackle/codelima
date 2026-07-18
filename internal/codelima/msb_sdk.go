package codelima

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/console"
	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

type SDKSandboxClient struct {
	RuntimeCommands RuntimeCommandTemplates
	Stdout          io.Writer
	Stderr          io.Writer
	executable      string
	publicKeyPath   string
	transportMu     sync.RWMutex
	runtime         sdkRuntime
	attachReady     func(ShellStreams) error
}

func NewSDKSandboxClient() *SDKSandboxClient {
	return &SDKSandboxClient{RuntimeCommands: defaultRuntimeCommandTemplates(), runtime: officialSDKRuntime{}}
}

func (c *SDKSandboxClient) sdk() sdkRuntime {
	if c.runtime != nil {
		return c.runtime
	}
	return officialSDKRuntime{}
}

func (c *SDKSandboxClient) withIO(stdout, stderr io.Writer) *SDKSandboxClient {
	c.transportMu.RLock()
	defer c.transportMu.RUnlock()
	return &SDKSandboxClient{
		RuntimeCommands: c.RuntimeCommands,
		Stdout:          stdout,
		Stderr:          stderr,
		executable:      c.executable,
		publicKeyPath:   c.publicKeyPath,
		runtime:         c.runtime,
		attachReady:     c.attachReady,
	}
}

func (c *SDKSandboxClient) Version(ctx context.Context) (string, error) {
	if err := c.sdk().EnsureInstalled(ctx); err != nil {
		return "", mapSDKError("install microsandbox SDK runtime", err, nil)
	}
	sdkVersion, runtimeVersion, err := c.sdk().Versions()
	if err != nil {
		return "", mapSDKError("load microsandbox SDK runtime", err, nil)
	}
	if runtimeVersion != sdkVersion {
		return "", dependencyUnavailable("microsandbox SDK and runtime versions do not match", nil, map[string]any{
			"sdk_version": sdkVersion, "runtime_version": runtimeVersion,
		})
	}
	return sdkVersion, nil
}

func (c *SDKSandboxClient) ResolveCommands(project Project, node Node, kind runtimeCommandKind, values map[string]string) ([]string, error) {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, node.RuntimeCommands); err != nil {
		return nil, err
	}
	if kind != runtimeCommandBootstrap && kind != runtimeCommandWorkspaceSeedPrepare {
		return nil, preconditionFailed("microsandbox CLI command overrides are unavailable with the Go SDK", map[string]any{"kind": string(kind)})
	}
	return resolveConfiguredRuntimeCommands("", c.RuntimeCommands, project, node.RuntimeCommands, kind, values)
}

func (c *SDKSandboxClient) List(ctx context.Context) ([]RuntimeObservation, error) {
	handles, err := c.sdk().List(ctx)
	if err != nil {
		return nil, mapSDKError("list microsandboxes", err, nil)
	}
	observations := make([]RuntimeObservation, 0, len(handles))
	for _, handle := range handles {
		observations = append(observations, RuntimeObservation{
			Name: handle.Name(), Exists: true, Status: strings.ToLower(string(handle.Status())),
		})
	}
	return observations, nil
}

type sdkNodeConfig struct {
	name    string
	image   string
	vcpus   uint8
	memory  uint32
	disk    uint32
	mounts  map[string]microsandbox.MountConfig
	ports   map[uint16]uint16
	network *microsandbox.NetworkConfig
}

func resolveSDKNodeConfig(project Project, node Node) (sdkNodeConfig, error) {
	if err := validateSandboxName(node.SandboxName); err != nil {
		return sdkNodeConfig{}, err
	}
	image := strings.TrimSpace(node.Image)
	if image == "" {
		image = strings.TrimSpace(project.DefaultImage)
	}
	if node.VCPUs == 0 || node.MemoryMiB == 0 || node.DiskMiB == 0 {
		return sdkNodeConfig{}, invalidArgument("node vcpus, memory, and disk must be positive", map[string]any{"node": node.ID})
	}
	if image == "" {
		return sdkNodeConfig{}, invalidArgument("node image is required", map[string]any{"node": node.ID})
	}
	validatedPorts, err := validatePorts(node.Ports)
	if err != nil {
		return sdkNodeConfig{}, err
	}
	ports := make(map[uint16]uint16, len(validatedPorts))
	for _, value := range validatedPorts {
		parts := strings.Split(value, ":")
		host, _ := strconv.ParseUint(parts[0], 10, 16)
		guest, _ := strconv.ParseUint(parts[1], 10, 16)
		ports[uint16(host)] = uint16(guest)
	}
	mounts := map[string]microsandbox.MountConfig{}
	if nodeWorkspaceMode(node) == WorkspaceModeMounted {
		hostPath := strings.TrimSpace(node.WorkspaceMountPath)
		if hostPath == "" {
			hostPath = project.WorkspacePath
		}
		guestPath := strings.TrimSpace(node.GuestWorkspacePath)
		if hostPath == "" || guestPath == "" {
			return sdkNodeConfig{}, invalidArgument("mounted workspace requires host and guest paths", map[string]any{"node": node.ID})
		}
		mounts[guestPath] = microsandbox.Mount.Bind(hostPath, microsandbox.MountOptions{})
	}
	network, err := sdkNetworkConfig(node.NetPolicy)
	if err != nil {
		return sdkNodeConfig{}, err
	}
	return sdkNodeConfig{name: node.SandboxName, image: image, vcpus: node.VCPUs, memory: node.MemoryMiB, disk: node.DiskMiB, mounts: mounts, ports: ports, network: network}, nil
}

func sdkNetworkConfig(policy *NetPolicy) (*microsandbox.NetworkConfig, error) {
	if policy == nil {
		return nil, nil
	}
	defaultAction := strings.ToLower(strings.TrimSpace(policy.Default))
	if defaultAction != "allow" && defaultAction != "deny" {
		return nil, invalidArgument("net policy default must be allow or deny", map[string]any{"default": policy.Default})
	}
	network := &microsandbox.NetworkConfig{DefaultEgress: microsandbox.PolicyAction(defaultAction)}
	for _, target := range policy.Allow {
		target = strings.TrimSpace(target)
		if target == "" || strings.ContainsAny(target, "\r\n") {
			return nil, invalidArgument("net policy allow target is invalid", map[string]any{"target": target})
		}
		network.Rules = append(network.Rules, microsandbox.PolicyRule{
			Action: microsandbox.PolicyActionAllow, Direction: microsandbox.PolicyDirectionEgress, Destination: target,
		})
	}
	return network, nil
}

func sdkCreateOptions(config sdkNodeConfig, snapshot string) []microsandbox.SandboxOption {
	options := []microsandbox.SandboxOption{
		microsandbox.WithMemory(config.memory),
		microsandbox.WithCPUs(config.vcpus),
		microsandbox.WithInit(microsandbox.Init.Auto()),
		microsandbox.WithShell("/bin/bash"),
		microsandbox.WithDetached(),
	}
	if snapshot != "" {
		options = append(options, microsandbox.WithSnapshot(snapshot))
	} else {
		options = append(options, microsandbox.WithImage(config.image), microsandbox.WithOCIUpperSize(config.disk))
	}
	if len(config.mounts) != 0 {
		options = append(options, microsandbox.WithMounts(config.mounts))
	}
	if len(config.ports) != 0 {
		options = append(options, microsandbox.WithPorts(config.ports))
	}
	if config.network != nil {
		options = append(options, microsandbox.WithNetwork(config.network))
	}
	return options
}

func (c *SDKSandboxClient) Create(ctx context.Context, project Project, node Node) error {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, node.RuntimeCommands); err != nil {
		return err
	}
	config, err := resolveSDKNodeConfig(project, node)
	if err != nil {
		return err
	}
	sandbox, err := c.sdk().Create(ctx, config.name, sdkCreateOptions(config, "")...)
	if err != nil {
		return mapSDKError("create microsandbox", err, map[string]any{"sandbox_name": config.name})
	}
	if err := sandbox.Stop(ctx, microsandbox.WithStopTimeout(10*time.Minute)); err != nil {
		cleanupFailedSDKSandbox(c.sdk(), config.name, sandbox)
		return mapSDKError("stop newly created microsandbox", err, map[string]any{"sandbox_name": config.name})
	}
	if err := sandbox.Close(); err != nil {
		cleanupFailedSDKSandbox(c.sdk(), config.name, nil)
		return mapSDKError("release newly created microsandbox", err, map[string]any{"sandbox_name": config.name})
	}
	return nil
}

func (c *SDKSandboxClient) Start(ctx context.Context, project Project, node Node) error {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, node.RuntimeCommands); err != nil {
		return err
	}
	sandbox, err := c.sdk().StartDetached(ctx, node.SandboxName)
	if err != nil {
		return mapSDKError("start microsandbox", err, map[string]any{"sandbox_name": node.SandboxName})
	}
	if err := sandbox.Detach(ctx); err != nil {
		_ = sandbox.Close()
		return mapSDKError("detach started microsandbox", err, map[string]any{"sandbox_name": node.SandboxName})
	}
	return nil
}

func (c *SDKSandboxClient) Stop(ctx context.Context, project Project, node Node) error {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, node.RuntimeCommands); err != nil {
		return err
	}
	handle, err := c.sdk().Get(ctx, node.SandboxName)
	if err != nil {
		return mapSDKError("find microsandbox to stop", err, map[string]any{"sandbox_name": node.SandboxName})
	}
	if err := handle.Stop(ctx, microsandbox.WithStopTimeout(10*time.Minute)); err != nil {
		return mapSDKError("stop microsandbox", err, map[string]any{"sandbox_name": node.SandboxName})
	}
	return nil
}

func (c *SDKSandboxClient) Delete(ctx context.Context, project Project, node Node) error {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, node.RuntimeCommands); err != nil {
		return err
	}
	handle, err := c.sdk().Get(ctx, node.SandboxName)
	if microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound) {
		return nil
	}
	if err != nil {
		return mapSDKError("find microsandbox to remove", err, map[string]any{"sandbox_name": node.SandboxName})
	}
	if handle.Status() != microsandbox.SandboxStatusStopped {
		if err := handle.Kill(ctx, microsandbox.WithKillTimeout(30*time.Second)); err != nil {
			return mapSDKError("kill microsandbox before removal", err, map[string]any{"sandbox_name": node.SandboxName})
		}
	}
	if err := handle.Remove(ctx); err != nil {
		return mapSDKError("remove microsandbox", err, map[string]any{"sandbox_name": node.SandboxName})
	}
	return nil
}

func (c *SDKSandboxClient) Clone(ctx context.Context, project Project, sourceNode, targetNode Node) error {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, sourceNode.RuntimeCommands, targetNode.RuntimeCommands); err != nil {
		return err
	}
	config, err := resolveSDKNodeConfig(project, targetNode)
	if err != nil {
		return err
	}
	snapshotName := cloneSnapshotName(targetNode.SandboxName)
	source, err := c.sdk().Get(ctx, sourceNode.SandboxName)
	if err != nil {
		return mapSDKError("find clone source", err, map[string]any{"source_sandbox": sourceNode.SandboxName})
	}
	if err := source.Snapshot(ctx, snapshotName); err != nil {
		return mapSDKError("snapshot clone source", err, map[string]any{"source_sandbox": sourceNode.SandboxName, "snapshot": snapshotName})
	}
	defer func() { _ = c.sdk().RemoveSnapshot(context.Background(), snapshotName) }()
	target, err := c.sdk().Create(ctx, config.name, sdkCreateOptions(config, snapshotName)...)
	if err != nil {
		return mapSDKError("create microsandbox clone", err, map[string]any{"source_sandbox": sourceNode.SandboxName, "sandbox_name": targetNode.SandboxName})
	}
	if err := target.Stop(ctx, microsandbox.WithStopTimeout(10*time.Minute)); err != nil {
		cleanupFailedSDKSandbox(c.sdk(), config.name, target)
		return mapSDKError("stop microsandbox clone", err, map[string]any{"sandbox_name": targetNode.SandboxName})
	}
	if err := target.Close(); err != nil {
		cleanupFailedSDKSandbox(c.sdk(), config.name, nil)
		return mapSDKError("release microsandbox clone", err, map[string]any{"sandbox_name": targetNode.SandboxName})
	}
	return nil
}

func cleanupFailedSDKSandbox(runtime sdkRuntime, name string, sandbox sdkSandbox) {
	ctx := context.Background()
	if sandbox != nil {
		_ = sandbox.Kill(ctx, microsandbox.WithKillTimeout(30*time.Second))
		_ = sandbox.Close()
	}
	handle, err := runtime.Get(ctx, name)
	if err != nil {
		return
	}
	if handle.Status() != microsandbox.SandboxStatusStopped {
		_ = handle.Kill(ctx, microsandbox.WithKillTimeout(30*time.Second))
	}
	_ = handle.Remove(ctx)
}

func (c *SDKSandboxClient) CopyToGuest(ctx context.Context, project Project, node Node, sourcePath, targetPath string, recursive bool) error {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, node.RuntimeCommands); err != nil {
		return err
	}
	sandbox, err := connectSDKSandbox(ctx, c.sdk(), node.SandboxName)
	if err != nil {
		return err
	}
	defer func() { _ = sandbox.Close() }()
	if err := c.copyHostPath(ctx, sandbox, node.SandboxName, sourcePath, targetPath, recursive); err != nil {
		return mapSDKError("copy into microsandbox", err, map[string]any{"sandbox_name": node.SandboxName, "source_path": sourcePath, "target_path": targetPath})
	}
	return nil
}

func (c *SDKSandboxClient) copyHostPath(ctx context.Context, sandbox sdkSandbox, sandboxName, sourcePath, targetPath string, recursive bool) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return c.copyHostSymlink(ctx, sandbox, sandboxName, sourcePath, targetPath)
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return invalidArgument("host copy source must be a regular file, directory, or symbolic link", map[string]any{"source_path": sourcePath})
		}
		return sandbox.CopyFromHost(ctx, sourcePath, targetPath)
	}
	if !recursive {
		return invalidArgument("copying a host directory requires recursive mode", map[string]any{"source_path": sourcePath})
	}
	return filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		guestPath := targetPath
		if relative != "." {
			guestPath = filepath.Join(targetPath, relative)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return c.copyHostSymlink(ctx, sandbox, sandboxName, path, guestPath)
		}
		if entry.IsDir() {
			return sandbox.Mkdir(ctx, guestPath)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return invalidArgument("host copy source contains an unsupported special file", map[string]any{"source_path": path})
		}
		return sandbox.CopyFromHost(ctx, path, guestPath)
	})
}

func (c *SDKSandboxClient) copyHostSymlink(ctx context.Context, sandbox sdkSandbox, sandboxName, sourcePath, targetPath string) error {
	linkTarget, err := os.Readlink(sourcePath)
	if err != nil {
		return err
	}
	return c.runSDKExec(ctx, sandbox, sandboxName, []string{"ln", "-s", linkTarget, targetPath}, "", ShellStreams{})
}

func (c *SDKSandboxClient) Shell(ctx context.Context, project Project, node Node, command []string, workdir string, interactive bool, streams ShellStreams) error {
	if err := validateSDKRuntimeCommandTemplates(c.RuntimeCommands, project.RuntimeCommands, node.RuntimeCommands); err != nil {
		return err
	}
	if interactive {
		if err := c.validateAttachStreams(streams); err != nil {
			return err
		}
	}
	sandbox, err := connectSDKSandbox(ctx, c.sdk(), node.SandboxName)
	if err != nil {
		return err
	}
	defer func() { _ = sandbox.Close() }()
	if len(command) == 0 {
		command = []string{"/bin/bash"}
	}
	if interactive {
		attachCommand := command
		if workdir != "" {
			attachCommand = []string{"/bin/sh", "-lc", "cd -- " + shellQuote(workdir) + " && exec " + shellArgsFragment(command)}
		}
		status, attachErr := sandbox.Attach(ctx, attachCommand[0], attachCommand[1:]...)
		if attachErr != nil {
			return mapSDKError("open microsandbox shell", attachErr, map[string]any{"sandbox_name": node.SandboxName})
		}
		if status != 0 {
			return guestExitError("microsandbox shell", status, nil, map[string]any{"sandbox_name": node.SandboxName})
		}
		return nil
	}
	return c.runSDKExec(ctx, sandbox, node.SandboxName, command, workdir, streams)
}

func (c *SDKSandboxClient) validateAttachStreams(streams ShellStreams) error {
	if c.attachReady != nil {
		return c.attachReady(streams)
	}
	if streams.Stdin != os.Stdin || streams.Stdout != os.Stdout || streams.Stderr != os.Stderr {
		return preconditionFailed("interactive SDK attach requires the codelima process terminal streams", nil)
	}
	if _, err := console.ConsoleFromFile(os.Stdin); err != nil {
		return preconditionFailed("interactive SDK attach requires a real TTY", nil)
	}
	return nil
}

func (c *SDKSandboxClient) runSDKExec(ctx context.Context, sandbox sdkSandbox, sandboxName string, command []string, workdir string, streams ShellStreams) error {
	options := []microsandbox.ExecOption{}
	if workdir != "" {
		options = append(options, microsandbox.WithExecCwd(workdir))
	}
	if streams.Stdin != nil {
		options = append(options, microsandbox.WithExecStdinPipe())
	}
	handle, err := sandbox.ExecStream(ctx, command[0], command[1:], options...)
	if err != nil {
		return mapSDKError("start microsandbox command", err, map[string]any{"sandbox_name": sandboxName})
	}
	defer func() { _ = handle.Close() }()
	if streams.Stdin != nil {
		sink := handle.TakeStdin()
		if sink == nil {
			return externalCommandFailed("start microsandbox command stdin", errors.New("SDK did not provide the requested stdin pipe"), map[string]any{"sandbox_name": sandboxName})
		}
		go func() {
			_, _ = io.Copy(sink, streams.Stdin)
			_ = sink.Close()
		}()
	}

	stdout := streams.Stdout
	if stdout == nil {
		stdout = c.Stdout
	}
	stderr := streams.Stderr
	if stderr == nil {
		stderr = c.Stderr
	}
	var stderrCapture bytes.Buffer
	stderr = sdkOutputWriter(&stderrCapture, stderr)
	exitCode := -1
	for {
		event, recvErr := handle.Recv(ctx)
		if recvErr != nil {
			if ctx.Err() != nil {
				_ = handle.Kill(context.Background())
			}
			return mapSDKError("stream microsandbox command", recvErr, map[string]any{"sandbox_name": sandboxName})
		}
		switch event.Kind {
		case microsandbox.ExecEventStdout:
			if stdout != nil {
				_, _ = stdout.Write(event.Data)
			}
		case microsandbox.ExecEventStderr:
			if stderr != nil {
				_, _ = stderr.Write(event.Data)
			}
		case microsandbox.ExecEventExited:
			exitCode = event.ExitCode
		case microsandbox.ExecEventFailed, microsandbox.ExecEventStdinError:
			_ = handle.Kill(context.Background())
			message := "guest process failed"
			if event.Failure != nil && event.Failure.Message != "" {
				message = event.Failure.Message
			}
			return externalCommandFailed("microsandbox command failed", errors.New(message), map[string]any{"sandbox_name": sandboxName})
		case microsandbox.ExecEventDone:
			if exitCode != 0 {
				return guestExitError("microsandbox command", exitCode, stderrCapture.Bytes(), map[string]any{"sandbox_name": sandboxName})
			}
			return nil
		}
	}
}

func sdkOutputWriter(primary, secondary io.Writer) io.Writer {
	if secondary == nil {
		return primary
	}
	return io.MultiWriter(primary, secondary)
}

func connectSDKSandbox(ctx context.Context, runtime sdkRuntime, name string) (sdkSandbox, error) {
	handle, err := runtime.Get(ctx, name)
	if err != nil {
		return nil, mapSDKError("find running microsandbox", err, map[string]any{"sandbox_name": name})
	}
	sandbox, err := handle.Connect(ctx)
	if err != nil {
		return nil, mapSDKError("connect to microsandbox", err, map[string]any{"sandbox_name": name})
	}
	return sandbox, nil
}

func guestExitError(action string, status int, stderr []byte, fields map[string]any) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["exit_status"] = status
	message := strings.TrimSpace(string(stderr))
	if message == "" {
		message = fmt.Sprintf("guest process exited with status %d", status)
	}
	return externalCommandFailed(action+" failed", errors.New(message), fields)
}

func mapSDKError(action string, err error, fields map[string]any) error {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return err
	}
	switch {
	case microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound):
		return notFound(action+" failed: "+err.Error(), fields)
	case microsandbox.IsKind(err, microsandbox.ErrSandboxAlreadyExists),
		microsandbox.IsKind(err, microsandbox.ErrSandboxStillRunning),
		microsandbox.IsKind(err, microsandbox.ErrSnapshotAlreadyExists):
		return preconditionFailed(action+" failed: "+err.Error(), fields)
	case microsandbox.IsKind(err, microsandbox.ErrInvalidArgument),
		microsandbox.IsKind(err, microsandbox.ErrInvalidConfig),
		microsandbox.IsKind(err, microsandbox.ErrNetworkPolicy):
		return invalidArgument(action+" failed: "+err.Error(), fields)
	case microsandbox.IsKind(err, microsandbox.ErrLibraryNotLoaded):
		return dependencyUnavailable("microsandbox SDK runtime is unavailable", err, fields)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		microsandbox.IsKind(err, microsandbox.ErrCancelled):
		return err
	default:
		return externalCommandFailed(action+" failed", err, fields)
	}
}

func sdkSSHServe(ctx context.Context, sandboxName, authorizedKeysPath string) error {
	return sdkSSHServeWithRuntime(ctx, officialSDKRuntime{}, sandboxName, authorizedKeysPath)
}

func sdkSSHServeWithRuntime(ctx context.Context, runtime sdkRuntime, sandboxName, authorizedKeysPath string) error {
	if err := validateSandboxName(sandboxName); err != nil {
		return err
	}
	authorizedKeysPath, err := filepath.Abs(authorizedKeysPath)
	if err != nil {
		return invalidArgument("failed to resolve authorized keys path", map[string]any{"path": authorizedKeysPath})
	}
	if info, err := os.Stat(authorizedKeysPath); err != nil || info.IsDir() {
		return invalidArgument("authorized keys file is unavailable", map[string]any{"path": authorizedKeysPath})
	}
	sandbox, err := connectSDKSandbox(ctx, runtime, sandboxName)
	if err != nil {
		return err
	}
	defer func() { _ = sandbox.Close() }()
	server, err := sandbox.PrepareSSHServer(ctx, authorizedKeysPath)
	if err != nil {
		return mapSDKError("prepare microsandbox SSH server", err, map[string]any{"sandbox_name": sandboxName})
	}
	defer func() { _ = server.Close(context.Background()) }()
	if err := server.ServeConnection(ctx); err != nil {
		return mapSDKError("serve microsandbox SSH connection", err, map[string]any{"sandbox_name": sandboxName})
	}
	return nil
}
