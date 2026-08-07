package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

type globalOptions struct {
	Home     string
	JSON     bool
	LogLevel string
	Help     bool
	Version  bool
}

type stringSliceFlag []string

func (f *stringSliceFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringSliceFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, rest, err := parseGlobalOptions(args)
	if err != nil {
		writeError(stdout, stderr, true, err)
		return exitCodeForError(err)
	}

	if options.Help {
		_, _ = fmt.Fprint(stdout, usage())
		return ExitSuccess
	}
	if options.Version {
		_, _ = fmt.Fprintln(stdout, Version)
		return ExitSuccess
	}

	cfg, err := LoadConfig(options.Home)
	if err != nil {
		writeError(stdout, stderr, options.JSON, err)
		return exitCodeForError(err)
	}

	// Wire the previously-parsed-but-ignored --log-level flag into a live
	// logger. CLI commands log structured text to stderr at the chosen level;
	// TUI mode swaps this for a file sink via enableFileLogging (ADR 59).
	level := parseLogLevel(options.LogLevel)
	service := NewService(cfg, nil, stdin, stdout, stderr)
	service.SetLogger(newTextLogger(stderr, level), level)
	result, err := dispatch(ctx, service, rest)
	if err != nil {
		writeError(stdout, stderr, options.JSON, err)
		return exitCodeForError(err)
	}

	if result != nil {
		writeSuccess(stdout, options.JSON, result)
	}

	return ExitSuccess
}

func parseGlobalOptions(args []string) (globalOptions, []string, error) {
	options := globalOptions{LogLevel: "info"}
	rest := []string{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--home":
			index++
			if index >= len(args) {
				return globalOptions{}, nil, invalidArgument("--home requires a path", nil)
			}
			options.Home = args[index]
		case "--json":
			options.JSON = true
		case "--log-level":
			index++
			if index >= len(args) {
				return globalOptions{}, nil, invalidArgument("--log-level requires a value", nil)
			}
			options.LogLevel = args[index]
		case "--help", "-h":
			options.Help = true
		case "--version":
			options.Version = true
		default:
			rest = args[index:]
			return options, rest, nil
		}
	}

	return options, rest, nil
}

func dispatch(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, service.TUI(ctx, "")
	}

	if len(args) == 1 && !isCommandGroup(args[0]) {
		workspaceRoot, ok, err := tuiWorkspaceRootArgument(args[0])
		if err != nil {
			return nil, err
		}
		if ok {
			return nil, service.TUI(ctx, workspaceRoot)
		}
	}

	group := findCLIGroup(args[0])
	if group == nil {
		return nil, invalidArgument("unknown command group", map[string]any{"group": args[0]})
	}
	return dispatchGroup(ctx, service, group, args[1:])
}

func isCommandGroup(value string) bool {
	// "tui" stays reserved so a directory named tui never opens the TUI.
	return value == "tui" || findCLIGroup(value) != nil
}

func tuiWorkspaceRootArgument(value string) (string, bool, error) {
	candidate := expandHome(value)
	info, err := os.Stat(candidate)
	if err == nil {
		if !info.IsDir() {
			return "", true, invalidArgument("tui path must be a directory", map[string]any{"path": value})
		}
		workspaceRoot, err := canonicalPath(value)
		if err != nil {
			return "", true, invalidArgument("tui path must be resolvable", map[string]any{"path": value})
		}
		return workspaceRoot, true, nil
	}

	if !looksLikePathArgument(value) {
		return "", false, nil
	}

	if os.IsNotExist(err) {
		return "", true, invalidArgument("tui path does not exist", map[string]any{"path": value})
	}
	return "", true, err
}

func looksLikePathArgument(value string) bool {
	return strings.HasPrefix(value, ".") ||
		strings.HasPrefix(value, "~") ||
		filepath.IsAbs(value) ||
		strings.ContainsRune(value, os.PathSeparator)
}

func configurationInputFromFlags(slug, image, agent string, vcpus uint, memory, disk string, environments, bootstrap stringSliceFlag, clearEnvironments, clearBootstrap bool) (ConfigurationCreateInput, error) {
	input := ConfigurationCreateInput{Slug: slug}
	if image != "" {
		input.Image = &image
	}
	if agent != "" {
		input.AgentProfile = &agent
	}
	if vcpus > 255 {
		return ConfigurationCreateInput{}, invalidArgument("vcpus must be between 1 and 255", map[string]any{"vcpus": vcpus})
	}
	if vcpus != 0 {
		value := uint8(vcpus)
		input.VCPUs = &value
	}
	if memory != "" {
		value, err := parseSizeMiB(memory)
		if err != nil {
			return ConfigurationCreateInput{}, invalidArgument("memory must be a positive MiB or GiB value", map[string]any{"memory": memory})
		}
		input.MemoryMiB = &value
	}
	if disk != "" {
		value, err := parseSizeMiB(disk)
		if err != nil {
			return ConfigurationCreateInput{}, invalidArgument("disk must be a positive MiB or GiB value", map[string]any{"disk": disk})
		}
		input.DiskMiB = &value
	}
	if clearEnvironments {
		input.Environments = []string{}
	} else if environments != nil {
		input.Environments = []string(environments)
	}
	if clearBootstrap {
		input.BootstrapCommands = []string{}
	} else if bootstrap != nil {
		input.BootstrapCommands = []string(bootstrap)
	}
	return input, nil
}

