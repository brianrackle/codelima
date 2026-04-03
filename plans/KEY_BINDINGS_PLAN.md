# Key Bindings Plan

Status: Draft

## Goal

Make TUI key bindings configurable through `CODELIMA_HOME/_config/config.yaml` with support for multiple bindings per action, while preserving the current default behavior out of the box.

This plan covers:

- tree and global TUI actions
- focus and quit bindings
- dialog, selector, and menu bindings
- help/footer rendering for configured bindings

## Requirements

- key binding configuration must live in `CODELIMA_HOME/_config/config.yaml`
- current default bindings must be written into that file by default
- the TUI must load bindings from config instead of hardcoding them in logic
- an action may have multiple bindings
- bindings must be validated at config load time

## Current State

The current TUI mixes three binding styles:

- top-level project and node actions are hardcoded as a single `Hotkey rune` in `availableTUIActions`
- terminal focus toggle and quit are hardcoded separately in helper functions
- dialogs, selectors, and menus each handle their own fixed key matching inline

Examples in the current code:

- `project.create` -> `a`
- `environment_config.manage` -> `g`
- `node.start` and `node.stop` -> `s`
- terminal focus toggle -> `Alt+\`` or `F6`
- quit -> `q` or `Ctrl+C`
- overlay cancel -> `Esc` or `Ctrl+[`

The current design is not sufficient for configurable multi-binding support because:

- `tuiActionSpec` stores only one `Hotkey rune`
- `tuiMenuEntry` stores only one `Key rune`
- footer and help text assume a single displayed binding per action in most places

## Proposed Config Shape

Add a root-level `key_bindings` section to `_config/config.yaml`.

Recommended YAML shape:

