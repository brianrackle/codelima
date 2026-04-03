# Split Environment Commands Into Guest And Host Bootstrap Phases

## Summary

Add two first-boot command classes anywhere users can currently define environment command sets:

- `guest` commands: run inside the VM after the VM exists and the guest workspace has been prepared
- `host` commands: run on the host after the VM exists, with access to node/project context so they can drive `limactl`, copy files, or coordinate host-side setup

Defaults chosen for this plan:

- both reusable environment configs and project-specific direct commands support both command classes
- bootstrap order is fixed: guest commands first, then host commands
- host commands are one-time bootstrap commands, not every-start hooks
- host commands run as plain host shell commands with injected environment variables

## Key Changes

### Data model and compatibility

Add separate guest/host command lists to the persisted project, environment-config, and bootstrap models.

Recommended wire shape:

- reusable config:
  - `guest_environment_commands`
  - `host_environment_commands`
- project:
  - `guest_environment_commands`
  - `host_environment_commands`
  - existing `environment_configs` still reference reusable configs that now contribute both lists
- bootstrap state:
  - `guest_setup_commands`
  - `host_setup_commands`

Compatibility rules:

- existing `environment_commands` and legacy `setup_commands` load as guest commands
- existing homes/configs remain valid without migration scripts
- built-in `codex` and `claude-code` defaults stay guest-only unless explicitly changed later

### Service and execution flow

Change node bootstrap resolution so it produces two ordered lists:

1. reusable config guest commands
2. project guest commands
3. reusable config host commands
4. project host commands

Execution flow on first bootstrap:

1. create/start VM
2. prepare guest workspace
3. run guest bootstrap commands in order inside the VM
4. run host bootstrap commands in order on the host
5. run validation command
6. mark bootstrap completed

Host command runner defaults:

- execute with host `/bin/sh -lc`
- stream stdout/stderr through the same CLI/TUI long-running operation output path already used for Lima/bootstrap work
- inject these environment variables for each host command:
  - `CODELIMA_HOME`
  - `CODELIMA_PROJECT_ID`
  - `CODELIMA_PROJECT_SLUG`
  - `CODELIMA_NODE_ID`
  - `CODELIMA_NODE_SLUG`
  - `CODELIMA_RUNTIME`
  - `CODELIMA_PROVIDER`
  - `CODELIMA_INSTANCE_NAME`
  - `CODELIMA_HOST_WORKSPACE_PATH`
  - `CODELIMA_GUEST_WORKSPACE_PATH`

Failure semantics:

- any guest or host bootstrap command failure fails `node start`
- the failing command and phase are recorded in node events/status output
- bootstrap completion remains false until both phases finish successfully

### CLI and TUI

CLI:

- project create/update:
  - keep current guest-command flags working
  - add explicit host-command flags
- environment create/update:
  - keep current guest-command flags working
  - add explicit host-command flags
- show/list output should render both command groups clearly

Recommended CLI names:

- guest:
  - `--env-command` for compatibility
  - optional alias `--guest-env-command`
- host:
  - `--host-env-command`
- clear flags:
  - `--clear-env-commands`
  - `--clear-host-env-commands`

TUI:

- project direct environment command menu and reusable config command menu both become two-section editors:
  - `Guest Commands`
  - `Host Commands`
- each section supports the current add/remove/reorder flow independently
- create environment config should open into the same editor with both sections available immediately
- project details pane should show both lists distinctly
- copy in labels/help should say guest runs "inside the VM" and host runs "on the host after VM creation"

## Test Plan

Automated tests:

- model serialization/deserialization:
  - new guest/host fields round-trip in YAML/JSON
  - legacy `environment_commands` / `setup_commands` map to guest commands
- service resolution:
  - reusable config + project direct commands resolve into separate guest/host bootstrap lists in the right order
- bootstrap execution:
  - guest commands run before host commands
  - host commands receive the injected env vars
  - host command failure fails node start and leaves bootstrap incomplete
  - completed bootstrap does not rerun either phase on later starts
- CLI:
  - create/update/show coverage for new host flags and compatibility aliases
- TUI:
  - env config editor supports add/remove/reorder in both sections
  - project environment editor supports add/remove/reorder in both sections
  - details rendering shows both command groups

Manual QA additions:

- CLI flow creating a reusable config with both command types, assigning it to a project, creating a node, and verifying:
  - guest command changes state inside the VM
  - host command copies or writes a host-driven artifact into the guest/workspace path
- TUI flow editing both sections for project and reusable config
- rerun all existing `QA.md` flows after the feature lands

Docs and records:

- update `README.md`, `QA.md`, and `PATTERNS.MD`
- add an ADR for the split bootstrap phases and host command execution model

## Assumptions

- "outside of VM" means host-executed commands run on the same machine as `codelima`, not in a separate agent process
- host commands are general shell commands, not a restricted DSL
- no per-command interpolation syntax is added; env vars are the only context mechanism
- existing built-in configs remain guest-only by default
