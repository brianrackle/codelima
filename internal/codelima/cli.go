package codelima

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

type globalOptions struct {
	Home     string
	JSON     bool
	LogLevel string
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

	if len(rest) == 0 {
		_, _ = fmt.Fprintln(stderr, usage())
		return ExitInvalidArgument
	}

	cfg, err := LoadConfig(options.Home)
	if err != nil {
		writeError(stdout, stderr, options.JSON, err)
		return exitCodeForError(err)
	}

	service := NewService(cfg, nil, stdin, stdout, stderr)
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
		default:
			rest = args[index:]
			return options, rest, nil
		}
	}

	return options, rest, nil
}

func dispatch(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing command group", nil)
	}

	switch args[0] {
	case "doctor":
		return service.Doctor(ctx)
	case "config":
		return dispatchConfig(service, args[1:])
	case "tui":
		return nil, service.TUI(ctx)
	case "project":
		return dispatchProject(ctx, service, args[1:])
	case "node":
		return dispatchNode(ctx, service, args[1:])
	case "patch":
		return dispatchPatch(ctx, service, args[1:])
	case "shell":
		return dispatchShell(ctx, service, args[1:])
	default:
		return nil, invalidArgument("unknown command group", map[string]any{"group": args[0]})
	}
}

func dispatchConfig(service *Service, args []string) (any, error) {
	if len(args) == 0 || args[0] == "show" {
		return service.ConfigSummary(), nil
	}

	return nil, invalidArgument("unknown config command", map[string]any{"command": strings.Join(args, " ")})
}

