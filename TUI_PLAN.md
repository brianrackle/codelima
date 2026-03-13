# CodeLima TUI Plan

## Summary

CodeLima TUI v1 is a shell-first client over the existing local control plane and shell behavior.
The primary screen is a two-pane layout with a project/node tree on the left and one visible terminal pane on the right.
Only the single-shell view is the chosen direction for v1.

## Chosen Experience

- The TUI remains a client over `codelima shell <node>` rather than introducing a new shell transport.
- The chosen v1 experience is one visible terminal pane for the selected node.
- Chat-first views, shell-as-chat, shell tabs, split-view, broadcast controls, and multi-lane layouts remain exploratory and out of scope for the chosen v1 path.
- The chosen implementation target in `mocks.txt` is the mock labeled `STYLE: CHOSEN`.

## Session Model

- The TUI maintains a live terminal session per opened node while the TUI process is running.
- Switching to a different node reuses that node's preserved session if one exists.
- Selecting a node with no existing session creates a new shell session immediately.
- Preserved state includes scrollback, cursor position, full-screen terminal apps, and partially typed but unsubmitted input.
- Persisting shell state across a TUI crash or restart is out of scope for v1.

## Navigation And Focus

- The left pane is a mouse-enabled project/node tree.
- Clicking a node selects it and switches the visible terminal to that node's session.
- Tree keyboard navigation uses up/down to move selection and left/right to collapse or expand project branches.
- Selecting a node auto-switches the terminal; selecting a project changes tree focus and structure but does not create a shell.
- `Enter` on a selected node focuses the terminal pane for that node.
- `Alt-\`` always returns focus from the terminal pane to the tree.
- `Tab` moves focus from the tree into the terminal pane.
- When the terminal pane is focused, all input is passed through to the shell except the `Alt-\`` escape back to the tree.

## Mock Contract

- `STYLE: CHOSEN` in `mocks.txt` must show a shell-first layout with a left navigation tree and one visible terminal pane.
- The chosen mock must show that node selection auto-switches the terminal session.
- The chosen mock must show mouse support for tree navigation and `Alt-\`` as the terminal escape back to the tree.
- The remaining mock styles and screens stay in the file as exploratory references only and do not override this plan.
