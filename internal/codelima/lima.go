package codelima

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	requiredLimaVersion = "2.1.0"
	limaCommandTimeout  = 15 * time.Minute
	limaOutputLimit     = 16 << 20
)

// LimaClient adapts the Lima 2.x CLI to CodeLima's runtime contract. Lima's
// CLI is the supported API boundary: every operation remains observable and
// reproducible with the same limactl commands a maintainer can run by hand.
type LimaClient struct {
	Binary          string
	MetadataRoot    string
	RuntimeCommands RuntimeCommandTemplates
	Stdout          io.Writer
	Stderr          io.Writer
	GOOS            string
	GOARCH          string
	LimaHome        string
	UnixSocketProbe func(string) error

	observer *limaObservationService
}

func NewLimaClient(metadataRoot string) *LimaClient {
	return &LimaClient{
		Binary:          "limactl",
		MetadataRoot:    metadataRoot,
		RuntimeCommands: defaultRuntimeCommandTemplates(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		observer:        newLimaObservationService(),
	}
}

func (c *LimaClient) withIO(stdout, stderr io.Writer) *LimaClient {
	clone := *c
	clone.Stdout = stdout
	clone.Stderr = stderr
	return &clone
}

func (c *LimaClient) binary() string {
	if strings.TrimSpace(c.Binary) == "" {
		return "limactl"
	}
	return c.Binary
}

func (c *LimaClient) platform() (string, string) {
	goos, goarch := c.GOOS, c.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (c *LimaClient) Version(ctx context.Context) (string, error) {
	commands, err := c.ResolveCommands(Node{}, runtimeCommandVersion, nil)
	if err != nil {
		return "", err
	}
	stdout, err := c.runCommands(ctx, time.Minute, "inspect Lima version", commands, nil)
	if err != nil {
		return "", err
	}
	version, err := parseLimaVersion(string(stdout))
	if err != nil {
		return "", dependencyUnavailable("could not parse limactl version", err, nil)
	}
	if err := validateLimaVersion(version); err != nil {
		return "", err
	}
	return version, nil
}

func parseLimaVersion(output string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(output))
	for _, field := range fields {
		candidate := strings.TrimPrefix(strings.TrimSpace(field), "v")
		parts := strings.Split(candidate, ".")
		if len(parts) != 3 {
			continue
		}
		valid := true
		for _, part := range parts {
			if _, err := strconv.Atoi(part); err != nil {
				valid = false
				break
			}
		}
		if valid {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unrecognized output %q", strings.TrimSpace(output))
}

func validateLimaVersion(version string) error {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return dependencyUnavailable("unsupported Lima version", nil, map[string]any{"found": version, "required": requiredLimaVersion})
	}
	values := [3]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return dependencyUnavailable("unsupported Lima version", err, map[string]any{"found": version, "required": requiredLimaVersion})
		}
		values[index] = value
	}
	minimum := [3]int{2, 1, 0}
	if values[0] != minimum[0] || values[1] < minimum[1] || values[1] == minimum[1] && values[2] < minimum[2] {
		return dependencyUnavailable("unsupported Lima version", nil, map[string]any{"found": version, "required": requiredLimaVersion, "supported_major": 2})
	}
	return nil
}

func (c *LimaClient) ResolveCommands(node Node, kind runtimeCommandKind, values map[string]string) ([]string, error) {
	return resolveConfiguredRuntimeCommands(c.binary(), c.RuntimeCommands, node.RuntimeCommands, kind, values)
}

func (c *LimaClient) List(ctx context.Context) ([]RuntimeObservation, error) {
	if c.observer != nil {
		if observations, ok := c.observer.cached(); ok {
			return observations, nil
		}
	}
	return c.listDirect(ctx)
}

func (c *LimaClient) listDirect(ctx context.Context) ([]RuntimeObservation, error) {
	commands, err := c.ResolveCommands(Node{}, runtimeCommandList, nil)
	if err != nil {
		return nil, err
	}
	stdout, err := c.runCommands(ctx, time.Minute, "list Lima instances", commands, nil)
	if err != nil {
		return nil, err
	}
	observations, err := parseLimaList(stdout)
	if err != nil {
		return nil, err
	}
	return observations, nil
}