func dispatchProject(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing project command", nil)
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("project create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		slug := flags.String("slug", "", "")
		workspace := flags.String("workspace", "", "")
		agentProfile := flags.String("agent-profile", "", "")
		template := flags.String("template", "", "")
		cpus := flags.Int("cpus", 0, "")
		memoryGiB := flags.Int("memory-gib", 0, "")
		diskGiB := flags.Int("disk-gib", 0, "")
		var setupCommands stringSliceFlag
		flags.Var(&setupCommands, "setup-command", "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if *workspace == "" {
			return nil, invalidArgument("--workspace is required", nil)
		}
		return service.ProjectCreate(ctx, ProjectCreateInput{
			Slug:          *slug,
			WorkspacePath: *workspace,
			AgentProfile:  *agentProfile,
			SetupCommands: []string(setupCommands),
			Template:      *template,
			Resources:     Resources{CPUs: *cpus, MemoryGiB: *memoryGiB, DiskGiB: *diskGiB},
		})
	case "list":
		flags := flag.NewFlagSet("project list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		includeDeleted := flags.Bool("include-deleted", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.ProjectList(*includeDeleted)
	case "show":
		if len(args) < 2 {
			return nil, invalidArgument("project show requires <project>", nil)
		}
		return service.ProjectShow(args[1])
	case "update":
		flags := flag.NewFlagSet("project update", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		slug := flags.String("slug", "", "")
		workspace := flags.String("workspace", "", "")
		agentProfile := flags.String("agent-profile", "", "")
		template := flags.String("template", "", "")
		clearSetup := flags.Bool("clear-setup-commands", false, "")
		cpus := flags.Int("cpus", 0, "")
		memoryGiB := flags.Int("memory-gib", 0, "")
		diskGiB := flags.Int("disk-gib", 0, "")
		var setupCommands stringSliceFlag
		flags.Var(&setupCommands, "setup-command", "")
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
			return nil, invalidArgument("project update requires <project>", nil)
		}
		var slugPtr, workspacePtr, agentPtr, templatePtr *string
		if *slug != "" {
			slugPtr = slug
		}
		if *workspace != "" {
			workspacePtr = workspace
		}
		if *agentProfile != "" {
			agentPtr = agentProfile
		}
		if *template != "" {
			templatePtr = template
		}
		var resources *Resources
		if *cpus > 0 || *memoryGiB > 0 || *diskGiB > 0 {
			resources = &Resources{CPUs: *cpus, MemoryGiB: *memoryGiB, DiskGiB: *diskGiB}
		}
		return service.ProjectUpdate(target, ProjectUpdateInput{
			Slug:          slugPtr,
			WorkspacePath: workspacePtr,
			AgentProfile:  agentPtr,
			SetupCommands: []string(setupCommands),
			ClearSetup:    *clearSetup,
			Template:      templatePtr,
			Resources:     resources,
		})
	case "delete":
		if len(args) < 2 {
			return nil, invalidArgument("project delete requires <project>", nil)
		}
		return service.ProjectDelete(args[1])
	case "tree":
		flags := flag.NewFlagSet("project tree", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		includeDeleted := flags.Bool("include-deleted", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		root := ""
		if flags.NArg() > 0 {
			root = flags.Arg(0)
		}
		return service.ProjectTree(root, *includeDeleted)
	case "fork":
		flags := flag.NewFlagSet("project fork", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		slug := flags.String("slug", "", "")
		workspace := flags.String("workspace", "", "")
		remaining := args[1:]
		sourceProject := ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			sourceProject = remaining[0]
			remaining = remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if sourceProject == "" && flags.NArg() > 0 {
			sourceProject = flags.Arg(0)
		}
		if sourceProject == "" {
			return nil, invalidArgument("project fork requires <source-project>", nil)
		}
		if *workspace == "" {
			return nil, invalidArgument("--workspace is required", nil)
		}
		return service.ProjectFork(ctx, ProjectForkInput{SourceProject: sourceProject, Slug: *slug, WorkspacePath: *workspace})
	default:
		return nil, invalidArgument("unknown project command", map[string]any{"command": args[0]})
	}
}

func dispatchNode(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing node command", nil)
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("node create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		project := flags.String("project", "", "")
		slug := flags.String("slug", "", "")
		runtime := flags.String("runtime", RuntimeVM, "")
		provider := flags.String("provider", ProviderLima, "")
		agentProfile := flags.String("agent-profile", "", "")
		cpus := flags.Int("cpus", 0, "")
		memoryGiB := flags.Int("memory-gib", 0, "")
		diskGiB := flags.Int("disk-gib", 0, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if *project == "" {
			return nil, invalidArgument("--project is required", nil)
		}
		return service.NodeCreate(ctx, NodeCreateInput{
			Project:      *project,
			Slug:         *slug,
			Runtime:      *runtime,
			Provider:     *provider,
			AgentProfile: *agentProfile,
			Resources:    Resources{CPUs: *cpus, MemoryGiB: *memoryGiB, DiskGiB: *diskGiB},
		})
	case "list":
		flags := flag.NewFlagSet("node list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		includeDeleted := flags.Bool("include-deleted", false, "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.NodeList(*includeDeleted)
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
		projectSlug := flags.String("project-slug", "", "")
		nodeSlug := flags.String("node-slug", "", "")
		workspace := flags.String("workspace", "", "")
		agentProfile := flags.String("agent-profile", "", "")
		cpus := flags.Int("cpus", 0, "")
		memoryGiB := flags.Int("memory-gib", 0, "")
		diskGiB := flags.Int("disk-gib", 0, "")
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
		if *workspace == "" {
			return nil, invalidArgument("--workspace is required", nil)
		}
		project, node, err := service.NodeClone(ctx, NodeCloneInput{
			SourceNode:    sourceNode,
			ProjectSlug:   *projectSlug,
			NodeSlug:      *nodeSlug,
			WorkspacePath: *workspace,
			AgentProfile:  *agentProfile,
			Resources:     Resources{CPUs: *cpus, MemoryGiB: *memoryGiB, DiskGiB: *diskGiB},
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": project, "node": node}, nil
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

func dispatchPatch(ctx context.Context, service *Service, args []string) (any, error) {
	if len(args) == 0 {
		return nil, invalidArgument("missing patch command", nil)
	}

	switch args[0] {
	case "propose":
		flags := flag.NewFlagSet("patch propose", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		sourceProject := flags.String("source", "", "")
		sourceNode := flags.String("source-node", "", "")
		targetProject := flags.String("target", "", "")
		targetNode := flags.String("target-node", "", "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if *sourceProject == "" || *targetProject == "" {
			return nil, invalidArgument("--source and --target are required", nil)
		}
		return service.PatchPropose(ctx, PatchProposeInput{
			SourceProject: *sourceProject,
			SourceNode:    *sourceNode,
			TargetProject: *targetProject,
			TargetNode:    *targetNode,
		})
	case "list":
		flags := flag.NewFlagSet("patch list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		status := flags.String("status", "", "")
		if err := flags.Parse(args[1:]); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		return service.PatchList(*status)
	case "show":
		if len(args) < 2 {
			return nil, invalidArgument("patch show requires <patch>", nil)
		}
		proposal, events, err := service.PatchShow(args[1])
		if err != nil {
			return nil, err
		}
		return map[string]any{"proposal": proposal, "events": events}, nil
	case "approve":
		flags := flag.NewFlagSet("patch approve", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		actor := flags.String("actor", "operator", "")
		note := flags.String("note", "", "")
		remaining := args[1:]
		patchID := ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			patchID = remaining[0]
			remaining = remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if patchID == "" && flags.NArg() > 0 {
			patchID = flags.Arg(0)
		}
		if patchID == "" {
			return nil, invalidArgument("patch approve requires <patch>", nil)
		}
		return service.PatchApprove(patchID, *actor, *note)
	case "apply":
		if len(args) < 2 {
			return nil, invalidArgument("patch apply requires <patch>", nil)
		}
		return service.PatchApply(ctx, args[1])
	case "reject":
		flags := flag.NewFlagSet("patch reject", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		actor := flags.String("actor", "operator", "")
		note := flags.String("note", "", "")
		remaining := args[1:]
		patchID := ""
		if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
			patchID = remaining[0]
			remaining = remaining[1:]
		}
		if err := flags.Parse(remaining); err != nil {
			return nil, invalidArgument(err.Error(), nil)
		}
		if patchID == "" && flags.NArg() > 0 {
			patchID = flags.Arg(0)
		}
		if patchID == "" {
			return nil, invalidArgument("patch reject requires <patch>", nil)
		}
		return service.PatchReject(patchID, *actor, *note)
	default:
		return nil, invalidArgument("unknown patch command", map[string]any{"command": args[0]})
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
	case []Project:
		_, _ = fmt.Fprint(stdout, renderProjectList(data))
	case []Node:
		_, _ = fmt.Fprint(stdout, renderNodeList(data))
	case []ProjectTreeNode:
		_, _ = fmt.Fprint(stdout, renderProjectTree(data, ""))
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

func renderProjectList(projects []Project) string {
	rows := make([][]string, 0, len(projects))
	for _, project := range projects {
		rows = append(rows, []string{
			project.Slug,
			project.ID,
			project.WorkspacePath,
			project.DefaultRuntime,
			project.AgentProfileName,
		})
	}

	return renderTable([]string{"slug", "uuid", "workspace_path", "runtime", "agent"}, rows)
}

func renderNodeList(nodes []Node) string {
	rows := make([][]string, 0, len(nodes))
	for _, node := range nodes {
		rows = append(rows, []string{
			node.Slug,
			node.ID,
			nodeWorkspacePath(node),
			node.Runtime,
			nodeVMStatus(node),
			node.AgentProfileName,
		})
	}

	return renderTable([]string{"slug", "uuid", "workspace_path", "runtime", "vm_status", "agent"}, rows)
}

func nodeWorkspacePath(node Node) string {
	if node.GuestWorkspacePath != "" {
		return node.GuestWorkspacePath
	}

	return node.WorkspaceMountPath
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
  codelima [--home PATH] [--json] [--log-level LEVEL] <group> <command> [flags]

Groups:
  doctor
  config show
  tui
  project create|list|show|update|delete|tree|fork
  node create|list|show|start|stop|clone|delete|status|logs|shell
  patch propose|list|show|approve|apply|reject
  shell <node> [-- command...]
`) + "\n"
}
