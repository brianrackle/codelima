package codelima

// Table-driven CLI dispatch. Every user-facing command is declared once in
// cliCommands with real flag usage strings; runCLICommand implements the one
// shared parsing contract (optional leading positional, then flags, with an
// fs.Arg(0) fallback) that the old dispatchers copy-pasted per command.
// Help is generated from the same table: `codelima <group> --help` and
// `codelima <group> <command> --help` print usage to stdout and succeed, and
// the top-level usage() renders the group index from the table.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

// cliCommand declares one CLI command for the table-driven dispatcher.
type cliCommand struct {
	Group   string // command group, e.g. "node"
	Name    string // subcommand name; "" when the group itself is the command (doctor, shell)
	Summary string // one-line description shown in group help

	Positional string // name of the optional leading positional argument; "" if none
	MinArgs    int    // 1 when the positional is required; enforced after flag parsing

	// PassThrough commands (shell, node shell) receive every argument after
	// the positional verbatim in rest; no flag parsing happens so "-x" and
	// "--" flow through to the guest command untouched.
	PassThrough bool

	// Hidden commands (daemon import) are omitted from the top-level usage
	// group lines but still appear in `codelima <group> --help`.
	Hidden bool

	// Flags registers the command's flags with real usage strings. May be nil.
	Flags func(fs *flag.FlagSet)

	// Run executes the command. positional is "" when absent, fs is the
	// parsed flag set (nil for PassThrough commands), rest holds the
	// remaining non-flag arguments.
	Run func(ctx context.Context, service *Service, positional string, fs *flag.FlagSet, rest []string) (any, error)
}

func (c *cliCommand) fullName() string {
	if c.Name == "" {
		return c.Group
	}
	return c.Group + " " + c.Name
}

// cliGroup declares a command group and its dispatch quirks.
type cliGroup struct {
	Name    string
	Summary string
	// Default is dispatched when no subcommand is given ("" means error).
	Default string
	// JoinUnknownArgs preserves the historical error fields: environment and
	// settings report the full remaining argument list, the rest report only
	// the unknown subcommand token.
	JoinUnknownArgs bool
}

var cliGroups = []cliGroup{
	{Name: "doctor", Summary: "run health checks against the local installation"},
	{Name: "settings", Summary: "inspect the effective configuration", Default: "show", JoinUnknownArgs: true},
	{Name: "environment", Summary: "manage environment configs (reusable bootstrap command sets)", JoinUnknownArgs: true},
	{Name: "configuration", Summary: "manage node configurations (image, resources, environments)"},
	{Name: "node", Summary: "manage sandbox nodes bound to host directories"},
	{Name: "shell", Summary: "open an interactive shell in a node"},
	{Name: "daemon", Summary: "manage the background daemon that owns terminal runtimes"},
	{Name: "terminal", Summary: "interact with daemon-owned terminals"},
}