type limaListRecord struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Dir           string `json:"dir"`
	Hostname      string `json:"hostname"`
	SSHAddress    string `json:"sshAddress"`
	SSHLocalPort  int    `json:"sshLocalPort"`
	SSHConfigFile string `json:"sshConfigFile"`
	LimaHome      string `json:"LimaHome"`
	LimaVersion   string `json:"limaVersion"`
	VMType        string `json:"vmType"`
}

func parseLimaList(data []byte) ([]RuntimeObservation, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []RuntimeObservation{}, nil
	}
	records := []limaListRecord{}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, metadataCorruption("failed to parse limactl list output", err, nil)
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(trimmed))
		scanner.Buffer(make([]byte, 64*1024), limaOutputLimit)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var record limaListRecord
			if err := json.Unmarshal(line, &record); err != nil {
				return nil, metadataCorruption("failed to parse limactl list output", err, map[string]any{"line": boundedText(line, 512)})
			}
			records = append(records, record)
		}
		if err := scanner.Err(); err != nil {
			return nil, metadataCorruption("failed to scan limactl list output", err, nil)
		}
	}

	observations := make([]RuntimeObservation, 0, len(records))
	for _, record := range records {
		name := strings.TrimSpace(record.Name)
		if name == "" {
			return nil, metadataCorruption("limactl list record is missing an instance name", nil, nil)
		}
		status, err := normalizeLimaStatus(record.Status)
		if err != nil {
			return nil, metadataCorruption("limactl list record has an invalid status", err, map[string]any{"sandbox_name": name, "status": record.Status})
		}
		observations = append(observations, RuntimeObservation{
			Name: name, Exists: true, Status: status, Dir: record.Dir, Hostname: record.Hostname,
			SSHAddress: record.SSHAddress, SSHPort: record.SSHLocalPort, SSHConfigFile: record.SSHConfigFile,
			LimaHome: record.LimaHome, LimaVersion: record.LimaVersion, VMType: record.VMType,
		})
	}
	return observations, nil
}

func normalizeLimaStatus(status string) (ObservationStatus, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "uninitialized":
		return ObservationUninitialized, nil
	case "installing":
		return ObservationInstalling, nil
	case "broken":
		return ObservationBroken, nil
	case "running":
		return ObservationRunning, nil
	case "stopped":
		return ObservationStopped, nil
	default:
		return "", fmt.Errorf("unsupported Lima status %q", status)
	}
}

func (c *LimaClient) Create(ctx context.Context, node Node) error {
	if err := validateLimaNode(node); err != nil {
		return err
	}
	if err := c.validateLimaHome(node.SandboxName); err != nil {
		return err
	}
	observations, err := c.listDirect(ctx)
	if err != nil {
		return err
	}
	if _, exists := findObservation(observations, node.SandboxName); exists {
		return preconditionFailed("Lima instance name already exists", map[string]any{"sandbox_name": node.SandboxName})
	}
	template, err := c.resolveTemplate(ctx, node.Image)
	if err != nil {
		return err
	}
	goos, goarch := c.platform()
	rendered, err := renderLimaTemplate(template, node, goos, goarch)
	if err != nil {
		return err
	}
	templatePath := c.templatePath(node)
	if err := writeRenderedLimaTemplate(templatePath, rendered); err != nil {
		return metadataCorruption("failed to write rendered Lima template", err, map[string]any{"path": templatePath})
	}
	if _, err := c.runDirect(ctx, 2*time.Minute, "validate Lima template", nil, c.binary(), "validate", templatePath); err != nil {
		return err
	}
	commands, err := c.ResolveCommands(node, runtimeCommandCreate, map[string]string{
		"sandbox_name":  shellQuote(node.SandboxName),
		"template_path": shellQuote(templatePath),
	})
	if err != nil {
		return err
	}
	if _, err := c.runCommands(ctx, limaCommandTimeout, "create Lima instance", commands, map[string]any{"sandbox_name": node.SandboxName}); err != nil {
		return &runtimeMutationError{error: err}
	}
	if err := c.verifyObservation(ctx, node.SandboxName, ObservationStopped); err != nil {
		return &runtimeMutationError{error: err}
	}
	return nil
}