```yaml
key_bindings:
  tui:
    app.quit:
      - q
      - ctrl+c
    focus.toggle_terminal:
      - alt+`
      - f6
    tree.move_up:
      - up
    tree.move_down:
      - down
    tree.collapse:
      - left
    tree.expand:
      - right
    project.create:
      - a
    environment_config.manage:
      - g
    project.create_node:
      - n
    project.update:
      - u
    project.delete:
      - x
    node.start:
      - s
    node.stop:
      - s
    node.delete:
      - d
    node.clone:
      - c
    dialog.cancel:
      - esc
      - ctrl+[
    dialog.submit:
      - enter
      - ctrl+s
    dialog.next_field:
      - tab
      - down
    dialog.prev_field:
      - up
    dialog.activate_field:
      - right
    selector.cancel:
      - esc
      - ctrl+[
    selector.move_next:
      - tab
      - down
    selector.move_prev:
      - up
    selector.confirm:
      - enter
    selector.toggle:
      - space
    selector.clear:
      - ctrl+u
    menu.cancel:
      - esc
      - ctrl+[
```

This shape has three useful properties:

- bindings are attached to stable action IDs
- actions can have one or many bindings
- defaults can be serialized into `config.yaml` instead of living only in code

## Stable Action IDs

Binding lookup should depend on stable string IDs, not on labels or view-specific help text.

Recommended initial IDs:

- `app.quit`
- `focus.toggle_terminal`
- `tree.move_up`
- `tree.move_down`
- `tree.collapse`
- `tree.expand`
- `project.create`
- `environment_config.manage`
- `project.create_node`
- `project.update`
- `project.delete`
- `node.start`
- `node.stop`
- `node.delete`
- `node.clone`
- `dialog.cancel`
- `dialog.submit`
- `dialog.next_field`
- `dialog.prev_field`
- `dialog.activate_field`
- `selector.cancel`
- `selector.move_next`
- `selector.move_prev`
- `selector.confirm`
- `selector.toggle`
- `selector.clear`
- `menu.cancel`

Menu entries that currently use ad hoc single-letter keys should eventually be upgraded from `Key rune` to a stable action ID plus resolved bindings.

## Binding Syntax

Bindings in YAML should use a small canonical string syntax:

- printable keys: `a`, `g`, `` ` ``
- arrows: `up`, `down`, `left`, `right`
- navigation keys: `tab`, `enter`, `esc`, `space`
- function keys: `f6`
- modifier combinations: `ctrl+c`, `ctrl+s`, `ctrl+[`, `alt+``

Normalization rules:

- case-insensitive on load
- stored and rendered in canonical lowercase form
- reject unknown key names early

This should be parsed into a small internal binding type rather than matched from raw strings repeatedly.

## Data Model Changes

Add key binding fields to `Config`.

Recommended shape:

```go
type Config struct {
    ...
    KeyBindings KeyBindingConfig `json:"key_bindings" yaml:"key_bindings"`
}

type KeyBindingConfig struct {
    TUI map[string][]string `json:"tui" yaml:"tui"`
}
```

Then add a normalized runtime form used by the TUI:

```go
type KeyMap struct {
    TUI map[string][]KeyBinding
}
```

Where `KeyBinding` is a parsed binding descriptor suitable for matching against `vaxis.Key`.

## Default Loading Behavior

The default bindings should be generated in code as a default config value, but they should be materialized into `_config/config.yaml` the same way default Lima commands are materialized today.

That means:

- `DefaultConfig()` includes default key bindings
- `defaultConfigYAML()` writes them into the file
- `LoadConfig()` applies missing defaults when older config files omit the section
- `EnsureLayout()` backfills the section into existing `config.yaml` files

The important rule is:

- code defines the defaults
- config persists the effective defaults
- runtime behavior reads from config

## Resolution Rules

Binding resolution should be context-aware.

Examples:

- `node.start` and `node.stop` may both use `s` because they are never available at the same time for the same node
- `project.create` and `project.delete` must not share the same binding because both are active on a selected project
- `dialog.submit` using `enter` is valid because it is resolved only while a dialog is active

Recommended validation model:

- define binding scopes
- reject collisions only within overlapping scopes

Suggested scopes:

- `tree.global`
- `tree.project`
- `tree.node`
- `terminal.global`
- `dialog`
- `selector.single`
- `selector.multi`
- `menu`

## UI Rendering Rules

The footer and help text must stop assuming one binding per action.

Recommended display rules:

- if an action has one binding, render it normally: `[a] add project`
- if an action has two short bindings, render both compactly: `Alt-\`/F6 shell focus`
- if an action has more than two bindings, render the primary plus a compact suffix, for example `q/+2 quit`

The first configured binding is the primary binding for display order.

This keeps the UI readable while still honoring multiple bindings.

## Refactor Required

This is not just a config-file change.

Required code changes:

- replace `Hotkey rune` in `tuiActionSpec` with binding-aware metadata
- replace direct `key.MatchString(...)` checks in the main TUI event loop with binding lookup by action ID
- replace direct key checks in dialogs and selectors with binding-aware helpers
- replace `tuiMenuEntry.Key rune` with action IDs or binding lists
- move footer/help generation onto the resolved keymap so displayed help matches actual configured behavior

## Suggested Implementation Shape

### 1. Introduce a TUI Keymap Layer

Add a small binding service responsible for:

- parsing config strings into normalized binding structs
- matching `vaxis.Key` against configured bindings
- formatting bindings for footer/help text

### 2. Separate Action Metadata From Binding Metadata

Keep action metadata such as:

- action ID
- label
- availability by context

separate from the configured bindings for that action.

### 3. Drive Help Text From the Keymap

Footer, selector help, dialog help, and menu help must use the keymap rather than hardcoded text.

Otherwise configuration and help text will drift immediately.

## Migration Strategy

### Phase 1

- add `key_bindings` to config
- define and serialize the current default bindings
- load and validate them
- use configured bindings for:
  - quit
  - terminal/tree focus toggle
  - tree navigation
  - top-level project/node actions

### Phase 2

- move dialog, selector, and overlay bindings onto the same keymap
- replace hardcoded help strings with rendered configured bindings

### Phase 3

- refactor menu entries to use stable action IDs instead of a single letter
- allow multiple bindings for menu actions as well

### Phase 4

- decide whether tmux sidebar reuses the exact same TUI binding namespace or gets its own namespace such as `key_bindings.tmux_sidebar`

## Validation Rules

Invalid configuration should fail clearly and early.

Examples of invalid config:

- unknown action ID
- unknown key token
- duplicate binding within the same action list
- conflicting bindings in the same active scope
- empty binding list for a required action

Load errors should point to the action ID and offending binding string.

## Test Plan

Automated coverage should include:

- `defaultConfigYAML()` includes `key_bindings`
- `LoadConfig()` applies default bindings when the section is missing
- `EnsureLayout()` backfills missing key bindings into legacy `config.yaml`
- custom config bindings override defaults
- multiple configured bindings trigger the same action
- conflicting bindings fail validation
- footer/help rendering reflects configured primary bindings
- node start/stop shared `s` binding still works because the actions are context-disjoint

## Recommendation

Implement key bindings as a first-class config-backed keymap, not as scattered inline conditionals. The current default bindings should move into `_config/config.yaml` as structured defaults, and all TUI input handling should resolve through stable action IDs so multiple bindings per action remain maintainable.