var cliCommands = []*cliCommand{
	{
		Group:   "doctor",
		Summary: "run health checks against the local installation",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("repair", false, "attempt automatic repair of issues the checks find")
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			return service.Doctor(ctx, flagBool(fs, "repair"))
		},
	},
	{
		Group:   "settings",
		Name:    "show",
		Summary: "print the effective configuration summary",
		Run: func(_ context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.ConfigSummary(), nil
		},
	},

	{
		Group:   "environment",
		Name:    "create",
		Summary: "create an environment config",
		Flags: func(fs *flag.FlagSet) {
			fs.String("slug", "", "unique lowercase identifier for the new environment")
			environmentCommandFlags(fs)
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			slug := flagString(fs, "slug")
			if slug == "" {
				return nil, invalidArgument("--slug is required", nil)
			}
			return service.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{
				Slug:              slug,
				BootstrapCommands: flagStrings(fs, "bootstrap-command"),
			})
		},
	},
	{
		Group:   "environment",
		Name:    "list",
		Summary: "list environment configs",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("include-deleted", false, "include soft-deleted environments in the listing")
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			return service.EnvironmentConfigList(ctx, flagBool(fs, "include-deleted"))
		},
	},
	{
		Group:      "environment",
		Name:       "show",
		Summary:    "show one environment config",
		Positional: "config",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, config string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.EnvironmentConfigShow(ctx, config)
		},
	},
	{
		Group:      "environment",
		Name:       "update",
		Summary:    "replace or clear an environment's bootstrap commands",
		Positional: "config",
		MinArgs:    1,
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("clear-bootstrap-commands", false, "remove every bootstrap command from the environment")
			fs.Bool("clear-setup-commands", false, "alias for --clear-bootstrap-commands")
			fs.Bool("clear-env-commands", false, "alias for --clear-bootstrap-commands")
			environmentCommandFlags(fs)
		},
		Run: func(ctx context.Context, service *Service, config string, fs *flag.FlagSet, _ []string) (any, error) {
			return service.EnvironmentConfigUpdate(ctx, config, EnvironmentConfigUpdateInput{
				BootstrapCommands: flagStrings(fs, "bootstrap-command"),
				ClearBootstrapCommands: flagBool(fs, "clear-bootstrap-commands") ||
					flagBool(fs, "clear-setup-commands") ||
					flagBool(fs, "clear-env-commands"),
			})
		},
	},
	{
		Group:      "environment",
		Name:       "delete",
		Summary:    "soft-delete an environment config",
		Positional: "config",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, config string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.EnvironmentConfigDelete(ctx, config)
		},
	},

	{
		Group:   "configuration",
		Name:    "create",
		Summary: "create a node configuration",
		Flags:   configurationSpecFlags,
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			input, err := configurationInputFromFlagSet(fs)
			if err != nil {
				return nil, err
			}
			if input.Slug == "" {
				return nil, invalidArgument("configuration create requires --slug", nil)
			}
			return service.ConfigurationCreate(ctx, input)
		},
	},
	{
		Group:   "configuration",
		Name:    "list",
		Summary: "list node configurations",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("include-deleted", false, "include soft-deleted configurations in the listing")
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			return service.ConfigurationList(ctx, flagBool(fs, "include-deleted"))
		},
	},
	{
		Group:      "configuration",
		Name:       "show",
		Summary:    "show one node configuration",
		Positional: "configuration",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, configuration string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.ConfigurationShow(ctx, configuration)
		},
	},
	{
		Group:      "configuration",
		Name:       "update",
		Summary:    "update fields on a node configuration",
		Positional: "configuration",
		Flags:      configurationSpecFlags,
		Run: func(ctx context.Context, service *Service, configuration string, fs *flag.FlagSet, _ []string) (any, error) {
			input, err := configurationInputFromFlagSet(fs)
			if err != nil {
				return nil, err
			}
			if configuration == "" {
				return nil, invalidArgument("configuration update requires <configuration>", nil)
			}
			return service.ConfigurationUpdate(ctx, configuration, input)
		},
	},
	{
		Group:      "configuration",
		Name:       "delete",
		Summary:    "soft-delete a node configuration",
		Positional: "configuration",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, configuration string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.ConfigurationDelete(ctx, configuration)
		},
	},
	{
		Group:      "configuration",
		Name:       "clone",
		Summary:    "clone an existing configuration under a new slug",
		Positional: "source",
		Flags: func(fs *flag.FlagSet) {
			fs.String("slug", "", "unique lowercase identifier for the cloned configuration")
		},
		Run: func(ctx context.Context, service *Service, source string, fs *flag.FlagSet, _ []string) (any, error) {
			slug := flagString(fs, "slug")
			if source == "" || slug == "" {
				return nil, invalidArgument("configuration clone requires <source> and --slug", nil)
			}
			return service.ConfigurationClone(ctx, ConfigurationCloneInput{Source: source, Slug: slug})
		},
	},

	{
		Group:   "node",
		Name:    "create",
		Summary: "create a node bound to a host directory",
		Flags: func(fs *flag.FlagSet) {
			fs.String("configuration", DefaultConfigurationSlug, "configuration slug or UUID the node is created from")
			fs.String("directory", "", "host directory the node is bound to (defaults to the current directory)")
			fs.String("slug", "", "unique lowercase identifier for the new node")
			fs.String("workspace-mode", DefaultWorkspaceMode, "how the workspace reaches the node: mounted or copy")
			fs.String("net-default", "", "default network policy for the node: allow or deny")
			ports := &stringSliceFlag{}
			fs.Var(ports, "port", "port forward as HOST:GUEST (repeatable)")
			netAllow := &stringSliceFlag{}
			fs.Var(netAllow, "net-allow", "host reachable when the default network policy is deny (repeatable)")
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			slug := flagString(fs, "slug")
			if slug == "" {
				return nil, invalidArgument("node create requires --slug", nil)
			}
			netDefault := flagString(fs, "net-default")
			netAllow := flagStrings(fs, "net-allow")
			var netPolicy *NetPolicy
			if netDefault != "" || netAllow != nil {
				netPolicy = &NetPolicy{Default: coalesce(netDefault, "deny"), Allow: netAllow}
			}
			return service.NodeCreate(ctx, NodeCreateInput{
				Configuration: flagString(fs, "configuration"),
				Directory:     flagString(fs, "directory"),
				Slug:          slug,
				WorkspaceMode: flagString(fs, "workspace-mode"),
				Ports:         flagStrings(fs, "port"),
				NetPolicy:     netPolicy,
			})
		},
	},
	{
		Group:   "node",
		Name:    "list",
		Summary: "list nodes",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("include-deleted", false, "include soft-deleted nodes in the listing")
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			return service.NodeList(ctx, flagBool(fs, "include-deleted"))
		},
	},
	{
		Group:   "node",
		Name:    "cleanup-incomplete",
		Summary: "remove metadata directories from interrupted node creations",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("apply", false, "delete the incomplete metadata (default is a dry run)")
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			return service.NodeCleanupIncomplete(ctx, flagBool(fs, "apply"))
		},
	},
	{
		Group:      "node",
		Name:       "show",
		Summary:    "show one node",
		Positional: "node",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.NodeShow(ctx, node)
		},
	},
	{
		Group:      "node",
		Name:       "start",
		Summary:    "start a node's sandbox",
		Positional: "node",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.NodeStart(ctx, node)
		},
	},
	{
		Group:      "node",
		Name:       "stop",
		Summary:    "stop a node's sandbox",
		Positional: "node",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.NodeStop(ctx, node)
		},
	},
	{
		Group:      "node",
		Name:       "clone",
		Summary:    "clone a node and its workspace under a new slug",
		Positional: "source-node",
		MinArgs:    1,
		Flags: func(fs *flag.FlagSet) {
			fs.String("slug", "", "unique lowercase identifier for the cloned node")
		},
		Run: func(ctx context.Context, service *Service, sourceNode string, fs *flag.FlagSet, _ []string) (any, error) {
			slug := flagString(fs, "slug")
			if slug == "" {
				return nil, invalidArgument("node clone requires --slug", nil)
			}
			return service.NodeClone(ctx, NodeCloneInput{SourceNode: sourceNode, NodeSlug: slug})
		},
	},
	{
		Group:      "node",
		Name:       "delete",
		Summary:    "soft-delete a node and remove its sandbox",
		Positional: "node",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.NodeDelete(ctx, node)
		},
	},
	{
		Group:      "node",
		Name:       "status",
		Summary:    "report a node's runtime status",
		Positional: "node",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.NodeStatus(ctx, node)
		},
	},
	{
		Group:      "node",
		Name:       "logs",
		Summary:    "print a node's sandbox logs",
		Positional: "node",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, _ []string) (any, error) {
			return service.NodeLogs(ctx, node)
		},
	},
	{
		Group:       "node",
		Name:        "shell",
		Summary:     "run a command (or an interactive shell) inside a node",
		Positional:  "node",
		MinArgs:     1,
		PassThrough: true,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, rest []string) (any, error) {
			return nil, service.Shell(ctx, node, rest)
		},
	},

	{
		Group:       "shell",
		Summary:     "run a command (or an interactive shell) inside a node",
		Positional:  "node",
		MinArgs:     1,
		PassThrough: true,
		Run: func(ctx context.Context, service *Service, node string, _ *flag.FlagSet, rest []string) (any, error) {
			return nil, service.Shell(ctx, node, rest)
		},
	},

	{
		Group:   "daemon",
		Name:    "run",
		Summary: "run the daemon in the foreground",
		Run: func(ctx context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return nil, runDaemon(ctx, service)
		},
	},
	{
		Group:   "daemon",
		Name:    "import",
		Summary: "adopt a live handoff from a retiring daemon (internal)",
		Hidden:  true,
		Flags: func(fs *flag.FlagSet) {
			fs.String("handoff", "", "path to the handoff socket published by the retiring daemon")
			fs.String("token", "", "one-time token authorizing the handoff")
		},
		Run: func(ctx context.Context, service *Service, _ string, fs *flag.FlagSet, _ []string) (any, error) {
			return nil, importDaemon(ctx, service, flagString(fs, "handoff"), flagString(fs, "token"))
		},
	},
	{
		Group:   "daemon",
		Name:    "start",
		Summary: "start the daemon in the background",
		Run: func(ctx context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return startDaemon(ctx, service)
		},
	},
	{
		Group:   "daemon",
		Name:    "stop",
		Summary: "stop the running daemon",
		Run: func(ctx context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return stopDaemon(ctx, service)
		},
	},
	{
		Group:   "daemon",
		Name:    "status",
		Summary: "report the running daemon's version and terminals",
		Run: func(ctx context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return daemonStatus(ctx, service)
		},
	},
	{
		Group:   "daemon",
		Name:    "snapshot",
		Summary: "dump the daemon's full state snapshot",
		Run: func(ctx context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return withDaemonClient(ctx, service, false, func(client *daemonclient.Client) (any, error) {
				var snapshot map[string]any
				if err := client.Call(ctx, "daemon.snapshot", nil, &snapshot); err != nil {
					return nil, fromDaemonError(err)
				}
				return snapshot, nil
			})
		},
	},
	{
		Group:      "daemon",
		Name:       "update",
		Summary:    "hand terminals off to a new daemon binary (defaults to this executable)",
		Positional: "path",
		Run: func(ctx context.Context, service *Service, positional string, _ *flag.FlagSet, _ []string) (any, error) {
			path, err := daemonUpdateBinaryPath(positional)
			if err != nil {
				return nil, invalidArgument("resolve daemon update binary: "+err.Error(), map[string]any{"path": path})
			}
			client, err := dialDaemonUpdateClient(ctx, service)
			if err != nil {
				return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
			}
			defer func() { _ = client.Close() }()
			var result map[string]any
			if err := client.Call(ctx, "daemon.update", map[string]string{"path": path}, &result); err != nil {
				if !isUnsupportedLegacyHandoffTransport(err) {
					return nil, fromDaemonError(err)
				}
				return restartAfterUnsupportedLegacyHandoff(ctx, service, client)
			}
			return result, nil
		},
	},

	{
		Group:      "terminal",
		Name:       "open",
		Summary:    "open a daemon-owned terminal attached to a node",
		Positional: "target",
		MinArgs:    1,
		Flags: func(fs *flag.FlagSet) {
			fs.String("kind", "", "terminal kind to launch (default node-shell)")
			fs.String("label", "", "human-readable label shown in terminal listings")
		},
		Run: func(ctx context.Context, service *Service, target string, fs *flag.FlagSet, _ []string) (any, error) {
			kind := coalesce(flagString(fs, "kind"), "node-shell")
			label := flagString(fs, "label")
			return withDaemonClient(ctx, service, true, func(client *daemonclient.Client) (any, error) {
				var state daemon.TerminalState
				if err := client.Call(ctx, "terminal.open", map[string]any{"target": target, "kind": kind, "label": label}, &state); err != nil {
					return nil, fromDaemonError(err)
				}
				return state, nil
			})
		},
	},
	{
		Group:      "terminal",
		Name:       "close",
		Summary:    "close a daemon-owned terminal",
		Positional: "terminal-id",
		MinArgs:    1,
		Run: func(ctx context.Context, service *Service, terminalID string, _ *flag.FlagSet, _ []string) (any, error) {
			return withDaemonClient(ctx, service, true, func(client *daemonclient.Client) (any, error) {
				var result map[string]bool
				if err := client.Call(ctx, "terminal.close", map[string]string{"terminal_id": terminalID}, &result); err != nil {
					return nil, fromDaemonError(err)
				}
				return result, nil
			})
		},
	},
	{
		Group:   "terminal",
		Name:    "list",
		Summary: "list daemon-owned terminals",
		Run: func(ctx context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return withDaemonClient(ctx, service, false, func(client *daemonclient.Client) (any, error) {
				var states []daemon.TerminalState
				if err := client.Call(ctx, "terminal.list", nil, &states); err != nil {
					return nil, fromDaemonError(err)
				}
				return states, nil
			})
		},
	},
	{
		Group:      "terminal",
		Name:       "read",
		Summary:    "read a terminal's screen contents",
		Positional: "terminal-id",
		MinArgs:    1,
		Flags: func(fs *flag.FlagSet) {
			fs.String("source", "visible", "region to read: visible or scrollback")
			fs.String("format", "text", "output format: text or styled")
		},
		Run: func(ctx context.Context, service *Service, terminalID string, fs *flag.FlagSet, _ []string) (any, error) {
			source := flagString(fs, "source")
			format := flagString(fs, "format")
			return withDaemonClient(ctx, service, false, func(client *daemonclient.Client) (any, error) {
				var result ReadResult
				if err := client.Call(ctx, "terminal.read", map[string]any{"terminal_id": terminalID, "source": source, "format": format}, &result); err != nil {
					return nil, fromDaemonError(err)
				}
				return map[string]any{"terminal_id": terminalID, "text": result.Text, "generation": result.Generation}, nil
			})
		},
	},
	{
		Group:      "terminal",
		Name:       "send",
		Summary:    "send text or key presses to a terminal",
		Positional: "terminal-id",
		MinArgs:    1,
		Flags: func(fs *flag.FlagSet) {
			fs.String("text", "", "literal text to type into the terminal")
			keys := &stringSliceFlag{}
			fs.Var(keys, "key", "named key to press, e.g. Enter or Ctrl+C (repeatable, overrides --text)")
		},
		Run: func(ctx context.Context, service *Service, terminalID string, fs *flag.FlagSet, _ []string) (any, error) {
			keys := flagStrings(fs, "key")
			method := "terminal.send_text"
			params := map[string]any{"terminal_id": terminalID, "text": flagString(fs, "text")}
			if len(keys) > 0 {
				method = "terminal.send_keys"
				params = map[string]any{"terminal_id": terminalID, "keys": keys}
			}
			return withDaemonClient(ctx, service, true, func(client *daemonclient.Client) (any, error) {
				var result map[string]int
				if err := client.Call(ctx, method, params, &result); err != nil {
					return nil, fromDaemonError(err)
				}
				return result, nil
			})
		},
	},
	{
		Group:   "terminal",
		Name:    "takeover",
		Summary: "claim daemon input ownership for this client",
		Run: func(ctx context.Context, service *Service, _ string, _ *flag.FlagSet, _ []string) (any, error) {
			return withDaemonClient(ctx, service, true, func(client *daemonclient.Client) (any, error) {
				var result map[string]bool
				if err := client.Call(ctx, "input.takeover", nil, &result); err != nil {
					return nil, fromDaemonError(err)
				}
				return result, nil
			})
		},
	},
}

