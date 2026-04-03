# Runtime Provider Plan

Status: Draft

## Goal

Make CodeLima capable of supporting more than Lima-backed VMs, including:

- local containers
- Firecracker-backed microVMs
- future remote runtimes

without forking the product into separate control planes per runtime.

## Current State

CodeLima is not provider-agnostic today.

What exists now:

- the type system reserves `runtime=container` and `provider=colima`
- the Milestone 1 spec says only `runtime=vm` and `provider=lima` are supported
- the service layer rejects anything else

What this means in practice:

- container support is only a placeholder
- Firecracker support is not planned anywhere yet
- the data model is still Lima-shaped in several important places

Examples of Lima-shaped fields and assumptions:

- project-level `default_lima_template`
- project- and node-level `lima_commands`
- config-level `lima_home`
- node-level `lima_instance_name`
- runtime inspection and shell flows wired directly through the Lima client

## Problem

The repo already reserves future runtimes, but the core metadata and service interfaces still assume Lima concepts directly.

That creates three blockers:

1. Adding containers would require threading Colima or Docker semantics through Lima-shaped metadata.
2. Adding Firecracker would require inventing fake Lima concepts just to fit the current model.
3. Supporting remote runtimes later would be awkward because the system currently assumes local host ownership of Lima-specific lifecycle mechanics.

The next architecture step is not "implement Colima." The next step is "separate core node identity from provider-specific runtime details."

## Design Goals

- keep a single CodeLima control plane and metadata store
- make node identity, lineage, workspace mode, and agent profile provider-independent
- move provider-specific lifecycle and shell behavior behind a runtime-provider interface
- support both local and remote providers through the same host-side orchestration model
- keep live runtime facts ephemeral and out of `node.yaml`
- preserve `codelima shell <node>` as the canonical managed entrypoint regardless of provider

## Non-Goals

- do not make Milestone 1 less Lima-native than it already is
- do not try to support all runtimes with a lowest-common-denominator shell hack
- do not introduce a second metadata store per provider
- do not make guest agents responsible for mutating host control-plane state

## Core Architectural Change

Introduce an explicit runtime-provider boundary.

### A. Core Node Model

The core node record should keep only provider-neutral fields such as:

- `id`
- `slug`
- `project_id`
- `parent_node_id`
- `runtime_kind`
- `provider`
- `workspace_mode`
- `guest_workspace_path`
- `agent_profile_name`
- `bootstrap_state`
- lifecycle metadata

These fields describe what the node is from CodeLima's perspective.

### B. Provider State

Provider-specific runtime details should move into a provider-owned state block or companion file.

Examples:

- Lima:
  - `instance_name`
  - template path
  - Lima command overrides
  - Lima-specific observed metadata
- Container provider:
  - engine name
  - container name or ID
  - image or template reference
  - mount/network policy
- Firecracker provider:
  - machine ID
  - kernel/rootfs references
  - jailer config
  - vsock or SSH transport details

This can be represented as either:

- a provider-specific file such as `runtime-provider.json`
- or a typed `provider_state` object in node metadata

My bias is a companion file so the main node metadata stays readable and provider-neutral.

## Provider Interface

Add a Go interface for runtime providers.

Recommended shape:

```go
type RuntimeProvider interface {
    Name() string
    RuntimeKind() string

    ValidateHost(context.Context) error

    Create(context.Context, Project, Node, ProviderCreateInput) (ProviderState, error)
    Start(context.Context, Project, Node, ProviderState) (ProviderState, error)
    Stop(context.Context, Project, Node, ProviderState) (ProviderState, error)
    Delete(context.Context, Project, Node, ProviderState) error
    Clone(context.Context, Project, Node, ProviderState, CloneInput) (ProviderState, error)

    Shell(context.Context, Project, Node, ProviderState, ShellRequest) error
    Exec(context.Context, Project, Node, ProviderState, ExecRequest) (ExecResult, error)

    Observe(context.Context, Project, Node, ProviderState) (RuntimeObservation, error)
}
```

Optional capability extensions can be added later for:

- file copy
- snapshots
- port forwarding
- metrics
- agent-monitor polling hooks

The important point is that CodeLima should depend on capabilities, not on Lima command-template fields.

## Host-Side Polling Compatibility

The provider boundary should be designed to work with host-driven polling from the start.

That means:

- the host remains the orchestrator
- monitoring should be phrased as provider-mediated guest exec or provider-mediated snapshot retrieval
- no design should require guest-to-host callbacks as the only monitoring path

This matters because:

- local Lima
- local containers
- Firecracker microVMs
- remote VMs

should all be able to satisfy the same host-side runtime and monitoring interfaces, even if the transport differs.

## Container Plan

Containers should not be treated as "small VMs with different commands." They need their own semantics.

Recommended container scope:

- provider: start with `colima` only if it can expose predictable shell and mount behavior
- runtime kind: `container`
- managed shell entry remains `codelima shell <node>`
- workspace mode semantics must be explicit:
  - `mounted` is natural for containers
  - `copy` needs an explicit seed/sync strategy

Important design questions:

- what is the canonical filesystem boundary for copied workspaces?
- how are bootstrap commands applied: image build, first-run init, or exec-on-start?
- how are long-lived named containers mapped to CodeLima node identity?

My recommendation is to treat containers as a separate provider family, not as a Lima special case.

## Firecracker Plan

Firecracker should be treated as a first-class future VM provider, not as a variant of Lima.

Firecracker-specific concerns that need explicit design:

- image/kernel/rootfs lifecycle
- SSH, serial, or vsock execution transport
- guest networking and host reachability
- workspace mount strategy
- startup latency and cold-boot caching
- snapshotting and cloning semantics

That means Firecracker support should come only after the provider boundary exists. Otherwise the implementation will either:

- leak Firecracker concerns all over the core service layer
- or fake Lima concepts in ways that become technical debt immediately

## Data Model Refactor Needed First

Before adding any non-Lima provider, CodeLima should remove or isolate these Lima-shaped concepts from the core project and node model:

- `DefaultLimaTemplate`
- `LimaCommands`
- `LimaHome`
- `LimaInstanceName`

The core model should instead describe:

- desired runtime kind
- selected provider
- provider-neutral workspace behavior
- provider-neutral bootstrap behavior

and then hand provider-specific details to the provider state layer.

## CLI and UX Implications

The user-facing command surface can stay mostly stable:

- `project create`
- `node create`
- `node start`
- `node stop`
- `node delete`
- `shell`

But the internals need to change so these commands dispatch through the selected provider.

Recommended UX rules:

- `node create --runtime vm --provider lima`
- `node create --runtime container --provider colima`
- future:
  - `node create --runtime vm --provider firecracker`

The TUI and tmux sidebar should show:

- runtime kind
- provider
- provider-observed runtime status

without assuming that every node corresponds to a Lima instance.

## Monitoring Implications

The agent-monitoring design should also be provider-aware.

The host should ask the selected provider to retrieve or execute the node's monitoring snapshot. That could mean:

- Lima shell or exec
- container exec
- Firecracker SSH or vsock call
- remote control-plane exec

This is another reason not to hardcode monitoring around Lima-specific paths.

## Migration Plan

### Phase 1: Introduce Provider Boundary While Keeping Lima Only

- add the provider interface
- move current Lima implementation behind it
- keep behavior functionally identical
- keep `vm+lima` as the only supported runtime pair

Success criteria:

- no user-visible behavior change
- Lima remains the only shipped provider
- service flows call the provider interface instead of Lima-specific code directly

### Phase 2: Split Provider State From Core Node Metadata

- move Lima-specific state into a provider-owned structure or companion file
- keep backward compatibility for existing node metadata
- normalize old nodes on rewrite

Success criteria:

- `node.yaml` is provider-neutral enough that a non-Lima provider does not need fake Lima fields

### Phase 3: Add Provider Capability Matrix

Define which providers support which features, for example:

- clone
- snapshot
- mounted workspace mode
- copy workspace mode
- interactive shell
- non-interactive exec
- agent monitoring

Success criteria:

- unsupported operations fail clearly and early
- UI actions are gated by provider capability, not by ad hoc conditionals

### Phase 4: Implement First Non-Lima Provider

Recommended order:

1. `container + colima` if the goal is broader developer workflow coverage quickly
2. `vm + firecracker` if the goal is stronger lightweight isolation and remote-friendly architecture

My bias:

- if product priority is convenience and iteration speed, do containers first
- if product priority is isolation, reproducibility, and future remote orchestration, do Firecracker after the abstraction lands

### Phase 5: Remote Runtime Support

Once provider boundaries exist, add remote-capable providers without redesigning the core node model.

At that stage, the main additional concerns become:

- credentials
- network reachability
- host/provider trust boundaries
- polling cost and backoff

## Risks

- If provider-specific fields stay in the core node model, every new provider will fight the schema.
- If the provider interface is too Lima-shaped, containers and Firecracker will look supported on paper but remain awkward in reality.
- If runtime monitoring is designed around local Lima assumptions, future remote providers will require a second monitoring architecture.
- If copy-mode workspace semantics are not defined per provider, container support will become confusing quickly.

## Recommendation

The next concrete architecture task should be a Lima-preserving refactor that introduces a provider interface and moves Lima-specific state out of the core node model.

After that:

- containers become a real roadmap item instead of a reserved enum
- Firecracker becomes feasible without redesigning the control plane
- host-driven monitoring and future remote runtimes fit the same architecture