func parseSizeMiB(input string) (uint32, error) {
	value := strings.TrimSpace(strings.ToLower(input))
	multiplier := uint64(1)
	switch {
	case strings.HasSuffix(value, "gib"):
		multiplier = 1024
		value = strings.TrimSpace(strings.TrimSuffix(value, "gib"))
	case strings.HasSuffix(value, "gb"):
		multiplier = 1024
		value = strings.TrimSpace(strings.TrimSuffix(value, "gb"))
	case strings.HasSuffix(value, "mib"):
		value = strings.TrimSpace(strings.TrimSuffix(value, "mib"))
	case strings.HasSuffix(value, "mb"):
		value = strings.TrimSpace(strings.TrimSuffix(value, "mb"))
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 || parsed*multiplier > uint64(^uint32(0)) {
		return 0, fmt.Errorf("invalid size")
	}
	return uint32(parsed * multiplier), nil
}

func writeSuccess(stdout io.Writer, asJSON bool, value any) {
	if asJSON {
		payload, _ := json.MarshalIndent(map[string]any{"ok": true, "data": value}, "", "  ")
		_, _ = stdout.Write(append(payload, '\n'))
		return
	}

	switch data := value.(type) {
	case []Configuration:
		_, _ = fmt.Fprint(stdout, renderConfigurationList(data))
	case []EnvironmentConfig:
		_, _ = fmt.Fprint(stdout, renderEnvironmentConfigList(data))
	case []Node:
		_, _ = fmt.Fprint(stdout, renderNodeList(data))
	case IncompleteNodeCleanupResult:
		_, _ = fmt.Fprint(stdout, renderIncompleteNodeCleanupResult(data))
	case DoctorReport:
		for _, check := range data.Checks {
			_, _ = fmt.Fprintf(stdout, "[%s] %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message)
		}
		for _, warning := range data.Warnings {
			_, _ = fmt.Fprintf(stdout, "warning: %s\n", warning)
		}
	default:
		payload, _ := yaml.Marshal(value)
		_, _ = stdout.Write(payload)
	}
}

func writeError(stdout, stderr io.Writer, asJSON bool, err error) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		appErr = &AppError{Category: CategoryInternal, Message: err.Error(), Code: ExitInternalFailure}
	}

	if asJSON {
		payload, _ := json.MarshalIndent(map[string]any{
			"ok": false,
			"error": map[string]any{
				"category": appErr.Category,
				"message":  appErr.Message,
				"fields":   appErr.Fields,
			},
		}, "", "  ")
		_, _ = stdout.Write(append(payload, '\n'))
		return
	}

	_, _ = fmt.Fprintf(stderr, "%s: %s\n", appErr.Category, appErr.Message)
}

func renderConfigurationList(configurations []Configuration) string {
	rows := make([][]string, 0, len(configurations))
	for _, configuration := range configurations {
		rows = append(rows, []string{
			configuration.Slug,
			configuration.ID,
			configuration.Image,
			configuration.AgentProfileName,
			strconv.Itoa(int(configuration.VCPUs)),
			strconv.FormatUint(uint64(configuration.MemoryMiB), 10),
			strconv.FormatUint(uint64(configuration.DiskMiB), 10),
		})
	}

	return renderTable([]string{"slug", "uuid", "image", "agent", "vcpus", "memory_mib", "disk_mib"}, rows)
}

func renderNodeList(nodes []Node) string {
	rows := make([][]string, 0, len(nodes))
	for _, node := range nodes {
		rows = append(rows, []string{
			node.Slug,
			node.ID,
			node.ConfigurationSlug,
			node.DirectoryPath,
			nodeWorkspaceMode(node),
			node.Runtime,
			nodeVMStatus(node),
			node.AgentProfileName,
		})
	}

	return renderTable([]string{"slug", "uuid", "configuration", "directory", "workspace_mode", "runtime", "vm_status", "agent"}, rows)
}

func renderEnvironmentConfigList(configs []EnvironmentConfig) string {
	rows := make([][]string, 0, len(configs))
	for _, config := range configs {
		rows = append(rows, []string{
			config.Slug,
			config.ID,
			strconv.Itoa(len(config.BootstrapCommands)),
		})
	}

	return renderTable([]string{"slug", "uuid", "command_count"}, rows)
}

func renderIncompleteNodeCleanupResult(result IncompleteNodeCleanupResult) string {
	rows := make([][]string, 0, len(result.Items))
	action := "would_remove"
	if !result.DryRun {
		action = "removed"
	}

	for _, item := range result.Items {
		rows = append(rows, []string{
			item.NodeID,
			coalesce(item.SandboxName, "-"),
			action,
		})
	}

	if len(rows) == 0 {
		if result.DryRun {
			return "no incomplete node metadata directories found\n"
		}
		return "removed 0 incomplete node metadata directories\n"
	}

	return renderTable([]string{"node_dir", "sandbox_name", "action"}, rows)
}

func nodeVMStatus(node Node) string {
	if node.LastRuntimeObservation != nil {
		if node.LastRuntimeObservation.Status != "" {
			return string(node.LastRuntimeObservation.Status)
		}
		if !node.LastRuntimeObservation.Exists {
			return "missing"
		}
	}

	return string(node.Status)
}

func renderTable(headers []string, rows [][]string) string {
	var builder strings.Builder
	writer := tabwriter.NewWriter(&builder, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, strings.Join(headers, "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(writer, strings.Join(row, "\t"))
	}
	_ = writer.Flush()
	return builder.String()
}