func environmentCommandFlags(fs *flag.FlagSet) {
	commands := &stringSliceFlag{}
	fs.Var(commands, "bootstrap-command", "command run once while bootstrapping a node (repeatable)")
	fs.Var(commands, "setup-command", "alias for --bootstrap-command")
	fs.Var(commands, "env-command", "alias for --bootstrap-command")
}

func configurationSpecFlags(fs *flag.FlagSet) {
	fs.String("slug", "", "unique lowercase identifier for the configuration")
	fs.String("image", "", "sandbox image reference nodes boot from")
	fs.String("agent-profile", "", "agent profile installed into nodes")
	fs.Uint("vcpus", 0, "virtual CPU count for nodes (1-255)")
	fs.String("memory", "", "memory size, e.g. 4096MiB or 8GiB")
	fs.String("disk", "", "disk size, e.g. 20480MiB or 40GiB")
	fs.Bool("clear-environments", false, "remove every environment from the configuration")
	fs.Bool("clear-bootstrap-commands", false, "remove every bootstrap command from the configuration")
	environments := &stringSliceFlag{}
	fs.Var(environments, "environment", "environment config slug to attach (repeatable)")
	bootstrap := &stringSliceFlag{}
	fs.Var(bootstrap, "bootstrap-command", "command run once while bootstrapping a node (repeatable)")
}

