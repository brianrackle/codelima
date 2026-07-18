package codelima

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
	"github.com/brianrackle/test_lima/internal/codelima/daemonclient"
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
	if len(args) > 0 && args[0] == sdkSSHServeCommand {
		if err := dispatchSDKSSHServe(ctx, args[1:]); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitCodeForError(err)
		}
		return ExitSuccess
	}

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

func dispatchSDKSSHServe(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet(sdkSSHServeCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sandboxName := flags.String("sandbox", "", "")
	authorizedKeysPath := flags.String("authorized-keys", "", "")
	if err := flags.Parse(args); err != nil {
		return invalidArgument(err.Error(), nil)
	}
	if flags.NArg() != 0 {
		return invalidArgument("unexpected SDK SSH helper arguments", map[string]any{"arguments": flags.Args()})
	}
	if *sandboxName == "" {
		return invalidArgument("--sandbox is required", nil)
	}
	if *authorizedKeysPath == "" {
		return invalidArgument("--authorized-keys is required", nil)
	}
	return sdkSSHServe(ctx, *sandboxName, *authorizedKeysPath)
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

	switch args[0] {
	case "doctor":
		flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		repair := flags.Bool("repair", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.Doctor(ctx, *repair)
	case "settings":
		return dispatchSettings(service, args[1:])
	case "environment":
		return dispatchEnvironment(service, args[1:])
	case "configuration":
		return dispatchConfiguration(service, args[1:])
	case "node":
		return dispatchNode(ctx, service, args[1:])
	case "shell":
		return dispatchShell(ctx, service, args[1:])
	case "daemon":
		return dispatchDaemon(ctx, service, args[1:])
	case "terminal":
		return dispatchTerminal(ctx, service, args[1:])
	default:
		return nil, invalidArgument("unknown command group", map[string]any{"group": args[0]})
	}
}

func isCommandGroup(value string) bool {
	switch value {
	case "doctor", "settings", "environment", "configuration", "node", "shell", "daemon", "terminal", "tui":
		return true
	default:
		return false
	}
}

func dispatchDaemon(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing daemon command", nil)
	}
	switch args[0] {
	case "run":
		return nil, runDaemon(ctx, service)
	case "import":
		flags := flag.NewFlagSet("daemon import", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		handoff := flags.String("handoff", "", "")
		token := flags.String("token", "", "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return nil, importDaemon(ctx, service, *handoff, *token)
	case "start":
		return startDaemon(ctx, service)
	case "stop":
		return stopDaemon(ctx, service)
	case "status":
		return daemonStatus(ctx, service)
	case "snapshot":
		client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version})
		if err != nil {
			return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
		}
		defer func() { _ = client.Close() }()
		var snapshot map[string]any
		if err := client.Call(ctx, "daemon.snapshot", nil, &snapshot); err != nil {
			return nil, fromDaemonError(err)
		}
		return snapshot, nil
	case "update":
		path := ""
		if len(args) > 1 {
			path = args[1]
		}
		client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version, WantInput: true, Timeout: 45 * time.Second})
		if err != nil {
			return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
		}
		defer func() { _ = client.Close() }()
		var result map[string]any
		if err := client.Call(ctx, "daemon.update", map[string]string{"path": path}, &result); err != nil {
			return nil, fromDaemonError(err)
		}
		return result, nil
	default:
		return nil, invalidArgument("unknown daemon command", map[string]any{"command": args[0]})
	}
}