func (c *LimaClient) resolveTemplate(ctx context.Context, locator string) ([]byte, error) {
	locator = strings.TrimSpace(locator)
	if locator == "" || strings.ContainsAny(locator, "\r\n") {
		return nil, invalidArgument("node image must be a valid Lima template reference", map[string]any{"image": locator})
	}
	stdout, err := c.runDirect(ctx, 2*time.Minute, "resolve Lima template", nil, c.binary(), "template", "copy", "--fill", locator, "-")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(stdout)) == 0 {
		return nil, metadataCorruption("limactl returned an empty template", nil, map[string]any{"image": locator})
	}
	return stdout, nil
}

func (c *LimaClient) templatePath(node Node) string {
	return filepath.Join(c.MetadataRoot, "nodes", node.ID, "instance.lima.yaml")
}

func validateLimaNode(node Node) error {
	if err := validateSandboxName(node.SandboxName); err != nil {
		return err
	}
	if strings.TrimSpace(node.ID) == "" {
		return invalidArgument("node id is required", nil)
	}
	if strings.TrimSpace(node.Image) == "" {
		return invalidArgument("node image is required", map[string]any{"node": node.ID})
	}
	if node.VCPUs == 0 || node.MemoryMiB == 0 || node.DiskMiB == 0 {
		return invalidArgument("node vcpus, memory, and disk must be positive", map[string]any{"node": node.ID})
	}
	_, err := validatePorts(node.Ports)
	return err
}

func (c *LimaClient) validateLimaHome(instanceName string) error {
	absoluteHome, err := c.resolvedLimaHome()
	if err != nil {
		return err
	}

	limit := 4095
	switch goos, _ := c.platform(); goos {
	case "darwin":
		limit = 103
	case "linux":
		limit = 107
	}
	longestSocket := filepath.Join(absoluteHome, instanceName, "ssh.sock.1234567890123456")
	if len([]byte(longestSocket)) > limit {
		return invalidArgument("LIMA_HOME and sandbox name exceed the Unix-socket path limit", map[string]any{
			"lima_home": absoluteHome, "sandbox_name": instanceName, "socket_path_length": len([]byte(longestSocket)), "maximum": limit,
		})
	}
	if err := os.MkdirAll(absoluteHome, 0o700); err != nil {
		return invalidArgument("LIMA_HOME must be a writable directory", map[string]any{"path": absoluteHome, "error": err.Error()})
	}
	probePath := filepath.Join(absoluteHome, fmt.Sprintf(".codelima-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	probe := c.UnixSocketProbe
	if probe == nil {
		probe = probeUnixSocket
	}
	if err := probe(probePath); err != nil {
		return invalidArgument("LIMA_HOME filesystem must support Unix sockets", map[string]any{"path": absoluteHome, "error": err.Error()})
	}
	return nil
}

func (c *LimaClient) resolvedLimaHome() (string, error) {
	home := strings.TrimSpace(c.LimaHome)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("LIMA_HOME"))
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", dependencyUnavailable("resolve LIMA_HOME", err, nil)
		}
		home = filepath.Join(userHome, ".lima")
	}
	absoluteHome, err := filepath.Abs(home)
	if err != nil {
		return "", invalidArgument("LIMA_HOME must resolve to an absolute path", map[string]any{"path": home})
	}
	if _, statErr := os.Stat(absoluteHome); statErr == nil {
		resolvedHome, resolveErr := filepath.EvalSymlinks(absoluteHome)
		if resolveErr != nil {
			return "", invalidArgument("LIMA_HOME symlinks must be resolvable", map[string]any{"path": home})
		}
		absoluteHome = resolvedHome
	} else if !os.IsNotExist(statErr) {
		return "", invalidArgument("LIMA_HOME must be accessible", map[string]any{"path": home, "error": statErr.Error()})
	}
	return filepath.Clean(absoluteHome), nil
}