func configurationInputFromFlagSet(fs *flag.FlagSet) (ConfigurationCreateInput, error) {
	return configurationInputFromFlags(
		flagString(fs, "slug"),
		flagString(fs, "image"),
		flagString(fs, "agent-profile"),
		flagUint(fs, "vcpus"),
		flagString(fs, "memory"),
		flagString(fs, "disk"),
		stringSliceFlag(flagStrings(fs, "environment")),
		stringSliceFlag(flagStrings(fs, "bootstrap-command")),
		flagBool(fs, "clear-environments"),
		flagBool(fs, "clear-bootstrap-commands"),
	)
}

// withDaemonClient dials the daemon and runs fn with the connected client,
// preserving the historical "daemon not running" dependency error.
func withDaemonClient(ctx context.Context, service *Service, wantInput bool, fn func(client *daemonclient.Client) (any, error)) (any, error) {
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version, WantInput: wantInput})
	if err != nil {
		return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
	}
	defer func() { _ = client.Close() }()
	return fn(client)
}

func findCLIGroup(name string) *cliGroup {
	for i := range cliGroups {
		if cliGroups[i].Name == name {
			return &cliGroups[i]
		}
	}
	return nil
}

func findCLICommand(group, name string) *cliCommand {
	for _, cmd := range cliCommands {
		if cmd.Group == group && cmd.Name == name {
			return cmd
		}
	}
	return nil
}

