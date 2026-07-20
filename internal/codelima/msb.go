package codelima

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const requiredMicrosandboxVersion = "0.6.6"

var (
	unresolvedRuntimePlaceholderPattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)
	validSandboxNamePattern             = regexp.MustCompile(`^[[:alnum:]][[:alnum:]._ -]*$`)
)

type ShellStreams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type SandboxClient interface {
	Version(ctx context.Context) (string, error)
	ResolveCommands(node Node, kind runtimeCommandKind, values map[string]string) ([]string, error)
	List(ctx context.Context) ([]RuntimeObservation, error)
	Create(ctx context.Context, node Node) error
	Start(ctx context.Context, node Node) error
	Stop(ctx context.Context, node Node) error
	Delete(ctx context.Context, node Node) error
	Clone(ctx context.Context, sourceNode, targetNode Node) error
	CopyToGuest(ctx context.Context, node Node, sourcePath, targetPath string, recursive bool) error
	Shell(ctx context.Context, node Node, command []string, workdir string, interactive bool, streams ShellStreams) error
}

type runtimeCommandKind string

const (
	runtimeCommandVersion              runtimeCommandKind = "version"
	runtimeCommandList                 runtimeCommandKind = "list"
	runtimeCommandCreate               runtimeCommandKind = "create"
	runtimeCommandStart                runtimeCommandKind = "start"
	runtimeCommandStop                 runtimeCommandKind = "stop"
	runtimeCommandDelete               runtimeCommandKind = "delete"
	runtimeCommandClone                runtimeCommandKind = "clone"
	runtimeCommandBootstrap            runtimeCommandKind = "bootstrap"
	runtimeCommandWorkspaceSeedPrepare runtimeCommandKind = "workspace_seed_prepare"
	runtimeCommandCopy                 runtimeCommandKind = "copy"
	runtimeCommandShellExec            runtimeCommandKind = "shell_exec"
	runtimeCommandShellLogin           runtimeCommandKind = "shell_login"
)

func supportedRuntimeCommandKind(kind runtimeCommandKind) bool {
	switch kind {
	case runtimeCommandVersion, runtimeCommandList, runtimeCommandCreate,
		runtimeCommandStart, runtimeCommandStop, runtimeCommandDelete,
		runtimeCommandClone, runtimeCommandBootstrap,
		runtimeCommandWorkspaceSeedPrepare, runtimeCommandCopy,
		runtimeCommandShellExec, runtimeCommandShellLogin:
		return true
	default:
		return false
	}
}

func resolveConfiguredRuntimeCommands(binary string, global RuntimeCommandTemplates, nodeCommands RuntimeCommandTemplates, kind runtimeCommandKind, values map[string]string) ([]string, error) {
	if !supportedRuntimeCommandKind(kind) {
		return nil, invalidArgument("unsupported runtime command kind", map[string]any{"kind": string(kind)})
	}
	if values == nil {
		values = map[string]string{}
	}
	values = cloneMap(values)
	values["binary"] = shellQuote(binary)

	templates := defaultRuntimeCommandTemplates().templates(kind)
	templates = applyDefaultCommandList(global.templates(kind), templates)
	templates = applyDefaultCommandList(nodeCommands.templates(kind), templates)

	resolved := make([]string, 0, len(templates))
	for _, template := range templates {
		command := template
		for key, value := range values {
			command = strings.ReplaceAll(command, "{{"+key+"}}", value)
		}
		if unresolved := unresolvedRuntimePlaceholderPattern.FindString(command); unresolved != "" {
			return nil, invalidArgument("runtime command template contains an unknown placeholder", map[string]any{"placeholder": unresolved, "command": template})
		}
		command = strings.TrimSpace(command)
		if command == "" {
			return nil, invalidArgument("runtime command template must not resolve to an empty command", map[string]any{"kind": string(kind)})
		}
		resolved = append(resolved, command)
	}
	return resolved, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func shellArgsFragment(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func cloneSnapshotName(sandboxName string) string {
	name := "clone-" + sandboxName
	if len(name) > 128 {
		name = name[:128]
	}
	return name
}

func validatePorts(ports []string) ([]string, error) {
	normalized := make([]string, 0, len(ports))
	hostPorts := map[int]struct{}{}
	for _, value := range ports {
		value = strings.TrimSpace(value)
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return nil, invalidArgument("port must use HOST:GUEST numeric form", map[string]any{"port": value})
		}
		host, hostErr := strconv.Atoi(parts[0])
		guest, guestErr := strconv.Atoi(parts[1])
		if hostErr != nil || guestErr != nil || host < 1 || host > 65535 || guest < 1 || guest > 65535 {
			return nil, invalidArgument("port must use HOST:GUEST values from 1 to 65535", map[string]any{"port": value})
		}
		if _, exists := hostPorts[host]; exists {
			return nil, invalidArgument("duplicate host port", map[string]any{"host_port": host})
		}
		hostPorts[host] = struct{}{}
		normalized = append(normalized, fmt.Sprintf("%d:%d", host, guest))
	}
	return normalized, nil
}

func resolvePorts(candidates ...[]string) []string {
	for _, ports := range candidates {
		if ports != nil {
			return slices.Clone(ports)
		}
	}
	return nil
}

func validateSandboxName(name string) error {
	if len(name) == 0 || len(name) > 128 || !validSandboxNamePattern.MatchString(name) || strings.ContainsRune(name, ' ') {
		return invalidArgument("sandbox name must start with an alphanumeric, contain only alphanumeric, dots, hyphens, and underscores, and be at most 128 characters", map[string]any{"sandbox_name": name})
	}
	return nil
}
