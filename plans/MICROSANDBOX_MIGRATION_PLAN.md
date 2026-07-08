# Microsandbox Migration Plan — Implementation Handover

Status: Approved — breaking change
Supersedes: `RUNTIME_PROVIDER_PLAN.md` (cancelled; its abstraction-first premise no longer applies — we keep its inventory of Lima-shaped fields, reproduced and extended in §2)
Decision record: `decisions/replace_lima_with_microsandbox_as_sole_runtime_55.md`
Prerequisite reading: `IMPROVEMENT_PLAN.md` Parts A and B (setup, process, architecture map). The same work-item rules apply here: TDD, ADRs, `make verify` per PR, trust-the-code-over-the-document.

---

## 1. The decision

Replace Lima with [microsandbox](https://github.com/superradcompany/microsandbox) (`msb`) as CodeLima's **only** runtime backend. This is a clean break:

- **No provider abstraction layer.** `msb` is the backend the way `limactl` was. `SandboxClient` replaces `LimaClient` one-for-one.
- **No dual-backend transition period.** There is never a build that supports both.
- **No data migration.** Existing Lima-backed homes are rejected with a clear error (§6.4); users delete old nodes with the previous release. Major version bump.

Why microsandbox (condensed; full rationale in ADR 55):

- libkrun microVMs (Hypervisor.framework on macOS, KVM on Linux): hardware isolation, sub-second boots vs Lima's full-VM startup.
- Persistent named sandboxes with detached mode (`msb create --name` / `-d`); survive the launching terminal; managed with `msb start/stop/restart/exec`.
- Real interactive PTY paths: `msb exec` auto-allocates a TTY (`-t/--tty`), and `msb ssh <name>` is the structural equivalent of `limactl shell`.
- Docker-style port publishing (`-p HOST:GUEST`, loopback by default, `0.0.0.0:` opt-in).
- Read-write/read-only host mounts (`--mount-dir SRC:DST[:ro,noexec,nosuid,nodev]`), named volumes.
- Per-sandbox network policy (`--net-default deny`, `--net-rule "allow@github.com"`). Lima never offered egress control; for a product whose pitch is *sandboxed* agentic coding this is a new differentiator, not just parity.
- Snapshots of the writable layer as portable artifacts — the candidate mechanism for `NodeClone`.
- OCI images as the guest environment: reproducible, versionable node environments instead of provision scripts against a moving `template:default`.

Known accepted risk: microsandbox is **beta** (v0.6.x, "breaking changes expected"). Mitigations, all mandatory: pin an exact `msb` version and enforce it in preflight/doctor (§5.5); keep every `msb` invocation behind the client seam and the command-template system (the ADR 22 pattern) so a breaking CLI change is a one-file fix; the Phase 0 spike (§4) is a **gate** — a failure there reopens the decision rather than getting worked around.

## 1.1 Sequencing

This plan executes **after IMPROVEMENT_PLAN Track 2 and before Track 3** (it is row 4.5 in that plan's sequencing table). Reasons, so nobody "optimizes" the order: Track 0 gives you logging and lifecycle tests that make backend bugs diagnosable; Track 2.2's `TerminalLaunchSpec` gives the swap a single launch-path touchpoint; doing it before Track 3 means the daemon, `session.json`, and the integration tier are born msb-native and never encode `limactl` argv.

**Exception: Phase 0 (the spike) can and should start immediately**, in parallel with any other work. It needs no code changes and its failure modes inform everything else.

---

## 2. What Lima does in this codebase today (demolition survey)

Everything in this section is verified against `cb90e7a`. This is the complete inventory of what you are removing or replacing. When you think you're done, re-run the greps in §9.3 against this list.

### 2.1 The client seam (clean — reuse its shape)

`LimaClient` (`lima.go:22-32`), nine methods, all `ctx`-first: `BaseTemplate`, `List`, `Create`, `Start`, `Stop`, `Delete`, `Clone`, `CopyToGuest`, `Shell`. Sole implementation `ExecLimaClient` (`lima.go:34+`, `Binary: "limactl"`); every method resolves a shell-command template and runs it via `sh -lc`, except `List` which execs `limactl list --json` directly and parses `name/status/dir/hostname` into `RuntimeObservation` (`lima.go:207`). `ShellStreams{Stdin, Stdout, Stderr}` carries I/O. There are exactly **20 `s.lima.*` call sites, all in `service.go`** — the CLI and TUI never touch the client directly.

### 2.2 Command templates (keep the pattern, replace the set)

`defaultLimaCommandTemplates()` (`lima_commands.go:22-53`): list / template-copy (`limactl template copy --fill <locator> -`) / create (`limactl create -y --name <n> --cpus=2 --memory=4 --disk=20 <template>`) / start / stop / delete / clone / copy / shell (interactive + exec). Overridable per scope as `lima_commands:` in `config.yaml` → `project.yaml` → `node.yaml` (precedence config → project → node; PATTERNS.MD "Command-Template-First Lima Overrides"; ADRs 17/18/22/23). Placeholders like `{{instance_name}}`.

### 2.3 Lima-isms that leak past the seam (each is a work item in Phases 1–2)

- **Two client bypasses**: `resolveConfiguredLimaCommands("limactl", …)` called directly at `service.go:1734` (bootstrap) and `:1861` (workspace-seed prepare).
- **Two preflight checks**: `exec.LookPath("limactl")` at `service.go:149`, `:183` (`validateDependencies` / `Doctor`).
- **Core metadata fields**: `Node.LimaInstanceName`, `Node.LimaCommands`, `Project.DefaultLimaTemplate`, `Project.LimaCommands`, `Config.LimaHome` (default `~/.lima`, honors `LIMA_HOME` env), `Config.LimaCommands`, `Config.DefaultTemplate` (default `"template:default"`, **required** by `validateConfig`).
- **Template rendering in the service layer**: `renderTemplate` (`service.go:1767`) parses the fetched Lima YAML, strips `cpus/memory/disk` (ADR 19 moved resources to create flags), appends a `provision` block (hostname script — `hostnamectl`/`/etc/hostname`, `service.go:1803`), sets `mounts` via `renderWorkspaceMounts` (`service.go:1956`: `mounted` mode → `{location, mountPoint, writable: true}`; `copy` mode → guest seed via `CopyToGuest`).
- **Store artifacts**: `nodes/<id>/instance.lima.yaml` (`store.go:299`), `nodes/<id>/lima-instance.ref` (`:307`), index `_index/nodes/by-instance/<limaInstanceName>` maintained by `SaveNode`.
- **Runtime-truth reads**: `reconcileNodes`/`reconcileNodeWithObservations` (`service.go:2020/:2039`) match nodes to observations **by `LimaInstanceName`** and map `running/stopped` statuses (ADR 37 batch pattern — keep the pattern, re-point the key).
- **Instance naming**: `generateInstanceName` (`service.go:1743`) derives from the node slug (ADR 42 — carry the logic over).
- **Packaging/docs**: Homebrew formula declares `depends_on "lima"` and desc "Shell-first TUI and CLI for Lima-backed project nodes" (`internal/release/release.go`); README, QA.md (Lima-based flows), PATTERNS.MD (≥6 Lima-named patterns), SPEC.md, TODO.md items (#10, containerd-readiness item), ROADMAP.md.
- **Tests**: `lima_test.go`, `lima_commands_test.go`, `fakeLima` (`service_test.go:12`) and its gates.

### 2.4 Corrections to earlier drafts — read before implementing

1. **`Clone` backs `NodeClone`, not `ProjectFork`.** `ProjectFork` (`service.go:649-751`) is a pure host-filesystem operation (`captureSnapshot` → `materializeSnapshot`, no Lima calls) and is **unaffected by this migration**. `NodeClone` (`service.go:1112`) is what calls `lima.Clone` (`limactl clone`, `service.go:1209`). The snapshot-based clone question (§4 E3) is about NodeClone.
2. **No Lima port forwarding exists in this repo** — Lima *itself* auto-forwards guest listening ports to the host; CodeLima never configured any. So the port regression (§8) is a change in *platform behavior users got for free*, not in CodeLima code you can find and port.
3. **The bootstrap machinery already matches the msb model.** Environment configs are just `BootstrapCommands []string` run in the guest at first `NodeStart` (via `resolveProjectBootstrapCommands` → `bootstrap.json` → `runGuestCommand`). That whole pipeline survives intact; only the *content* of the built-in commands changes (§7.2) and the Lima `provision` YAML block disappears.

---

## 3. The guest contract (new concept — decide once, document)

With Lima, guests were full Ubuntu VMs: systemd, snapd, sudo, apt, cloud-init. With msb, guests are OCI images with (assume) a minimal init and **no systemd/snapd**. Define and document a minimum guest contract that CodeLima requires of any image used for a node:

- `sh` at `/bin/sh`; `curl` or `wget`; a package manager appropriate to the image; `git` (agents need it).
- A writable layer that persists across `msb stop/start` (verified in spike E6).
- Known user identity: determine in the spike (E9) whether the default guest user is root; all built-in bootstrap commands must work under whichever it is (§7.2).

Default image: a mainstream apt-based image (`debian:bookworm` or `ubuntu:24.04` — pick in the spike based on what msb's examples use and image pull size). Users can override per project/node (§6.2). Record the contract + default choice in the Phase 3 ADR.

---

## 4. Phase 0 — validation spike (BLOCKING GATE; start immediately)

A throwaway spike, no production code. Deliverable: `plans/spike-notes/MSB_SPIKE.md` recording, for each experiment: exact commands run, msb version, full relevant output, PASS/FAIL against the listed criterion. Keep the spike script(s) under `./tmp/` per AGENTS.md; the notes file is the durable artifact.

Setup: install microsandbox per docs.microsandbox.dev; record the exact version you pin (`msb --version`). Do everything on both a macOS (Apple Silicon) and a Linux (KVM) machine — CI runs both.

### 4.1 E1 — the embeddability gate (go/no-go for the entire migration)

**Do this first, before any other experiment. If E1 fails on either platform, stop: the migration does not proceed and ADR 55 is reopened. There is no workaround to attempt — CodeLima *is* an embedded terminal product.**

Why this is the gate and not just another row: the evidence that microsandbox supports interactive PTYs at all is **documentation-only and was contradictory during evaluation**. The project README documents no interactive shell whatsoever; only the CLI reference claims `msb exec` auto-detects a TTY (`-t/--tty` to force) and that `msb ssh <name>` exists. Nobody has verified either claim on a real install, and everything downstream (the TUI, the Track 3 daemon, agent detection) assumes a full-fidelity interactive PTY into the guest. Treat vendor docs as hypotheses; this experiment is the test.

Setup: `msb create` a debian sandbox. Then run every check below through **both** `msb ssh <n>` and `msb exec -t <n> -- bash`, **inside the CodeLima TUI's embedded Ghostty terminal** (hack `OpenNodeTab`'s argv locally, or open a project tab and run `msb` from it — the point is that msb's stdin/stdout is CodeLima's PTY, exactly as it will be in production). A check passing in a bare host terminal but failing inside the embedded terminal is a FAIL.

1. **True PTY allocation, guest side.** `tty` prints a `/dev/pts/*` device (not "not a tty"); `stty -a` shows sane raw-capable termios; `[ -t 0 ] && echo interactive` succeeds.
2. **Window size truth + resize propagation.** `stty size` in the guest matches the embedded terminal's cell geometry. Resize the CodeLima pane/window repeatedly: `stty size` updates (SIGWINCH propagates through msb's host→guest transport), and a running `vim`/`htop` reflows instead of clearing or corrupting.
3. **Raw-mode input fidelity.** In `vim`: insert mode, Esc, arrow keys, Ctrl-key chords all behave. In a `python3` REPL: arrow-key history works (readline in raw mode). Paste a multi-line block — no mangling, no stray newlines executing early.
4. **Signal delivery.** `sleep 100` then Ctrl+C interrupts the *guest* process (not the msb client); Ctrl+Z / `fg` job control works; closing the tab kills the msb client and the guest shell learns it (no orphaned guest session accumulating — check `who`/process list in the guest).
5. **Full-screen agent session.** A real `claude` or `codex` session: spinner renders, permission prompts are answerable, screen redraws stay coherent over minutes of use. This screen content is also what Track 5's detection engine will parse — eyeball that the OSC title and prompt lines come through.
6. **Escape-sequence passthrough.** OSC 52 clipboard write from the guest reaches the host clipboard; OSC 8 hyperlinks render and are clickable; 256-color/truecolor test script renders correctly; mouse reporting works in `htop`.
7. **Non-interactive path.** `echo hi | msb exec <n> -- cat` (stdin is a pipe, not a TTY): auto-detection must degrade to non-TTY exec cleanly — output uncorrupted, exit code propagated. This is the `ShellExec` template path.
8. **Latency sanity.** Typing echo in the guest shell feels indistinguishable from `limactl shell` side by side; hold a key down — no accumulating lag.

Record per check, per transport (`ssh` vs `exec -t`), per platform: PASS/FAIL + notes. The better transport becomes the `ShellLogin` template default (§5.2); the other stays one template-override away. Also record whether `msb ssh` needs host-side SSH configuration (keys, agent, known_hosts) — if it does, that setup burden becomes part of doctor/README in Phase 5, and weighs toward `exec -t` as the default.

### 4.2 Remaining experiments

| # | Experiment | How | Pass criterion |
|---|---|---|---|
| E2 | **Machine-readable status** | Find the JSON output: try `msb ls --json`, `msb ps --json`, `msb inspect <n>` | Some command yields parseable output containing at least sandbox name + running/stopped state. Paste the exact schema into the notes — §5.3's `RuntimeObservation` rewrite is built from it |
| E3 | **Clone path (backs NodeClone)** | Create sandbox A; install a package + write files in the workspace and in `$HOME`; snapshot it; create sandbox B from the snapshot | B has A's writable-layer state. If snapshots can't produce a new *named* sandbox, document the closest sequence (snapshot → image → create). If nothing works: NodeClone degrades to "create fresh + re-run bootstrap + copy workspace" — flag the decision in the notes |
| E4 | **Mount uid mapping** | `--mount-dir <host-project>:/workspace` rw; create/edit files from both sides | Guest user can write; host-side ownership of guest-created files is the invoking user (or at minimum consistent and documented); no root-owned droppings in the project dir |
| E5 | **Process ownership** | `msb create -d`; from the host: `ps`-tree the VMM process(es); note parent, pgid, what `msb stop` signals | We can name the host process that owns a detached sandbox and what happens to it on stop/kill. Feeds Track 0.1 semantics and the Track 3 daemon's supervision model |
| E6 | **Persistence across stop/start and host reboot** | Install packages, `msb stop`, `msb start` → check; then full host reboot → `msb start` → check | Writable layer intact in both cases |
| E7 | **Ports** | `msb create -p 8080:8080`; run `python3 -m http.server 8080` inside; `curl localhost:8080` from host. Then try to reach a port that was *not* published; then check whether any command adds a port to a running sandbox | Published port reachable; unpublished not; confirm (expected) ports cannot be added at runtime — this fact drives §8 |
| E8 | **Version pinning** | `msb --version` | Output parseable with a trivial regex for exact-match comparison in doctor |
| E9 | **Guest identity & sudo** | `msb exec <n> -- id -un; command -v sudo` on the default image | Know whether bootstrap commands need `sudo` stripped/added (§7.2) |
| E10 | **Create idempotency & name rules** | `msb create --name x` twice; weird names (uppercase, dots, 63+ chars) | Know the error shape for duplicate names and the legal name charset — feeds `generateInstanceName` port and error mapping (§5.4) |

**Gate review.** E1 is the go/no-go for the entire migration (§4.1) — run it first; a failure reopens ADR 55, full stop. E2, E4, E6, E7 are also hard requirements — a failure on any goes back to the team lead with the notes file; do not start Phase 1. E3 failing only degrades NodeClone (decision recorded, migration proceeds). E5/E8/E9/E10 are informational but must be answered.

---

## 5. Phase 1 — client swap

One PR series behind the seam. The Service layer's *call sites* barely change; the client, templates, and observation types do.

### 5.1 The interface

New file `msb.go` (delete `lima.go` in the same series). Keep the nine-method shape — the Service layer is already programmed against it:

```go
type SandboxClient interface {
    List(ctx context.Context) ([]RuntimeObservation, error)
    Create(ctx context.Context, project Project, node Node) error   // note: no templatePath param — templates are gone
    Start(ctx context.Context, project Project, node Node) error
    Stop(ctx context.Context, project Project, node Node) error
    Delete(ctx context.Context, project Project, node Node) error
    Clone(ctx context.Context, project Project, source, target Node) error // snapshot-based; shape per spike E3
    CopyToGuest(ctx context.Context, project Project, node Node, src, dst string, recursive bool) error
    Shell(ctx context.Context, project Project, node Node, command []string, workdir string, interactive bool, streams ShellStreams) error
}
```

`BaseTemplate` is **deleted** — there is no template fetch/render step; `Create` gets everything from node metadata (image, resources, mounts, ports, net policy). Delete `renderTemplate` (`service.go:1767`), the hostname `provision` injection (`:1803`), and `renderWorkspaceMounts`' YAML output (`:1956`) — mounts/ports/resources become argv assembled by the client from templates. `Service.NodeCreate` drops its template-write step; `SaveNode`'s `template []byte` parameter goes away with it (§6.3).

### 5.2 Command templates — `msb_commands.go`

Keep the command-template-first pattern exactly (config → project → node precedence, `sh -lc` execution, placeholder substitution). Draft defaults — **every one of these must be finalized against spike outputs; the flags below are from vendor docs, not verified locally**:

```go
func defaultRuntimeCommandTemplates() RuntimeCommandTemplates {
    return RuntimeCommandTemplates{
        List:        "msb ls --json",                                                  // finalize per E2
        Create:      "msb create --name {{sandbox_name}} --image {{image}} --cpus {{cpus}} --memory {{memory}}{{mount_flags}}{{port_flags}}{{net_flags}}",
        Start:       "msb start {{sandbox_name}}",
        Stop:        "msb stop {{sandbox_name}}",
        Delete:      "msb rm {{sandbox_name}}",
        Clone:       "<per spike E3>",
        Copy:        "<msb cp if it exists; else tar-over-exec, see §5.4>",
        ShellExec:   "msb exec {{sandbox_name}} -- {{command}}",
        ShellLogin:  "msb ssh {{sandbox_name}}",                                       // or exec -t, per E1
    }
}
```

Placeholders: `{{sandbox_name}}`, `{{image}}`, `{{cpus}}`, `{{memory}}`, `{{mount_flags}}` (rendered from workspace mode: `--mount-dir <ws>:<guest>` for `mounted`, empty for `copy`), `{{port_flags}}` (one `-p h:g` per configured port), `{{net_flags}}` (empty unless a net policy is set, §8.3). Config key renames: `lima_commands:` → `runtime_commands:` everywhere (config/project/node YAML). Delete `lima_commands.go` + `lima_commands_test.go`; the new `msb_commands_test.go` ports the same precedence/substitution test structure.

### 5.3 `RuntimeObservation` reshape

Rebuild the struct from spike E2's actual JSON (expect at least `Name`, `Running bool` or a status string; keep `Exists bool` — reconcile depends on it). Rewrite the `List` parser and the matching key in `reconcileNodeWithObservations`: match on `Node.SandboxName` instead of `Node.LimaInstanceName`. The ADR 37 batch-reconcile pattern (one `List` per read surface, merged in memory) is preserved untouched.

### 5.4 Per-method implementation notes

- **Create**: assemble flags from node metadata. Duplicate-name error (E10) maps to `preconditionFailed`. Name generation: port `generateInstanceName` → `generateSandboxName`, same slug derivation (ADR 42), constrained to the legal charset from E10.
- **CopyToGuest** (used only by copy-mode workspace seeding, `seedGuestWorkspace` `service.go:1846`): use `msb cp` if the spike finds one; otherwise implement tar-over-exec: `tar -C <src> -cf - . | msb exec <n> -- tar -C <dst> -xf -` via the template (stdin plumbing through `ShellStreams`).
- **Shell**: interactive → `ShellLogin` template with `streams` wired directly (mirror the current interactive path, `lima.go:421-432`, which is NOT doubled); non-interactive → `ShellExec`. **Do not reproduce the pre-command double-writer bug** — Track 0.7.1 fixed it in the Lima client; carry the fix, and port its single-line-output regression test to the new client.
- **Error mapping**: msb exit codes/stderr are unknown territory — default wrap is `externalCommandFailed` (exit 6); detect "not found" stderr shapes (E10 notes) → `notFound`; missing binary → `dependencyUnavailable`.
- **Bypasses**: delete the two `resolveConfiguredLimaCommands("limactl", …)` calls (`service.go:1734`, `:1861`) — route both through the client. After this PR, `grep -rn '"msb"' internal/ --include='*.go'` must hit only `msb.go`/`msb_commands.go`.

### 5.5 Preflight and doctor

Replace both `exec.LookPath("limactl")` sites: `exec.LookPath("msb")` + run the version template, parse (E8 regex), compare against a pinned constant `requiredMSBVersion` (exact match — same philosophy as the daemon's protocol versioning). Mismatch → `dependencyUnavailable` with the message: `msb <found> found; codelima <version> requires exactly <pinned> (see docs: pinning microsandbox)`. Doctor prints found vs required.

### 5.6 The fake

Port `fakeLima` → `fakeSandbox` in `service_test.go`: same call-recording, same injectable errors, same **gates** (`fakeLimaGate` → `fakeSandboxGate`) — the async TUI operation tests depend on the gate mechanism; keep their semantics identical so those tests pass with only rename-level changes (characterization contract, §9.1).

---

## 6. Phase 2 — metadata break

### 6.1 Field diff — `types.go` / `config.go`

Remove:

| Field | Where |
|---|---|
| `Node.LimaInstanceName` | types.go |
| `Node.LimaCommands` | types.go |
| `Project.DefaultLimaTemplate` | types.go |
| `Project.LimaCommands` | types.go |
| `Config.LimaHome` (+ `LIMA_HOME` env handling) | config.go |
| `Config.LimaCommands` | config.go |
| `Config.DefaultTemplate` (+ its `validateConfig` requirement) | config.go |

Add:

| Field | Where | Notes |
|---|---|---|
| `Node.SandboxName` | types.go | slug-derived (ADR 42 logic), yaml `sandbox_name` |
| `Node.Image` | types.go | resolved at create from node → project → config |
| `Node.Ports []string` | types.go | `"HOST:GUEST"` strings, validated on create |
| `Node.NetPolicy *NetPolicy` | types.go | optional; `{Default: allow\|deny, Allow []string}` (§8.3) |
| `Node.RuntimeCommands` / `Project.RuntimeCommands` / `Config.RuntimeCommands` | both | the renamed template overrides |
| `Project.DefaultImage` | types.go | yaml `default_image` |
| `Config.DefaultImage` | config.go | required by `validateConfig` (replaces DefaultTemplate); default = the §3 image |
| `Config.DefaultPorts []string` | config.go | seeds `Node.Ports` when unset (§8.1) |

CLI sweep: grep `cli.go` for flags/args referencing `template` (`node create`, `project create/update`) → rename to `--image`; update help text and `cli_test.go`.

### 6.2 Resolution order

Image: `node.Image` → `project.DefaultImage` → `cfg.DefaultImage`. Ports: `node.Ports` → `project` (add `Project.DefaultPorts` if trivially easy, else skip) → `cfg.DefaultPorts`. Same precedence style as the command templates; table-test it.

### 6.3 Store changes

- `instance.lima.yaml` is gone (no template artifact exists anymore); drop the path helper (`store.go:299`) and `SaveNode`'s `template` parameter and conditional write.
- `lima-instance.ref` → `sandbox.ref` (same content pattern: the runtime name for recovery tooling).
- `_index/nodes/by-instance/` keeps its name and role but is keyed by `SandboxName`; `SaveNode`'s index maintenance (including removal on `DeletedAt`/`Terminated`, `store.go:669-671`) carries over unchanged.
- `nodeFileNeedsRefresh`/`projectFileNeedsRefresh`: verify they don't resurrect removed Lima keys when rewriting (they rewrite from the parsed struct, so removed fields disappear — add a test proving a rewritten file contains no `lima` keys).

### 6.4 The schema guard (breaking-change enforcement)

Mechanism — keep it dumb and explicit:

1. New file `CODELIMA_HOME/_config/schema.version`, content `2`, written by `EnsureLayout`'s directory pass on fresh homes (after the Track 0.3 split: by `ensureDirectories`).
2. On startup (in `LoadConfig` or `EnsureReady` — pick the earliest single choke point), classify the home:
   - marker == `2` → proceed.
   - no marker + empty/fresh home (no `projects/`, no `nodes/` entries) → write marker, proceed.
   - no marker + Lima artifacts present (any `nodes/*/lima-instance.ref`, or `lima_commands:`/`lima_home:` keys in `_config/config.yaml`) → fail with `preconditionFailed` (exit 5): `this CODELIMA_HOME contains Lima-backed nodes from codelima <v1>. Delete them with the previous release (codelima node delete ...) or point --home/CODELIMA_HOME at a new directory. No automatic migration exists.`
   - marker missing but no Lima artifacts and home non-empty → same error, softened ("unrecognized home layout").
3. **No migrator. Do not write one.** If you are tempted, re-read ADR 55.

Tests: fresh home proceeds and gains the marker; synthesized v1 home (fixture with a `lima-instance.ref`) → exact error, exit code 5, nothing written; marker-2 home proceeds.

---

## 7. Phase 3 — environments and images

### 7.1 What survives unchanged (verified)

The whole bootstrap pipeline: `EnvironmentConfig{BootstrapCommands []string}` → `resolveProjectBootstrapCommands` at NodeCreate → `bootstrap.json` (`BootstrapState`, frozen per PATTERNS "Bootstrap State Freeze") → at first NodeStart, `runGuestCommand` runs `CombinedCommands()` then `ValidationCommand`, marks `Completed`. Agent profiles (`codex-cli`, `claude-code`: launch command, env, validation) unchanged. Guest workspace seeding (`copy` mode via `CopyToGuest`, `mounted` via mount flags) unchanged in shape.

### 7.2 What changes: the built-in bootstrap commands

The current `codex` built-in **starts with `sudo snap install node --classic`. snapd requires systemd; OCI-image guests have neither.** This is not optional cleanup — the built-in is broken on day one of msb. Rewrite both built-ins against the §3 guest contract (apt-based image), adjusting for spike E9 (if guest user is root, drop `sudo`; if not, keep it and the image must provide it):

- `codex`: NodeSource or distro node install (e.g. `apt-get update && apt-get install -y nodejs npm` if the distro version suffices for the codex CLI — check its engines requirement), then the existing npm-prefix/PATH steps and `npm install -g @openai/codex`.
- `claude-code`: `curl -fsSL https://claude.ai/install.sh | bash` — survives as-is *if* the image has curl (guest contract) — verify interactively in the spike-adjacent QA run.

Update `builtInEnvironmentConfigs()` and **add the old commands to `legacyBuiltInEnvironmentConfigs()`** so the existing upgrade-if-unedited seeding logic (`ensureBuiltInEnvironmentConfigs`, verified: upgrades only when current commands match a legacy spec) migrates untouched configs and leaves user-edited ones alone. Tests: fresh home seeds the new commands; a home with old-spec commands upgrades; a home with user-edited commands doesn't.

### 7.3 Hostname

The Lima `provision` hostname script dies with `renderTemplate`. If E1/E5 show msb doesn't set a useful hostname, prepend a hostname bootstrap command (plain `hostname` + `/etc/hostname` write — no `hostnamectl`, that's systemd) or just drop the feature and record it in the ADR. Nothing in CodeLima reads the guest hostname; it was cosmetic.

### 7.4 Later (record in TODO.md, do not build now)

Prebaked CodeLima agent images on GHCR (image with node+codex / claude preinstalled) for instant node creation — a Dockerfile + release-workflow job. Cuts first-start bootstrap to zero.

---

## 8. Phase 4 — ports and networking UX

Lima auto-forwarded every guest listening port to host localhost with zero configuration. msb publishes only ports declared at create time (spike E7 confirms they can't be added later). This is **the one user-visible regression** — handle it deliberately, not apologetically:

### 8.1 Default port set

`Config.DefaultPorts` seeded into config.yaml as a commented default, e.g. `["3000:3000","5173:5173","8000:8000","8080:8080"]`, applied to every node without explicit ports. Per-project/node override per §6.2. Validation at create: `H:G` numeric, no duplicate host ports across the node's list (duplicate host ports across *nodes* will fail at `msb create` — map that error to `preconditionFailed` naming the conflicting node).

### 8.2 Surfacing

`node show` prints the port list; the TUI info pane shows it; `node create` output mentions it ("ports: 3000, 5173, 8000, 8080 → change requires delete+recreate until runtime port add exists upstream"). README + release notes explain the regression prominently, with the workaround (put your dev-server ports in config before creating the node).

### 8.3 Egress policy (new differentiator — opt-in)

`Node.NetPolicy {Default: "allow"|"deny", Allow: []string}` → rendered to `--net-default deny --net-rule "allow@<entry>"` flags. Default remains allow-all (don't break agent workflows). Document a `sandboxed-strict` example in the README: deny + allowlist of the agent's API endpoints + package registries. Known limitation to document: ICMP behavior depends on policy — `ping`-based connectivity checks inside guests may fail.

---

## 9. Testing strategy

### 9.1 Characterization first (IMPROVEMENT_PLAN Track 7.1 policy — this migration is its poster child)

Before Phase 1: sweep `service_test.go` and tag (a comment or a list in the PR) every test that pins Service behavior *through* `fakeLima` — lifecycle ordering, error propagation, event appends, reconcile semantics, TUI async gates. These are the contract. The Phase 1 PR ports them to `fakeSandbox` with **rename-level changes only**; any assertion that has to change is a semantics break — stop and justify it in the PR or fix the code.

### 9.2 New tests (minimum set)

- Command-template assembly: table tests per verb (create with mounts+ports+net, copy-mode create without mount flags).
- `RuntimeObservation` parser against captured E2 JSON fixtures (commit fixtures under `testdata/`).
- Schema guard (§6.4 three cases).
- Built-in env-config migration (§7.2 three cases).
- Port validation + default-port resolution.
- Version preflight (found==pinned passes; mismatch → exit 3 message).
- Single-line shell output regression (port of TODO #6's test to the new client).
- Integration tier (once Track 7.2 exists): the fake-runtime binary approach — a stub `msb` shell script on PATH that records argv and emulates `ls --json` — enabling end-to-end CLI tests with no real virtualization. (Note: today's suite uses interface fakes only — verified; the stub-binary pattern is new, introduce it here.)

### 9.3 Done-sweep greps (run before calling the migration complete)

```sh
grep -rni 'lima' internal/ cmd/ --include='*.go'        # expect: zero hits
grep -rni 'lima' README.md QA.md PATTERNS.MD SPEC.md Makefile scripts/ .github/
# expect: hits only in decisions/ (historical ADRs), plans/ (historical plans), and release-notes text
grep -rn 'limactl\|lima_commands\|lima_home\|LimaInstance' . --include='*.yaml' --include='*.go'
```

---

## 10. Phase 5 — packaging, docs, and the sweep

1. **Homebrew formula** (`internal/release/release.go` + `scripts/render_homebrew_formula.sh`): `depends_on "lima"` → whatever installs msb (if no brew formula exists upstream, drop the dependency and have `doctor` + README carry the install instruction — decide from what the spike found); desc → "Shell-first TUI and CLI for microsandbox-backed project nodes". Update `internal/release/release_test.go` goldens.
2. **README**: install prerequisites, quickstart, the ports section (§8.2), the guest-contract/images section, the breaking-change notice for v1 homes.
3. **QA.md**: every flow that boots Lima (List/Tree/Shell/Workspace Mode/Clone/TUI/Doctor…) gets its setup/cleanup rewritten for msb; a full manual pass of the rewritten runbook is part of this phase's definition of done.
4. **PATTERNS.MD**: update/rename the Lima-named patterns — "External Command Boundary" (limactl → msb), "Node Slug Lima Identity" (→ Sandbox Identity), "Command-Template-First Lima Overrides" (→ Runtime Overrides), "Clone Is VM Copy" (→ snapshot semantics per E3), "Batch Runtime Reconciliation" (key change), "Guest Workspace Seed".
5. **TODO.md**: close/rewrite Lima-specific items (#10 already handled by Track 0.4; the containerd-readiness item dies with Lima); add the §7.4 prebaked-images item.
6. **ROADMAP.md**: mark the backend swap delivered; note superseded entries.
7. **`scripts/smoke_3_layers.sh`**: update if it touches the runtime.
8. **Version bump**: this ships as the next **major** version; release notes lead with the breaking change and the no-migration policy.

---

## 11. PR sequence (suggested)

| # | PR | Phase |
|---|---|---|
| 0 | Spike notes committed (`plans/spike-notes/MSB_SPIKE.md`) + gate review sign-off | 0 |
| 1 | Characterization sweep/tagging (tests only, no prod change) | 9.1 |
| 2 | `SandboxClient` + `msb.go` + `msb_commands.go` + `fakeSandbox`; Service compiles against the new seam; Lima client deleted | 1 |
| 3 | Bypasses + preflight/doctor + version pin | 1 |
| 4 | Metadata break: types/config/store/CLI flags + schema guard | 2 |
| 5 | Built-in env configs + guest contract ADR + hostname decision | 3 |
| 6 | Ports + net policy + surfacing | 4 |
| 7 | Formula/README/QA/PATTERNS/TODO/ROADMAP sweep + done-greps + manual QA pass | 5 |

PRs 2–4 land in quick succession on a branch if needed — the tree is broken for real use between 2 and 4 (client speaks msb, metadata still Lima-shaped in 2–3 only if you stage it wrong; avoid that by keeping PR 2 to the client+fake with temporary adapter shims on the old fields, removed in PR 4. If the shims get ugly, collapse 2–4 into one PR — correctness beats PR aesthetics here).

---

## 12. Definition of done (whole migration)

On a machine with only `msb` (no Lima installed), both macOS and Linux:

1. Fresh home: `codelima project create` → `node create` (image pulls, ports listed in output) → `node start` (bootstrap runs, completes) — all green.
2. TUI: open the node tab; run a full-screen agent session; resize; copy via OSC 52; close the tab → spike-E5-verified process tree shows nothing leaked (Track 0.1's test extended to the msb chain).
3. `python3 -m http.server 8000` in the node → `curl localhost:8000` on the host succeeds.
4. `node stop` → `node start` → previously installed packages still present.
5. `node clone` produces a working copy (or the documented degraded path, per E3 decision).
6. `node delete` → `msb ls` shows nothing; metadata terminated; re-delete is a no-op.
7. v1 home fixture → exact §6.4 error, exit 5.
8. `codelima doctor` green, including the exact-version check; wrong-version msb → actionable failure.
9. `make verify` green on both CI platforms; §9.3 greps clean; rewritten QA.md manual pass completed.

## 13. Risks and rollback

| Risk | Mitigation |
|---|---|
| msb beta breaking changes | exact version pin enforced by doctor/preflight; all invocations behind seam + templates (one-file fix); spike notes record the pinned version's exact behaviors |
| TTY fidelity gaps vs `limactl shell` (ssh-based) | E1 is a hard gate; both `msb ssh` and `exec -t` evaluated; the better one is the template default and the other stays one template-override away |
| Port pre-declaration surprises users | default port set, create-time output, README/release-notes prominence, `node show`/TUI surfacing |
| Snapshot clone semantics differ from `limactl clone` | E3 before any NodeClone code; documented degraded path as worst case |
| Built-in bootstraps broken in OCI guests (snap/systemd) | §7.2 rewrites them against the guest contract; legacy-spec migration keeps user edits safe |
| Old homes brick silently | §6.4 schema guard with explicit, tested error |
| Rollback | git revert of the PR series; there is no runtime rollback — v(N) homes are msb-only by design, which is why the schema guard exists in both directions (the old binary simply won't know the new fields; document "don't downgrade past the major") |