func commandsForGroup(group string) []*cliCommand {
	var commands []*cliCommand
	for _, cmd := range cliCommands {
		if cmd.Group == group {
			commands = append(commands, cmd)
		}
	}
	return commands
}

func isHelpArgument(value string) bool {
	return value == "--help" || value == "-h"
}

// dispatchGroup routes group-relative arguments to a table command, handling
// group help, the settings-style default command, and the historical
// missing/unknown command errors.
func dispatchGroup(ctx context.Context, service *Service, group *cliGroup, args []string) (any, error) {
	if bare := findCLICommand(group.Name, ""); bare != nil {
		return runCLICommand(ctx, service, bare, args)
	}
	if len(args) > 0 && isHelpArgument(args[0]) {
		_, _ = fmt.Fprint(service.stdout, groupHelp(group))
		return nil, nil
	}
	name, rest := group.Default, args
	if len(args) > 0 {
		name, rest = args[0], args[1:]
	}
	if name == "" {
		return nil, invalidArgument("missing "+group.Name+" command", nil)
	}
	cmd := findCLICommand(group.Name, name)
	if cmd == nil {
		detail := name
		if group.JoinUnknownArgs {
			detail = strings.Join(args, " ")
		}
		return nil, invalidArgument("unknown "+group.Name+" command", map[string]any{"command": detail})
	}
	return runCLICommand(ctx, service, cmd, rest)
}

