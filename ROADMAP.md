# Roadmap

Status: Draft

This roadmap tracks the current prioritized plan documents for CodeLima. Every linked plan below is a draft.

## Priorities
0.0 [complete]: shorten lima generated vm ids so they align with codelima node names, because the long names that show up in the terminal are too long (e.g. brianrackle@lima-happi-happi-node-019e4c5b should instead be brianrackle@happi-node)
0.1 [complete]: keybind to switch to host terminal and back with Option+Shift+Backtick
0.2 [complete]: Refresh project tree automatically.
0.3 [complete]: Fix issue: Pasting content with newlines removes the newlines.
0.4 [complete]: Fix issue: resizing window often causes terminal contents to clear
0.5 [complete]: red host-machine indicator uses the existing TUI top bar instead of adding another bar
0.6 [complete]: support syncing vm clipboard to host system clipboard
0.7 [superseded]: ghostty cmd + d and cmd + shift + d style TUI split support was removed; terminal tabbing stays tabbing and modified terminal keys no longer create TUI splits
0.8 [complete]: explicit per-node terminal tabs — the TUI starts with one default tab for the initial project or running node, Option+t opens fresh tabs for the focused project or node, Option+Left/Right switch and Option+w close within that item's tabs with adjacent close focus, tabs are scoped to the focused tree item, and selection/visiting never creates additional sessions (F7-F9 and tree `t` fallbacks removed)
0.9 support kitty graphcis protocol so I can get those sweet codex pets.
0.10 bring in and wire up the latest libghostty improvements as demonstrated in https://github.com/ghostty-org/ghostling
0.11 support codelima node renaming through command line (persisting the name to lima as defined in 0.0)
1. Configurable key bindings
   Plan: [KEY_BINDINGS_PLAN.md](/Users/brianrackle/personal/codelima/KEY_BINDINGS_PLAN.md)
2. Sub-project support
   Plan: [SUB_PROJECT_PLAN.md](/Users/brianrackle/personal/codelima/SUB_PROJECT_PLAN.md)
3. Project support for localhost or SSH projects with unmanaged nodes and no workspace management
   Plan: [LOCALHOST_SSH_PROJECTS_PLAN.md](/Users/brianrackle/personal/codelima/LOCALHOST_SSH_PROJECTS_PLAN.md)
4. Configuration overall for worktree support
   Plan: [WORKTREE_SUPPORT_PLAN.md](/Users/brianrackle/personal/codelima/WORKTREE_SUPPORT_PLAN.md)
5. Docker and Firecracker VM or container support
   Plan: [RUNTIME_PROVIDER_PLAN.md](/Users/brianrackle/personal/codelima/RUNTIME_PROVIDER_PLAN.md)
6. Project-level configuration of remote node support
   Plan: [REMOTE_NODE_CONFIGURATION_PLAN.md](/Users/brianrackle/personal/codelima/REMOTE_NODE_CONFIGURATION_PLAN.md)

## Related Draft Plans

- Agent monitoring: [AGENT_MONITORING_PLAN.md](/Users/brianrackle/personal/codelima/AGENT_MONITORING_PLAN.md)
- tmux sidebar frontend: [TMUX_PLAN.md](/Users/brianrackle/personal/codelima/TMUX_PLAN.md)
