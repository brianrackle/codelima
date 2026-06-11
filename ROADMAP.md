# Roadmap

Status: Draft

This roadmap tracks the current prioritized plan documents for CodeLima. Every linked plan below is a draft.

## Priorities
0.0 [complete]: shorten lima generated vm ids so they align with codelima node names, because the long names that show up in the terminal are too long (e.g. brianrackle@lima-happi-happi-node-019e4c5b should instead be brianrackle@happi-node)
0.1 [complete]: keybind to switch to host terminal and back with Option+Shift+Backtick
0.2: Refresh project tree automatically.
0.3: Fix issue: Pasting content with newlines removes the newlines.
0.4: Fix issue: resizing window often causes terminal contents to clear
0.5: red line at top when in host machine
0.6: support syncing vm clipboard to host system clipboard
0.7: ghostty cmd + d and cmd + shift + d style support to create pane in ghostty with the current codelima terminal open

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