// runCLICommand is the one shared argument parser: an optional leading
// positional (a first argument not starting with "-"), then flags, with a
// post-parse fs.Arg(0) fallback for the positional. -h/--help prints the
// generated command usage to stdout and succeeds.
func runCLICommand(ctx context.Context, service *Service, cmd *cliCommand, args []string) (any, error) {
	if cmd.PassThrough {
		if len(args) > 0 && isHelpArgument(args[0]) {
			_, _ = fmt.Fprint(service.stdout, commandHelp(cmd))
			return nil, nil
		}
		positional, rest := "", args
		if len(rest) > 0 {
			positional, rest = rest[0], rest[1:]
		}
		if cmd.MinArgs > 0 && positional == "" {
			return nil, invalidArgument(cmd.fullName()+" requires <"+cmd.Positional+">", nil)
		}
		return cmd.Run(ctx, service, positional, nil, rest)
	}

	fs := newCommandFlagSet(cmd)
	positional, rest := "", args
	if cmd.Positional != "" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		positional, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(service.stdout, commandHelp(cmd))
			return nil, nil
		}
		return nil, invalidArgument(err.Error(), nil)
	}
	if cmd.Positional != "" && positional == "" && fs.NArg() > 0 {
		positional = fs.Arg(0)
	}
	if cmd.MinArgs > 0 && positional == "" {
		return nil, invalidArgument(cmd.fullName()+" requires <"+cmd.Positional+">", nil)
	}
	return cmd.Run(ctx, service, positional, fs, fs.Args())
}

