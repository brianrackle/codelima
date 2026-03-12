# CodeLima M1

`CodeLima M1` is a Go CLI that implements the Milestone 1 control plane from [SPEC_M1.md](/Users/brianrackle/Projects/test_lima/SPEC_M1.md).

The CLI manages:

- projects and immutable workspace snapshots under `CODELIMA_HOME`
- Lima-backed nodes delegated through `limactl`
- bidirectional patch proposals along direct project lineage edges
- a canonical shell surface that passes through to `limactl shell`

## Prerequisites

- macOS or Linux
- `curl`, `tar`, `git`, and `make`
- Lima installed and working on the host

The Go toolchain and `golangci-lint` are installed locally by `make init`; a system Go install is not required.

## Setup

```sh
make init
make build
```

The binary is written to `./bin/codelima`.

## User Guide

The examples in this guide assume `codelima` is installed and available on `PATH`.
For repository-local development, use `make run ARGS="..."` or `./bin/codelima ...`.

### Capabilities

- register host workspaces as lineage-aware projects
- capture immutable snapshots when projects are created or forked
- create, start, stop, clone, inspect, and delete Lima-backed nodes
- open an interactive shell or run one-off commands inside a node, starting in a guest-local copy of the project workspace that keeps the same absolute path
- propose, approve, apply, reject, and inspect patches across direct project lineage edges
- inspect local control-plane health with `doctor` and resolved defaults with `config show`

### Command Structure

Most commands follow this shape:

```sh
codelima [--home PATH] [--json] <group> <command> [flags]
```

Useful global flags:

- `--home PATH` points the CLI at a specific `CODELIMA_HOME`
- `--json` returns structured output for automation
- `--log-level LEVEL` reserves a verbosity setting for future CLI logging

### Typical Workflow

1. Check host readiness and inspect the active config.

```sh
codelima doctor
codelima config show
```

2. Register a workspace as a project.

```sh
codelima project create \
  --slug root \
  --workspace ./test-project-dir \
  --setup-command ./script/setup
```

3. Create and start a Lima-backed node for that project.

```sh
codelima node create --project root --slug root-node
codelima node start root-node
codelima node status root-node
```

4. Open a shell or run a one-off command inside the node.

```sh
codelima shell root-node
codelima shell root-node -- uname -a
```

On first start, CodeLima copies the host project workspace into the VM at the same absolute path it has on the host. The host workspace is not mounted into the VM, so guest-side edits stay isolated inside the guest unless you explicitly bring them back out.

`node create` and the first `node start` still require the registered host workspace to exist so the guest copy can be seeded. After that seed is in place, `shell` and later restarts use the guest-local copy without re-mounting the host workspace. Rebind the project before creating a replacement node from a moved host workspace:

```sh
codelima node delete root-node
codelima project update root --workspace /new/host/path
codelima node create --project root --slug root-node
codelima node start root-node
```

5. Fork the project or clone the node into a child lineage.

```sh
codelima project fork root --slug child --workspace /tmp/codelima-child
codelima node clone root-node --project-slug child --node-slug child-node --workspace /tmp/codelima-child
```

`node clone` copies the source VM at the Lima layer. If the source node is running, CodeLima stops it, clones it, and starts it again. The child node keeps the same guest workspace path as the source VM; `--workspace` only defines the child project's host workspace.

6. Move changes back across the lineage with a patch proposal.

```sh
codelima patch propose --source child --target root
codelima patch approve <patch-id> --actor you
codelima patch apply <patch-id>
```

### Useful Examples

Create an isolated metadata root for a temporary session:

```sh
codelima --home /tmp/codelima-dev doctor
```

List projects and print the lineage tree:

```sh
codelima project list
codelima project tree
```

Inspect node history and runtime state:

```sh
codelima node show root-node
codelima node logs root-node
codelima node status root-node
```

Create a three-layer VM lineage from `test-project-dir`:

```sh
codelima project create --slug root --workspace ./test-project-dir --setup-command ./script/setup
codelima node create --project root --slug root-node
codelima node start root-node
codelima node clone root-node --project-slug child --node-slug child-node --workspace /tmp/codelima-child
codelima node start child-node
codelima node clone child-node --project-slug grandchild --node-slug grandchild-node --workspace /tmp/codelima-grandchild
codelima project tree
codelima node list
```

Review a patch without applying it:

```sh
codelima patch list
codelima patch show <patch-id>
codelima patch reject <patch-id> --actor you --note "needs more work"
```

Rebind a project after moving its workspace on the host:

```sh
codelima node delete root-node
codelima project update root --workspace /Users/you/Projects/codelima/test-project-dir
codelima project show root
```

Clean up a node while keeping historical metadata:

```sh
codelima node stop root-node
codelima node delete root-node
```

## Make Shortcuts

```sh
make run ARGS="doctor"
make run ARGS="config show"
make run ARGS="project create --slug root --workspace ./test-project-dir --setup-command ./script/setup"
make run ARGS="node create --project root --slug root-node"
make run ARGS="node start root-node"
make run ARGS="shell root-node -- uname -a"
```

## Tooling

```sh
make fmt
make lint
make test
make build
make verify
```

## Documentation

Keep `README.md` focused on user-facing setup, capabilities, workflows, and command examples.
Internal documentation for design, maintenance, and tooling should live in `BUILD.md`.

## Smoke Test

The smoke test uses the real `limactl` binary and the repository fixture in `test-project-dir` to create and manage three VM layers from a single lineage:

```sh
make smoke
```

The script:

1. creates a root project bound to `test-project-dir`
2. creates and starts a root Lima-backed node
3. clones that node into a child project and child node
4. clones the child node into a grandchild project and grandchild node
5. prints the resulting project tree and node list

## Metadata Layout

By default the CLI stores metadata in `~/.codelima`:

```text
~/.codelima/
  _config/
  _locks/
  _index/
  projects/
  nodes/
  patches/
```

Override the location with `--home` or `CODELIMA_HOME`.

## Notes

- `config show` displays the active defaults and resolved paths.
- Built-in `codex-cli` and `claude-code` profiles are smoke-friendly defaults. Replace the profile YAML files under `CODELIMA_HOME/_config/agent-profiles/` when you want real install commands.