func probeUnixSocket(path string) error {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	if closeErr := listener.Close(); closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func renderLimaTemplate(template []byte, node Node, goos, goarch string) ([]byte, error) {
	if err := validateLimaNode(node); err != nil {
		return nil, err
	}
	settings := map[string]any{}
	if err := yaml.Unmarshal(template, &settings); err != nil {
		return nil, metadataCorruption("failed to parse resolved Lima template", err, nil)
	}
	if len(settings) == 0 {
		return nil, metadataCorruption("resolved Lima template is empty", nil, nil)
	}

	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if arch == "" {
		return nil, unsupportedFeature("host architecture is not supported by the Lima runtime", map[string]any{"goarch": goarch})
	}
	vmType, mountType := "", ""
	switch goos {
	case "darwin":
		vmType, mountType = "vz", "virtiofs"
	case "linux":
		vmType, mountType = "qemu", "9p"
	default:
		return nil, unsupportedFeature("host operating system is not supported by the Lima runtime", map[string]any{"goos": goos})
	}

	settings["os"] = "Linux"
	settings["arch"] = arch
	settings["vmType"] = vmType
	settings["mountType"] = mountType
	settings["cpus"] = int(node.VCPUs)
	settings["memory"] = fmt.Sprintf("%dMiB", node.MemoryMiB)
	settings["disk"] = fmt.Sprintf("%dMiB", node.DiskMiB)
	settings["plain"] = false
	settings["upgradePackages"] = false
	settings["nestedVirtualization"] = false
	settings["containerd"] = map[string]any{"system": false, "user": false, "archives": []any{}}
	settings["ssh"] = map[string]any{
		"localPort": 0, "loadDotSSHPubKeys": false, "forwardAgent": false,
		"forwardX11": false, "forwardX11Trusted": false, "overVsock": false,
	}
	settings["audio"] = map[string]any{"device": ""}
	settings["video"] = map[string]any{"display": "none"}

	mounts := []any{}
	if nodeWorkspaceMode(node) == WorkspaceModeMounted {
		hostPath := strings.TrimSpace(node.WorkspaceMountPath)
		if hostPath == "" {
			hostPath = node.DirectoryPath
		}
		resolvedHostPath, err := filepath.EvalSymlinks(hostPath)
		if err != nil {
			return nil, invalidArgument("mounted workspace path must be resolvable", map[string]any{"path": hostPath})
		}
		resolvedHostPath, err = filepath.Abs(resolvedHostPath)
		if err != nil {
			return nil, invalidArgument("mounted workspace path must be absolute", map[string]any{"path": hostPath})
		}
		guestPath := filepath.Clean(strings.TrimSpace(node.GuestWorkspacePath))
		if !filepath.IsAbs(guestPath) || guestPath == string(filepath.Separator) {
			return nil, invalidArgument("guest workspace path must be an absolute non-root path", map[string]any{"path": node.GuestWorkspacePath})
		}
		mounts = append(mounts, map[string]any{"location": resolvedHostPath, "mountPoint": guestPath, "writable": true})
	}
	settings["mounts"] = mounts

	validatedPorts, err := validatePorts(node.Ports)
	if err != nil {
		return nil, err
	}
	portForwards := make([]any, 0, len(validatedPorts)+1)
	for _, value := range validatedPorts {
		parts := strings.Split(value, ":")
		hostPort, _ := strconv.Atoi(parts[0])
		guestPort, _ := strconv.Atoi(parts[1])
		portForwards = append(portForwards, map[string]any{
			"guestPort": guestPort, "hostPort": hostPort, "hostIP": "127.0.0.1", "proto": "tcp", "static": true,
		})
	}
	portForwards = append(portForwards, map[string]any{
		"guestIP": "0.0.0.0", "guestIPMustBeZero": false, "guestPortRange": []int{1, 65535}, "proto": "any", "ignore": true,
	})
	settings["portForwards"] = portForwards

	data, err := yaml.Marshal(settings)
	if err != nil {
		return nil, metadataCorruption("failed to serialize rendered Lima template", err, nil)
	}
	return data, nil
}

func writeRenderedLimaTemplate(path string, data []byte) error {
	return atomicWriteFile(path, data, 0o600)
}

func (c *LimaClient) Start(ctx context.Context, node Node) error {
	commands, err := c.ResolveCommands(node, runtimeCommandStart, map[string]string{"sandbox_name": shellQuote(node.SandboxName)})
	if err != nil {
		return err
	}
	if _, err := c.runCommands(ctx, limaCommandTimeout, "start Lima instance", commands, map[string]any{"sandbox_name": node.SandboxName}); err != nil {
		return err
	}
	return c.verifyObservation(ctx, node.SandboxName, ObservationRunning)
}

func (c *LimaClient) Stop(ctx context.Context, node Node) error {
	commands, err := c.ResolveCommands(node, runtimeCommandStop, map[string]string{"sandbox_name": shellQuote(node.SandboxName)})
	if err != nil {
		return err
	}
	if _, err := c.runCommands(ctx, 10*time.Minute, "stop Lima instance", commands, map[string]any{"sandbox_name": node.SandboxName}); err != nil {
		return err
	}
	return c.verifyObservation(ctx, node.SandboxName, ObservationStopped)
}

func (c *LimaClient) Delete(ctx context.Context, node Node) error {
	if err := validateSandboxName(node.SandboxName); err != nil {
		return err
	}
	observations, err := c.listDirect(ctx)
	if err != nil {
		return err
	}
	observation, exists := findObservation(observations, node.SandboxName)
	if !exists {
		c.removeCachedObservation(node.SandboxName)
		return nil
	}
	if observation.Status == ObservationRunning {
		if err := c.Stop(ctx, node); err != nil {
			return err
		}
	}
	commands, err := c.ResolveCommands(node, runtimeCommandDelete, map[string]string{"sandbox_name": shellQuote(node.SandboxName)})
	if err != nil {
		return err
	}
	if _, err := c.runCommands(ctx, 10*time.Minute, "delete Lima instance", commands, map[string]any{"sandbox_name": node.SandboxName}); err != nil {
		return err
	}
	c.removeCachedObservation(node.SandboxName)
	return nil
}

func (c *LimaClient) Clone(ctx context.Context, sourceNode, targetNode Node) error {
	if err := validateSandboxName(sourceNode.SandboxName); err != nil {
		return err
	}
	if err := validateSandboxName(targetNode.SandboxName); err != nil {
		return err
	}
	if err := c.validateLimaHome(targetNode.SandboxName); err != nil {
		return err
	}
	observations, err := c.listDirect(ctx)
	if err != nil {
		return err
	}
	if _, exists := findObservation(observations, sourceNode.SandboxName); !exists {
		return notFound("source Lima instance does not exist", map[string]any{"sandbox_name": sourceNode.SandboxName})
	}
	if _, exists := findObservation(observations, targetNode.SandboxName); exists {
		return preconditionFailed("target Lima instance name already exists", map[string]any{"sandbox_name": targetNode.SandboxName})
	}
	commands, err := c.ResolveCommands(targetNode, runtimeCommandClone, map[string]string{
		"source_sandbox": shellQuote(sourceNode.SandboxName),
		"sandbox_name":   shellQuote(targetNode.SandboxName),
	})
	if err != nil {
		return err
	}
	if _, err := c.runCommands(ctx, 20*time.Minute, "clone Lima instance", commands, map[string]any{
		"source_sandbox": sourceNode.SandboxName, "sandbox_name": targetNode.SandboxName,
	}); err != nil {
		return &runtimeMutationError{error: err}
	}
	if err := c.verifyObservation(ctx, targetNode.SandboxName, ObservationStopped); err != nil {
		return &runtimeMutationError{error: err}
	}
	return nil
}

func (c *LimaClient) CopyToGuest(ctx context.Context, node Node, sourcePath, targetPath string, recursive bool) error {
	recursiveFlag := ""
	if recursive {
		recursiveFlag = " -r"
	}
	commands, err := c.ResolveCommands(node, runtimeCommandCopy, map[string]string{
		"sandbox_name":   shellQuote(node.SandboxName),
		"source_path":    shellQuote(sourcePath),
		"target_path":    shellQuote(targetPath),
		"copy_target":    shellQuote(node.SandboxName + ":" + targetPath),
		"recursive_flag": recursiveFlag,
	})
	if err != nil {
		return err
	}
	_, err = c.runCommands(ctx, 20*time.Minute, "copy files to Lima instance", commands, map[string]any{"sandbox_name": node.SandboxName})
	return err
}

func (c *LimaClient) Shell(ctx context.Context, node Node, command []string, workdir string, interactive bool, streams ShellStreams) error {
	workdirFlag := ""
	if strings.TrimSpace(workdir) != "" {
		workdirFlag = " --workdir " + shellQuote(workdir)
	}
	values := map[string]string{
		"sandbox_name": shellQuote(node.SandboxName),
		"workdir":      shellQuote(workdir),
		"workdir_flag": workdirFlag,
		"command_args": "",
	}
	if len(command) != 0 {
		// Pre-schema-v4 node commands ran as root. Lima logs in as its
		// unprivileged instance user, so preserve CodeLima's shell and
		// bootstrap contract with Lima's passwordless sudo boundary.
		rootCommand := make([]string, 0, len(command)+3)
		rootCommand = append(rootCommand, "sudo", "-H", "--")
		rootCommand = append(rootCommand, command...)
		values["command_args"] = " -- " + shellArgsFragment(rootCommand)
	}
	kind := runtimeCommandShellExec
	if interactive {
		kind = runtimeCommandShellLogin
	}
	commands, err := c.ResolveCommands(node, kind, values)
	if err != nil {
		return err
	}
	for index, commandString := range commands {
		lastInteractive := interactive && index == len(commands)-1
		if lastInteractive {
			if err := runInteractiveCommand(ctx, commandString, streams); err != nil {
				return mapLimaCommandError("open Lima shell", err, nil, map[string]any{"sandbox_name": node.SandboxName})
			}
			continue
		}
		_, stderr, runErr := runShellCommand(ctx, 10*time.Minute, commandString, multiWriter(streams.Stdout, c.Stdout), multiWriter(streams.Stderr, c.Stderr))
		if runErr != nil {
			return mapLimaCommandError("run command in Lima instance", runErr, stderr, map[string]any{"sandbox_name": node.SandboxName, "command_index": index})
		}
	}
	return nil
}

func (c *LimaClient) verifyObservation(ctx context.Context, name string, expected ObservationStatus) error {
	observations, err := c.listDirect(ctx)
	if err != nil {
		return err
	}
	if c.observer != nil {
		c.observer.replace(observations)
	}
	observation, ok := findObservation(observations, name)
	if !ok {
		return notFound("Lima instance not found after runtime operation", map[string]any{"sandbox_name": name})
	}
	if observation.Status != expected {
		return externalCommandFailed("Lima instance did not reach the expected state", fmt.Errorf("found %s, expected %s", observation.Status, expected), map[string]any{"sandbox_name": name})
	}
	return nil
}

func (c *LimaClient) removeCachedObservation(name string) {
	if c.observer != nil {
		c.observer.remove(name)
	}
}

func (c *LimaClient) runCommands(ctx context.Context, timeout time.Duration, action string, commands []string, fields map[string]any) ([]byte, error) {
	var combined bytes.Buffer
	for index, command := range commands {
		stdout, stderr, err := runShellCommand(ctx, timeout, command, nil, c.Stderr)
		if err != nil {
			commandFields := cloneAnyMap(fields)
			commandFields["command_index"] = index
			return nil, mapLimaCommandError(action, err, stderr, commandFields)
		}
		if combined.Len()+len(stdout) > limaOutputLimit {
			return nil, metadataCorruption("limactl output exceeded the safety limit", nil, fields)
		}
		combined.Write(stdout)
		if len(stdout) != 0 && stdout[len(stdout)-1] != '\n' {
			combined.WriteByte('\n')
		}
	}
	return combined.Bytes(), nil
}

func (c *LimaClient) runDirect(ctx context.Context, timeout time.Duration, action string, fields map[string]any, name string, args ...string) ([]byte, error) {
	runCtx, cancel := commandContext(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, name, args...)
	configureCommandCancellation(command)
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = limaOutputLimit, 64*1024
	command.Stdout = &stdout
	command.Stderr = multiWriter(&stderr, c.Stderr)
	err := command.Run()
	if runCtx.Err() != nil {
		return stdout.Bytes(), runCtx.Err()
	}
	if err != nil {
		return stdout.Bytes(), mapLimaCommandError(action, err, stderr.Bytes(), fields)
	}
	return stdout.Bytes(), nil
}

func runShellCommand(ctx context.Context, timeout time.Duration, commandString string, stdoutWriter, stderrWriter io.Writer) ([]byte, []byte, error) {
	runCtx, cancel := commandContext(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, "sh", "-lc", commandString)
	configureCommandCancellation(command)
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = limaOutputLimit, 64*1024
	command.Stdout = multiWriter(&stdout, stdoutWriter)
	command.Stderr = multiWriter(&stderr, stderrWriter)
	err := command.Run()
	if runCtx.Err() != nil {
		return stdout.Bytes(), stderr.Bytes(), runCtx.Err()
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

func runInteractiveCommand(ctx context.Context, commandString string, streams ShellStreams) error {
	command := exec.CommandContext(ctx, "sh", "-lc", commandString)
	// The terminal wrapper is the PTY session leader. Keep the interactive
	// transport in that foreground process group so limactl/ssh can switch the
	// controlling terminal to raw mode and deliver input to the guest. Moving
	// only this child to a new group leaves it in the background: escape keys
	// are echoed locally and Ctrl+C kills the wrapper instead of the guest job.
	command.Stdin, command.Stdout, command.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	err := command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func commandContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func configureCommandCancellation(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 5 * time.Second
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.limit <= 0 {
		return len(data), nil
	}
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(data[:min(remaining, len(data))])
	}
	return len(data), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func mapLimaCommandError(action string, err error, stderr []byte, fields map[string]any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ToLower(strings.TrimSpace(string(stderr)))
	detail := errors.Join(err, errors.New(boundedText(stderr, 4096)))
	switch {
	case errors.Is(err, exec.ErrNotFound), strings.Contains(message, "command not found"), strings.Contains(message, "not found") && strings.Contains(message, "limactl"):
		return dependencyUnavailable(action, detail, fields)
	case strings.Contains(message, "does not exist"), strings.Contains(message, "no such instance"), strings.Contains(message, "not found"):
		return notFound(action, fields)
	case strings.Contains(message, "already exists"), strings.Contains(message, "must be stopped"), strings.Contains(message, "is running"):
		return preconditionFailed(action, fields)
	case strings.Contains(message, "invalid"), strings.Contains(message, "validation"):
		return newAppError(CategoryInvalidArgument, action, ExitInvalidArgument, detail, fields)
	default:
		return externalCommandFailed(action, detail, fields)
	}
}

func boundedText(data []byte, limit int) string {
	if len(data) <= limit {
		return strings.TrimSpace(string(data))
	}
	return strings.TrimSpace(string(data[len(data)-limit:]))
}

func cloneAnyMap(source map[string]any) map[string]any {
	target := make(map[string]any, len(source)+1)
	for key, value := range source {
		target[key] = value
	}
	return target
}

func multiWriter(writers ...io.Writer) io.Writer {
	filtered := make([]io.Writer, 0, len(writers))
	for _, writer := range writers {
		if writer == nil {
			continue
		}
		duplicate := false
		for _, existing := range filtered {
			if sameWriter(existing, writer) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			filtered = append(filtered, writer)
		}
	}
	switch len(filtered) {
	case 0:
		return io.Discard
	case 1:
		return filtered[0]
	default:
		return io.MultiWriter(filtered...)
	}
}

func sameWriter(left, right io.Writer) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftType, rightType := reflect.TypeOf(left), reflect.TypeOf(right)
	return leftType == rightType && leftType.Comparable() && left == right
}

type LimaSSHConfig struct {
	User         string
	Host         string
	Port         int
	IdentityFile string
}

func parseLimaSSHConfig(configPath, limaHome string) (LimaSSHConfig, error) {
	resolvedHome, err := filepath.Abs(filepath.Clean(limaHome))
	if err != nil {
		return LimaSSHConfig{}, err
	}
	resolvedHome, err = filepath.EvalSymlinks(resolvedHome)
	if err != nil {
		return LimaSSHConfig{}, preconditionFailed("LIMA_HOME is unavailable", map[string]any{"path": limaHome})
	}
	resolvedConfig, err := filepath.Abs(filepath.Clean(configPath))
	if err != nil {
		return LimaSSHConfig{}, err
	}
	resolvedConfig, err = filepath.EvalSymlinks(resolvedConfig)
	if err != nil {
		return LimaSSHConfig{}, preconditionFailed("Lima SSH config is unavailable", map[string]any{"path": configPath})
	}
	if !pathWithinRoot(resolvedHome, resolvedConfig) {
		return LimaSSHConfig{}, preconditionFailed("Lima SSH config is outside LIMA_HOME", map[string]any{"path": resolvedConfig})
	}
	info, err := os.Stat(resolvedConfig)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !ownedByCurrentUser(info) {
		return LimaSSHConfig{}, preconditionFailed("Lima SSH config is unavailable or has unsafe permissions", map[string]any{"path": resolvedConfig})
	}
	data, err := os.ReadFile(resolvedConfig)
	if err != nil {
		return LimaSSHConfig{}, err
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		value := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if unquoted, unquoteErr := strconv.Unquote(value); unquoteErr == nil {
			value = unquoted
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return LimaSSHConfig{}, err
	}
	port, err := strconv.Atoi(values["port"])
	if err != nil || port < 1 || port > 65535 {
		return LimaSSHConfig{}, metadataCorruption("Lima SSH config has an invalid port", err, map[string]any{"path": resolvedConfig})
	}
	identity, err := filepath.Abs(filepath.Clean(expandHome(values["identityfile"])))
	if err != nil || !pathWithinRoot(resolvedHome, identity) {
		return LimaSSHConfig{}, preconditionFailed("Lima SSH identity is outside LIMA_HOME", map[string]any{"path": identity})
	}
	identity, err = filepath.EvalSymlinks(identity)
	if err != nil || !pathWithinRoot(resolvedHome, identity) {
		return LimaSSHConfig{}, preconditionFailed("Lima SSH identity is unavailable or outside LIMA_HOME", map[string]any{"path": identity})
	}
	identityInfo, err := os.Stat(identity)
	if err != nil || !identityInfo.Mode().IsRegular() || identityInfo.Mode().Perm()&0o077 != 0 || !ownedByCurrentUser(identityInfo) {
		return LimaSSHConfig{}, preconditionFailed("Lima SSH identity is unavailable or has unsafe permissions", map[string]any{"path": identity})
	}
	host := strings.TrimSpace(values["hostname"])
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return LimaSSHConfig{}, preconditionFailed("Lima SSH endpoint is not loopback-scoped", map[string]any{"host": host})
	}
	user := strings.TrimSpace(values["user"])
	if user == "" {
		return LimaSSHConfig{}, metadataCorruption("Lima SSH config is missing the user", nil, map[string]any{"path": resolvedConfig})
	}
	return LimaSSHConfig{User: user, Host: host, Port: port, IdentityFile: identity}, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

// The observer implementation lives in lima_observer.go. Keeping the cache
// behind this small type lets command-only clients avoid a background process.
type limaObservationService struct {
	mu           sync.RWMutex
	started      bool
	observations map[string]RuntimeObservation
	lastEvent    time.Time
	lastList     time.Time
	restarts     int
	lastError    string
	connected    bool
	cancel       context.CancelFunc
	done         chan struct{}
}

func newLimaObservationService() *limaObservationService {
	return &limaObservationService{observations: map[string]RuntimeObservation{}}
}

func (o *limaObservationService) cached() ([]RuntimeObservation, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.started {
		return nil, false
	}
	return observationMapValues(o.observations), true
}

func (o *limaObservationService) replace(observations []RuntimeObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observations = make(map[string]RuntimeObservation, len(observations))
	for _, observation := range observations {
		o.observations[strings.ToLower(observation.Name)] = observation
	}
	o.lastList = time.Now().UTC()
}

func (o *limaObservationService) remove(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.observations, strings.ToLower(name))
}

func observationMapValues(source map[string]RuntimeObservation) []RuntimeObservation {
	result := make([]RuntimeObservation, 0, len(source))
	for _, observation := range source {
		result = append(result, observation)
	}
	return result
}