func newCommandFlagSet(cmd *cliCommand) *flag.FlagSet {
	fs := flag.NewFlagSet(cmd.fullName(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	return fs
}

// Flag accessors for Run bodies. Registration and lookup live in the same
// table entry, so a missing name is a programming error caught by tests.
func flagBool(fs *flag.FlagSet, name string) bool {
	return fs.Lookup(name).Value.(flag.Getter).Get().(bool)
}

func flagString(fs *flag.FlagSet, name string) string {
	return fs.Lookup(name).Value.(flag.Getter).Get().(string)
}

func flagUint(fs *flag.FlagSet, name string) uint {
	return fs.Lookup(name).Value.(flag.Getter).Get().(uint)
}

func flagStrings(fs *flag.FlagSet, name string) []string {
	return []string(*fs.Lookup(name).Value.(*stringSliceFlag))
}

func commandHasFlags(cmd *cliCommand) bool {
	if cmd.Flags == nil {
		return false
	}
	count := 0
	newCommandFlagSet(cmd).VisitAll(func(*flag.Flag) { count++ })
	return count > 0
}

func commandUsageLine(cmd *cliCommand) string {
	var b strings.Builder
	b.WriteString("codelima " + cmd.fullName())
	if cmd.Positional != "" {
		if cmd.MinArgs > 0 {
			b.WriteString(" <" + cmd.Positional + ">")
		} else {
			b.WriteString(" [<" + cmd.Positional + ">]")
		}
	}
	if cmd.PassThrough {
		b.WriteString(" [-- command...]")
	} else if commandHasFlags(cmd) {
		b.WriteString(" [flags]")
	}
	return b.String()
}

func commandHelp(cmd *cliCommand) string {
	var b strings.Builder
	b.WriteString("Usage:\n  " + commandUsageLine(cmd) + "\n")
	if cmd.Summary != "" {
		b.WriteString("\n" + cmd.Summary + "\n")
	}
	if commandHasFlags(cmd) {
		b.WriteString("\nFlags:\n")
		fs := newCommandFlagSet(cmd)
		fs.SetOutput(&b)
		fs.PrintDefaults()
	}
	return b.String()
}

func groupHelp(group *cliGroup) string {
	if bare := findCLICommand(group.Name, ""); bare != nil {
		return commandHelp(bare)
	}
	var b strings.Builder
	b.WriteString("Usage:\n  codelima " + group.Name + " <command> [flags]\n")
	if group.Summary != "" {
		b.WriteString("\n" + group.Summary + "\n")
	}
	b.WriteString("\nCommands:\n")
	for _, cmd := range commandsForGroup(group.Name) {
		b.WriteString(fmt.Sprintf("  %-20s %s\n", cmd.Name, cmd.Summary))
	}
	b.WriteString("\nRun 'codelima " + group.Name + " <command> --help' for command flags.\n")
	return b.String()
}

func groupUsageLine(group *cliGroup) string {
	if bare := findCLICommand(group.Name, ""); bare != nil {
		line := group.Name
		if bare.Positional != "" {
			line += " <" + bare.Positional + ">"
		}
		if bare.PassThrough {
			line += " [-- command...]"
		} else {
			newCommandFlagSet(bare).VisitAll(func(f *flag.Flag) {
				line += " [--" + f.Name + "]"
			})
		}
		return line
	}
	names := make([]string, 0, 8)
	for _, cmd := range commandsForGroup(group.Name) {
		if cmd.Hidden {
			continue
		}
		names = append(names, cmd.Name)
	}
	return group.Name + " " + strings.Join(names, "|")
}

func usage() string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	b.WriteString("  codelima [--home PATH] [--json] [--log-level LEVEL] [PATH]\n")
	b.WriteString("  codelima [--home PATH] [--json] [--log-level LEVEL] <group> <command> [flags]\n")
	b.WriteString("  codelima --version\n")
	b.WriteString("\nGroups:\n")
	for i := range cliGroups {
		b.WriteString("  " + groupUsageLine(&cliGroups[i]) + "\n")
	}
	b.WriteString("\nRunning with no command opens the TUI. Passing PATH opens the TUI scoped to nodes in that directory and its descendants.\n")
	b.WriteString("Run 'codelima <group> --help' for group commands and 'codelima <group> <command> --help' for flags.\n")
	return b.String()
}