func dispatchTerminal(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing terminal command", nil)
	}
	wantInput := args[0] == "send" || args[0] == "open" || args[0] == "close" || args[0] == "takeover"
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version, WantInput: wantInput})
	if err != nil {
		return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
	}
	defer func() { _ = client.Close() }()
	switch args[0] {
	case "open":
		flags := flag.NewFlagSet("terminal open", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		kind := flags.String("kind", "", "")
		label := flags.String("label", "", "")
		remaining, target := args[1:], ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			target, remaining = remaining[0], remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if target == "" && flags.NArg() > 0 {
			target = flags.Arg(0)
		}
		if target == "" {
			return nil, invalidArgument("terminal open requires <target>", nil)
		}
		resolvedKind := *kind
		if resolvedKind == "" {
			resolvedKind = "node-shell"
		}
		var state daemon.TerminalState
		if err := client.Call(ctx, "terminal.open", map[string]any{"target": target, "kind": resolvedKind, "label": *label}, &state); err != nil {
			return nil, fromDaemonError(err)
		}
		return state, nil
	case "close":
		if len(args) < 2 {
			return nil, invalidArgument("terminal close requires <terminal-id>", nil)
		}
		var result map[string]bool
		if err := client.Call(ctx, "terminal.close", map[string]string{"terminal_id": args[1]}, &result); err != nil {
			return nil, fromDaemonError(err)
		}
		return result, nil
	case "takeover":
		var result map[string]bool
		if err := client.Call(ctx, "input.takeover", nil, &result); err != nil {
			return nil, fromDaemonError(err)
		}
		return result, nil
	case "list":
		var states []daemon.TerminalState
		if err := client.Call(ctx, "terminal.list", nil, &states); err != nil {
			return nil, fromDaemonError(err)
		}
		return states, nil
	case "read":
		flags := flag.NewFlagSet("terminal read", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		source := flags.String("source", "visible", "")
		format := flags.String("format", "text", "")
		remaining, terminalID := args[1:], ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			terminalID, remaining = remaining[0], remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if terminalID == "" && flags.NArg() > 0 {
			terminalID = flags.Arg(0)
		}
		if terminalID == "" {
			return nil, invalidArgument("terminal read requires <terminal-id>", nil)
		}
		var result ReadResult
		if err := client.Call(ctx, "terminal.read", map[string]any{"terminal_id": terminalID, "source": *source, "format": *format}, &result); err != nil {
			return nil, fromDaemonError(err)
		}
		return map[string]any{"terminal_id": terminalID, "text": result.Text, "generation": result.Generation}, nil
	case "send":
		flags := flag.NewFlagSet("terminal send", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		text := flags.String("text", "", "")
		var keys stringSliceFlag
		flags.Var(&keys, "key", "")
		remaining, terminalID := args[1:], ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			terminalID, remaining = remaining[0], remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if terminalID == "" && flags.NArg() > 0 {
			terminalID = flags.Arg(0)
		}
		if terminalID == "" {
			return nil, invalidArgument("terminal send requires <terminal-id>", nil)
		}
		method := "terminal.send_text"
		params := map[string]any{"terminal_id": terminalID, "text": *text}
		if len(keys) > 0 {
			method = "terminal.send_keys"
			params = map[string]any{"terminal_id": terminalID, "keys": []string(keys)}
		}
		var result map[string]int
		if err := client.Call(ctx, method, params, &result); err != nil {
			return nil, fromDaemonError(err)
		}
		return result, nil
	default:
		return nil, invalidArgument("unknown terminal command", map[string]any{"command": args[0]})
	}
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

func dispatchSettings(service *Service, args []string) (any, error) {
	if len(args) == 0 || args[0] == "show" {
		return service.ConfigSummary(), nil
	}

	return nil, invalidArgument("unknown settings command", map[string]any{"command": strings.Join(args, " ")})
}

func dispatchEnvironment(service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing environment command", nil)
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("environment create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		slug := flags.String("slug", "", "")
		var commands stringSliceFlag
		flags.Var(&commands, "bootstrap-command", "")
		flags.Var(&commands, "setup-command", "")
		flags.Var(&commands, "env-command", "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if *slug == "" {
			return nil, invalidArgument("--slug is required", nil)
		}
		return service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
			Slug:              *slug,
			BootstrapCommands: []string(commands),
		})
	case "list":
		flags := flag.NewFlagSet("environment list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		includeDeleted := flags.Bool("include-deleted", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.EnvironmentConfigList(*includeDeleted)
	case "show":
		if len(args) < 2 {
			return nil, invalidArgument("environment show requires <config>", nil)
		}
		return service.EnvironmentConfigShow(args[1])
	case "update":
		flags := flag.NewFlagSet("environment update", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		clearBootstrap := flags.Bool("clear-bootstrap-commands", false, "")
		clearSetup := flags.Bool("clear-setup-commands", false, "")
		clearEnvironment := flags.Bool("clear-env-commands", false, "")
		var commands stringSliceFlag
		flags.Var(&commands, "bootstrap-command", "")
		flags.Var(&commands, "setup-command", "")
		flags.Var(&commands, "env-command", "")
		remaining := args[1:]
		target := ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			target = remaining[0]
			remaining = remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if target == "" && flags.NArg() > 0 {
			target = flags.Arg(0)
		}
		if target == "" {
			return nil, invalidArgument("environment update requires <config>", nil)
		}
		return service.EnvironmentConfigUpdate(target, EnvironmentConfigUpdateInput{
			BootstrapCommands:      []string(commands),
			ClearBootstrapCommands: *clearBootstrap || *clearSetup || *clearEnvironment,
		})
	case "delete":
		if len(args) < 2 {
			return nil, invalidArgument("environment delete requires <config>", nil)
		}
		return service.EnvironmentConfigDelete(args[1])
	default:
		return nil, invalidArgument("unknown environment command", map[string]any{"command": strings.Join(args, " ")})
	}
}

func dispatchConfiguration(service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing configuration command", nil)
	}
	switch args[0] {
	case "create", "update":
		flags := flag.NewFlagSet("configuration "+args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		slug := flags.String("slug", "", "")
		image := flags.String("image", "", "")
		agent := flags.String("agent-profile", "", "")
		vcpus := flags.Uint("vcpus", 0, "")
		memory := flags.String("memory", "", "")
		disk := flags.String("disk", "", "")
		clearEnvironments := flags.Bool("clear-environments", false, "")
		clearBootstrap := flags.Bool("clear-bootstrap-commands", false, "")
		var environments stringSliceFlag
		var bootstrap stringSliceFlag
		flags.Var(&environments, "environment", "")
		flags.Var(&bootstrap, "bootstrap-command", "")
		remaining, target := args[1:], ""
		if args[0] == "update" && len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			target, remaining = remaining[0], remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if args[0] == "update" && target == "" && flags.NArg() > 0 {
			target = flags.Arg(0)
		}
		input, err := configurationInputFromFlags(*slug, *image, *agent, *vcpus, *memory, *disk, environments, bootstrap, *clearEnvironments, *clearBootstrap)
		if err != nil {
			return nil, err
		}
		if args[0] == "create" {
			if *slug == "" {
				return nil, invalidArgument("configuration create requires --slug", nil)
			}
			return service.ConfigurationCreate(input)
		}
		if target == "" {
			return nil, invalidArgument("configuration update requires <configuration>", nil)
		}
		return service.ConfigurationUpdate(target, input)
	case "clone":
		flags := flag.NewFlagSet("configuration clone", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		slug := flags.String("slug", "", "")
		remaining, source := args[1:], ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			source, remaining = remaining[0], remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if source == "" || *slug == "" {
			return nil, invalidArgument("configuration clone requires <source> and --slug", nil)
		}
		return service.ConfigurationClone(ConfigurationCloneInput{Source: source, Slug: *slug})
	case "list":
		flags := flag.NewFlagSet("configuration list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		includeDeleted := flags.Bool("include-deleted", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.ConfigurationList(*includeDeleted)
	case "show":
		if len(args) < 2 {
			return nil, invalidArgument("configuration show requires <configuration>", nil)
		}
		return service.ConfigurationShow(args[1])
	case "delete":
		if len(args) < 2 {
			return nil, invalidArgument("configuration delete requires <configuration>", nil)
		}
		return service.ConfigurationDelete(args[1])
	default:
		return nil, invalidArgument("unknown configuration command", map[string]any{"command": args[0]})
	}
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

func dispatchNode(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing node command", nil)
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("node create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		configuration := flags.String("configuration", DefaultConfigurationSlug, "")
		directory := flags.String("directory", "", "")
		slug := flags.String("slug", "", "")
		workspaceMode := flags.String("workspace-mode", DefaultWorkspaceMode, "")
		netDefault := flags.String("net-default", "", "")
		var ports stringSliceFlag
		var netAllow stringSliceFlag
		flags.Var(&ports, "port", "")
		flags.Var(&netAllow, "net-allow", "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if *slug == "" {
			return nil, invalidArgument("node create requires --slug", nil)
		}
		var netPolicy *NetPolicy
		if *netDefault != "" || netAllow != nil {
			netPolicy = &NetPolicy{Default: coalesce(*netDefault, "deny"), Allow: []string(netAllow)}
		}
		return service.NodeCreate(ctx, NodeCreateInput{
			Configuration: *configuration,
			Directory:     *directory,
			Slug:          *slug,
			WorkspaceMode: *workspaceMode,
			Ports:         []string(ports),
			NetPolicy:     netPolicy,
		})
	case "list":
		flags := flag.NewFlagSet("node list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		includeDeleted := flags.Bool("include-deleted", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.NodeList(ctx, *includeDeleted)
	case "cleanup-incomplete":
		flags := flag.NewFlagSet("node cleanup-incomplete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		apply := flags.Bool("apply", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.NodeCleanupIncomplete(*apply)
	case "show":
		if len(args) < 2 {
			return nil, invalidArgument("node show requires <node>", nil)
		}
		return service.NodeShow(ctx, args[1])
	case "start":
		if len(args) < 2 {
			return nil, invalidArgument("node start requires <node>", nil)
		}
		return service.NodeStart(ctx, args[1])
	case "stop":
		if len(args) < 2 {
			return nil, invalidArgument("node stop requires <node>", nil)
		}
		return service.NodeStop(ctx, args[1])
	case "clone":
		flags := flag.NewFlagSet("node clone", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		nodeSlug := flags.String("slug", "", "")
		remaining := args[1:]
		sourceNode := ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			sourceNode = remaining[0]
			remaining = remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if sourceNode == "" && flags.NArg() > 0 {
			sourceNode = flags.Arg(0)
		}
		if sourceNode == "" {
			return nil, invalidArgument("node clone requires <source-node>", nil)
		}
		if *nodeSlug == "" {
			return nil, invalidArgument("node clone requires --slug", nil)
		}
		node, err := service.NodeClone(ctx, NodeCloneInput{
			SourceNode: sourceNode,
			NodeSlug:   *nodeSlug,
		})
		if err != nil {
			return nil, err
		}
		return node, nil
	case "delete":
		if len(args) < 2 {
			return nil, invalidArgument("node delete requires <node>", nil)
		}
		return service.NodeDelete(ctx, args[1])
	case "status":
		if len(args) < 2 {
			return nil, invalidArgument("node status requires <node>", nil)
		}
		return service.NodeStatus(ctx, args[1])
	case "logs":
		if len(args) < 2 {
			return nil, invalidArgument("node logs requires <node>", nil)
		}
		return service.NodeLogs(args[1])
	case "shell":
		if len(args) < 2 {
			return nil, invalidArgument("node shell requires <node>", nil)
		}
		command := args[2:]
		return nil, service.Shell(ctx, args[1], command)
	default:
		return nil, invalidArgument("unknown node command", map[string]any{"command": args[0]})
	}
}

func dispatchShell(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) < 1 {
		return nil, invalidArgument("shell requires <node>", nil)
	}

	return nil, service.Shell(ctx, args[0], args[1:])
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
	if !As(err, &appErr) {
		appErr = &AppError{Category: "Internal", Message: err.Error(), Code: ExitInternalFailure}
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
			return node.LastRuntimeObservation.Status
		}
		if !node.LastRuntimeObservation.Exists {
			return "missing"
		}
	}

	return node.Status
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

func renderProjectTree(nodes []ProjectTreeNode, prefix string) string {
	var builder strings.Builder
	for index, node := range nodes {
		renderProjectTreeNode(&builder, node, prefix, index == len(nodes)-1)
	}
	return builder.String()
}

func renderProjectTreeNode(builder *strings.Builder, node ProjectTreeNode, prefix string, last bool) {
	connector, nextPrefix := treeConnector(prefix, last)
	builder.WriteString(prefix)
	builder.WriteString(connector)
	builder.WriteString(node.Project.Slug)
	builder.WriteString("\n")

	entries := len(node.Nodes) + len(node.Children)
	entryIndex := 0
	for _, projectNode := range node.Nodes {
		entryIndex++
		renderProjectTreeLeaf(builder, "node: "+projectNode.Slug, nextPrefix, entryIndex == entries)
	}
	for _, child := range node.Children {
		entryIndex++
		renderProjectTreeNode(builder, child, nextPrefix, entryIndex == entries)
	}
}

func renderProjectTreeLeaf(builder *strings.Builder, label string, prefix string, last bool) {
	connector, _ := treeConnector(prefix, last)
	builder.WriteString(prefix)
	builder.WriteString(connector)
	builder.WriteString(label)
	builder.WriteString("\n")
}

func treeConnector(prefix string, last bool) (string, string) {
	connector := "├── "
	nextPrefix := prefix + "│   "
	if last {
		connector = "└── "
		nextPrefix = prefix + "    "
	}

	return connector, nextPrefix
}

func usage() string {
	return strings.TrimSpace(`
Usage:
  codelima [--home PATH] [--json] [--log-level LEVEL] [PATH]
  codelima [--home PATH] [--json] [--log-level LEVEL] <group> <command> [flags]
  codelima --version

Groups:
  doctor [--repair]
  settings show
  environment create|list|show|update|delete
  configuration create|list|show|update|delete|clone
  node create|list|cleanup-incomplete|show|start|stop|clone|delete|status|logs|shell
  shell <node> [-- command...]
  daemon run|start|stop|status|snapshot|update
  terminal open|close|list|read|send|takeover

Running with no command opens the TUI. Passing PATH opens the TUI scoped to nodes in that directory and its descendants.
`) + "\n"
}
