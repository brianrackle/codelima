# Manual QA

Run these flows from the repository root after `make verify`. Keep all disposable artifacts under `./tmp/qa-v4` and remove them when finished.

These checks assume the host has Lima 2.1.0 or a compatible newer Lima 2.x
release and can run VZ on macOS arm64 or QEMU/KVM on Linux. The first create may
download the upstream Ubuntu cloud image.

## Setup

```sh
export QA_ROOT="$PWD/tmp/qa-v4"
export CODELIMA_HOME="$QA_ROOT/home"
export LIMA_HOME="$QA_ROOT/lima"
rm -rf "$QA_ROOT"
mkdir -p "$QA_ROOT/work/root/child" "$QA_ROOT/work/prefix"
printf 'root\n' > "$QA_ROOT/work/root/README.md"
printf 'child\n' > "$QA_ROOT/work/root/child/README.md"
```

Use a short root. Lima derives Unix-domain socket paths below `LIMA_HOME`, and
deep paths can exceed the kernel limit. The selected filesystem must support
Unix sockets; if the checkout is on 9p/NFS, use an isolated short local
`LIMA_HOME` and remove it explicitly during cleanup.

## Flow 1: schema-v4 surface and clean break

```sh
./bin/codelima --help > "$QA_ROOT/help.txt"
./bin/codelima settings show
./bin/codelima doctor --repair
cat "$CODELIMA_HOME/_config/schema.version"
cat "$CODELIMA_HOME/_config/seed.version"
find "$CODELIMA_HOME" -maxdepth 3 -type d | sort
./bin/codelima configuration list
for preset in xsmall small medium large xlarge; do
  ./bin/codelima configuration show "$preset"
done
```

Verify:

- help lists `settings`, `environment`, `configuration`, and `node`, with no project command
- schema version is `4`
- seed version is `4`
- the home contains `configurations`, `environments`, and `nodes`, with no `projects` directory
- `small` is the implicit default and exists with 2 CPUs, 4096 MiB memory, 25600 MiB disk, image `template:ubuntu`, `codex-cli`, and ordered environments `codex` then `claude-code`
- the configuration list contains only `xsmall`, `small`, `medium`, `large`, `xlarge` in that order; they respectively report 1/1024/10240, 2/4096/25600, 4/8192/51200, 6/16384/76800, and 8/32768/102400 for vCPUs/memory MiB/disk MiB, while sharing the initial image, agent profile, and environments

Check schema-v3 rejection without mutation:

```sh
mkdir -p "$QA_ROOT/v3/_config"
printf '3\n' > "$QA_ROOT/v3/_config/schema.version"
sha256sum "$QA_ROOT/v3/_config/schema.version" > "$QA_ROOT/v3.before"
if ./bin/codelima --home "$QA_ROOT/v3" configuration list > "$QA_ROOT/v3.out" 2> "$QA_ROOT/v3.err"; then
  echo 'unexpected schema-v3 success' >&2
  exit 1
fi
cat "$QA_ROOT/v3.err"
sha256sum "$QA_ROOT/v3/_config/schema.version" > "$QA_ROOT/v3.after"
diff -u "$QA_ROOT/v3.before" "$QA_ROOT/v3.after"
```

Verify the error requests a fresh `--home`/`CODELIMA_HOME` and does not claim to migrate.

## Flow 2: reusable configurations and frozen node values

```sh
./bin/codelima environment create \
  --slug qa-tools \
  --bootstrap-command 'printf qa-tools > .qa-tools-installed'

./bin/codelima configuration update small \
  --environment qa-tools \
  --vcpus 2 \
  --memory 4GiB \
  --disk 20GiB

./bin/codelima configuration create \
  --slug qa-large \
  --vcpus 3 \
  --memory 5GiB \
  --disk 24GiB

./bin/codelima node create \
  --slug qa-v3-root \
  --configuration qa-large \
  --directory "$QA_ROOT/work/root"

./bin/codelima configuration update qa-large \
  --vcpus 4 \
  --memory 6GiB \
  --disk 28GiB

./bin/codelima node show qa-v3-root > "$QA_ROOT/frozen-node.yaml"
cat "$QA_ROOT/frozen-node.yaml"
```

Verify the node still reports 3 CPUs, 5120 MiB memory, and 24576 MiB disk, proving configuration edits affect only future nodes. Its directory is canonical, its configuration label is `qa-large`, its workspace mode is `mounted`, and its workspace mount path matches the canonical directory.

Protection checks:

```sh
if ./bin/codelima configuration delete small; then exit 1; fi
if ./bin/codelima configuration update small --slug renamed-small; then exit 1; fi
if ./bin/codelima configuration delete qa-large; then exit 1; fi
```

Verify all three fail with `PreconditionFailed`.

## Flow 3: multiple directory-bound nodes and cloning

```sh
./bin/codelima node create \
  --slug qa-v3-root-two \
  --configuration small \
  --directory "$QA_ROOT/work/root"

./bin/codelima node create \
  --slug qa-v3-child \
  --configuration small \
  --directory "$QA_ROOT/work/root/child"

./bin/codelima node create \
  --slug qa-v3-prefix \
  --configuration small \
  --directory "$QA_ROOT/work/prefix" \
  --workspace-mode copy

./bin/codelima node clone qa-v3-root --slug qa-v3-root-clone
./bin/codelima node list
./bin/codelima node show qa-v3-root-clone
```

Verify:

- both root nodes coexist with the same directory
- the child node is bound to the child directory
- the prefix node is separate, not a descendant of root
- the prefix node reports `workspace_mode: copy` and no workspace mount path, proving the default can be overridden
- the clone has the source directory, configuration ID, frozen resources, and `parent_node_id`
- omitting `--slug` from create or clone fails with `InvalidArgument`

## Flow 4: lifecycle, bootstrap, and SDK resources

```sh
./bin/codelima node start qa-v3-root
./bin/codelima doctor > "$QA_ROOT/doctor.txt"
cat "$QA_ROOT/doctor.txt"
grep -h '^nestedVirtualization:' "$CODELIMA_HOME"/nodes/*/instance.lima.yaml
./bin/codelima shell qa-v3-root -- sh -lc 'test -f .qa-tools-installed && printf bootstrap-ok'
./bin/codelima node status qa-v3-root
./bin/codelima node stop qa-v3-root
./bin/codelima node status qa-v3-root
```

Verify bootstrap prints `bootstrap-ok`, the first status is running, and the final status is stopped. Review runtime diagnostics to confirm the VM uses the node's frozen 3 CPU / 5120 MiB / 24576 MiB values; CodeLima must not invoke an `msb` subprocess.

On a macOS arm64 host where `doctor` reports `nested virtualization is enabled automatically`, verify every rendered node template reports `nestedVirtualization: true` and, while the node is running, this succeeds:

```sh
./bin/codelima shell qa-v3-root -- sh -lc 'test -c /dev/kvm'
```

On an unsupported macOS arm64 host, verify `doctor` reports `nested virtualization is unavailable` and every rendered template reports `nestedVirtualization: false`. On Linux, verify every rendered template also reports `nestedVirtualization: false`; Linux continues to use the separately reported QEMU/KVM host path. Starting a pre-existing node on a supported Mac must also expose `/dev/kvm`, proving the start-time `--nested-virt` override covers nodes whose original template predates this feature.

## Flow 5: daemon terminals and node-host shell

```sh
mkdir -p "$CODELIMA_HOME/_daemon"
printf '{"version":1,"terminals":[]}\n' > "$CODELIMA_HOME/_daemon/session.json"
./bin/codelima daemon start
cat "$CODELIMA_HOME/_daemon/session.json"
find "$CODELIMA_HOME/_daemon" -maxdepth 1 \
  -name 'session.json.unsupported-v1-*' -print
grep 'quarantined incompatible daemon session' \
  "$CODELIMA_HOME/_daemon/daemon.log"
./bin/codelima node start qa-v3-root
NODE_ID="$(./bin/codelima --json node show qa-v3-root | sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' | head -1)"
./bin/codelima --json terminal open "node:$NODE_ID" --kind node-shell > "$QA_ROOT/guest-terminal.json"
./bin/codelima --json terminal open "node:$NODE_ID" --kind node-host-shell > "$QA_ROOT/host-terminal.json"
./bin/codelima terminal list
./bin/codelima daemon status
./bin/codelima --json daemon update > "$QA_ROOT/daemon-update.json"
cat "$QA_ROOT/daemon-update.json"
```

Verify daemon startup succeeds, `session.json` is version 2 with no terminals, exactly one version-1 quarantine file exists, and the recovery warning names that file. Then verify both terminals target the same `node:<id>` and have different kinds. The no-argument update must replace the daemon PID, report `live_handoff: true`, and preserve both terminal IDs. On macOS it must not report `protocol not supported`, `legacy daemon did not stop`, or `daemon exited before becoming ready`; temporary endpoint files are not shutdown readiness signals. Send `pwd` to the host terminal and verify it resolves to the node's host directory. Send `pwd` to the guest terminal and verify it resolves to the node workspace.

Before closing the host terminal, verify renderer containment. Extract its
shell PID, renderer PID, and renderer generation from a daemon snapshot, stop
only the renderer, then generate shell output:

```sh
HOST_TERMINAL_ID="$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); print $j->{data}{terminal_id}' "$QA_ROOT/host-terminal.json")"
export HOST_TERMINAL_ID
./bin/codelima --json daemon snapshot > "$QA_ROOT/renderer-before.json"
QA_SHELL_PID="$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{HOST_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{shell_pid}' "$QA_ROOT/renderer-before.json")"
QA_RENDERER_PID="$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{HOST_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_pid}' "$QA_ROOT/renderer-before.json")"
QA_RENDERER_GENERATION="$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{HOST_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_generation}' "$QA_ROOT/renderer-before.json")"
kill -STOP "$QA_RENDERER_PID"
./bin/codelima terminal send "$HOST_TERMINAL_ID" --text $'printf renderer-recovered\\n\r'
attempt=0
while :; do
  ./bin/codelima --json daemon snapshot > "$QA_ROOT/renderer-after.json"
  generation="$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{HOST_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_generation}' "$QA_ROOT/renderer-after.json")"
  [ "$generation" -gt "$QA_RENDERER_GENERATION" ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 20 ] || exit 1
  sleep 0.25
done
test "$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{HOST_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{shell_pid}' "$QA_ROOT/renderer-after.json")" = "$QA_SHELL_PID"
./bin/codelima terminal read "$HOST_TERMINAL_ID" --source recent
./bin/codelima daemon status
```

Verify `renderer-recovered` appears, the renderer generation and PID changed,
the shell PID did not change, daemon status stayed responsive, and the other
terminal remained usable. The stopped renderer must be killed and reaped by
the terminal-local supervisor without manual cleanup.

Close both terminal IDs before continuing.

## Flow 6: dynamic generic and `{node}.localhost` forwarding

Start a guest-loopback server in the running node. This uses Perl's core socket module because the default image does not promise Python:

```sh
./bin/codelima shell qa-v3-root -- sh -lc \
  'nohup perl -MIO::Socket::INET -e '\''$s=IO::Socket::INET->new(LocalAddr=>"127.0.0.1",LocalPort=>18080,Listen=>5,Reuse=>1); while($c=$s->accept){<$c>; while(<$c>){last if /^\r?$/}; print $c "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nroot\n"; close $c}'\'' > .qa-http.log 2>&1 &'
```

Wait for daemon discovery, then:

```sh
curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://qa-v3-root.localhost:18080/"
curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://localhost:18080/"
curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://127.0.0.1:18080/"
curl --resolve "localhost:18080:[::1]" \
  "http://localhost:18080/"
curl --resolve "qa-v3-root.localhost:18080:[::1]" \
  "http://qa-v3-root.localhost:18080/"
```

Verify all five responses contain `root`, proving the first listener claims
both generic host forms and both hostname routes work when forced through host
IPv6 loopback. In `./bin/codelima --json daemon snapshot`, verify this port's
forwarding `addresses` include both `127.0.0.1:18080` and `[::1]:18080`. Start
a second VM on the same guest port with a distinct response:

```sh
./bin/codelima node start qa-v3-root-two
./bin/codelima shell qa-v3-root-two -- sh -lc \
  'nohup perl -MIO::Socket::INET -e '\''$s=IO::Socket::INET->new(LocalAddr=>"127.0.0.1",LocalPort=>18080,Listen=>5,Reuse=>1); while($c=$s->accept){<$c>; while(<$c>){last if /^\r?$/}; print $c "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\ntwo\n"; close $c}'\'' > .qa-http.log 2>&1 &'

curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://qa-v3-root-two.localhost:18080/"
curl "http://qa-v3-root.localhost:18080/"
curl "http://localhost:18080/"
curl "http://127.0.0.1:18080/"
```

Verify the node-specific URLs return `two` and `root` respectively while generic `localhost` and `127.0.0.1` still return `root`. Stop the first claimant and wait for the one-second reconciliation retry to transfer the generic route:

```sh
./bin/codelima node stop qa-v3-root
attempt=0
while :; do
  response="$(curl -fsS "http://localhost:18080/" 2>/dev/null || true)"
  [ "$response" = "two" ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 10 ] || exit 1
  sleep 1
done
curl "http://127.0.0.1:18080/"
curl "http://qa-v3-root-two.localhost:18080/"
./bin/codelima node stop qa-v3-root-two
sleep 2
if curl -fsS --max-time 2 "http://localhost:18080/"; then exit 1; fi
if curl -fsS --max-time 2 "http://127.0.0.1:18080/"; then exit 1; fi
```

Verify generic `localhost`, `127.0.0.1`, and the second node's explicit hostname all return `two` after transfer, then verify the host listener disappears after the final claimant stops. This flow uses no static 8080/5173 mapping.

On a node with an IPv6-capable HTTP test server, repeat the request on an unused port with the guest server bound only to `::1`. Verify `http://localhost:{port}`, `http://127.0.0.1:{port}`, and `http://{node}.localhost:{port}` return the service response instead of a 502. The daemon log should show neither address failing after the successful IPv6 fallback.

## Flow 7: path-scoped flat TUI

Run in a real terminal:

```sh
./bin/codelima node start qa-v3-root
./bin/codelima node start qa-v3-prefix
./bin/codelima "$QA_ROOT/work/root"
```

On the initial frame, verify all eight top-left wordmark characters shuffle
without moving the adjacent header fields. Starting with `C`, verify one
additional `CodeLima` character settles from left to right about every third of
a second, the complete word remains stable after roughly 2.67 seconds, and
navigation remains responsive throughout.

After the TUI renders, launch the same command from a second real terminal. Confirm the second TUI starts normally and the first does not show an input-ownership warning. Return host focus to the first window and open a terminal tab, then return host focus to the second window and open another terminal tab. Repeat the switch once more in each direction; every newly focused window must work immediately and neither window may show an ownership-revoked message. Quit one TUI, then leave the remaining TUI idle for at least 35 seconds before opening a new terminal tab or switching between guest and host terminals.

Copy and paste these two lines into an active guest or host shell, without pressing Enter:

```sh
printf 'paste-one\n'
printf 'paste-two\n'
```

Verify both lines appear promptly as one paste and neither command runs: the terminal must not print `paste-one` or `paste-two`. Press `Ctrl+c` to clear the pasted input.

In the active guest shell, type `abcd`, use Left twice, type `X`, then use
`Ctrl+a` and `Ctrl+e`. Verify the cursor edits and moves normally and no literal
`^[[D`, `^[[C`, `^A`, or `^E` text appears. Run `sleep 60`, press `Ctrl+c`, and
verify only `sleep` is interrupted: the terminal tab and guest prompt remain
open. Paste the two-line block again and verify no literal `^[[200~` or
`^[[201~` bracketed-paste markers appear.

Immediately after starting a node, toggle between tree and terminal focus with
`Option+Backtick` or `F6` several times. Verify width growth keeps the prompt
clean without typing literal `^L` characters or clearing earlier terminal
history.

Type `printf 'typing-responsive\\n'` quickly into the same shell without pasting. Verify input keeps pace with typing, characters remain ordered, the TUI chrome remains responsive, and the command runs exactly once only after Enter is pressed.

Leave both TUIs and all their terminal tabs idle for at least 30 seconds. In Activity Monitor, inspect every `codelima` process (the daemon and both TUI clients): none may remain near 100% CPU, and idle clients should settle near zero rather than consuming CPU in proportion to their open tab count. `msb` and the Virtual Machine Service are separate VM-runtime processes and are not part of this client/daemon idle assertion.

During that idle interval, verify no structured Lima warning or other
subprocess diagnostic overwrites the TUI chrome. From the second terminal,
inspect `$CODELIMA_HOME/_logs/codelima.log`; any Lima diagnostic emitted during
initial load or refresh must appear there with `source=limactl` instead of on
the TUI screen.

While one TUI remains open, run `./bin/codelima --json daemon update` from the
second real terminal. Confirm the open TUI briefly reports reconnecting, then
returns to ready without being reopened. Return host focus to that window
twice, type a fresh command, and verify it runs exactly once in the original
terminal ID. No `broken pipe`, `EOF`, ownership warning, or reopen instruction
may remain. Leave it attached for at least 30 seconds and verify heartbeats do
not pin a CPU core.

For restart and handoff restoration, open at least three tabs on `qa-v3-root` in a recognizable guest/host/guest order. Move the active third tab left with `Option+Shift+Left`, verify it stays active in the second position, then move it right with `Option+Shift+Right`. Move it left once more so the final order differs from creation order. In one guest tab, print several lines longer than the visible pane width so they wrap. Quit both TUIs, reopen `./bin/codelima "$QA_ROOT/work/root"`, and verify the three tabs retain the reordered left-to-right order. Quit again, run `./bin/codelima --json daemon update` from the second real terminal, then reopen the TUI at the same window size. Verify the same reordered tab order remains and the wrapped lines have the same row boundaries: no line may be offset, combined with its neighbor, or split using an 80-column stride.

For cross-scope restoration, run `./bin/codelima "$QA_ROOT/work/root"` in one real terminal and `./bin/codelima "$QA_ROOT/work/prefix"` in another. Open two tabs for `qa-v3-root` in the root-scoped TUI and two tabs for `qa-v3-prefix` in the prefix-scoped TUI. Leave both open through at least one one-second refresh, quit both processes, and reopen both commands. Again leave them open through a refresh and verify each node still has both tabs; neither scoped window may close the other window's daemon tabs.

From a second real terminal, create a bounded CPU load inside the selected
running node:

```sh
./bin/codelima shell qa-v3-root -- sh -lc \
  'sh -c "while :; do :; done" & load_pid=$!; sleep 5; kill "$load_pid"; wait "$load_pid" || true'
```

Verify the node's `CPU` property changes on successive one-second samples,
rises while the loop runs, and falls after it exits. A running node may show
`CPU: --` only until two valid samples have been collected. Stopped nodes must
show `CPU: --`.

From the second real terminal, create bounded memory and root-disk loads:

```sh
./bin/codelima shell qa-v3-root -- perl -e \
  '$buffer = "x" x (256 * 1024 * 1024); sleep 5'

./bin/codelima shell qa-v3-root -- sh -lc \
  'usage_file=$(mktemp /var/tmp/codelima-usage.XXXXXX); trap '\''rm -f "$usage_file"'\'' EXIT; dd if=/dev/zero of="$usage_file" bs=1M count=256 status=none; sleep 5'
```

Verify `Memory` rises while the allocation is resident and falls after the
process exits. Verify `Disk` rises while the temporary file exists and falls
after the trap removes it. Both lines must refresh on successive one-second
samples and show used/total binary units. The disk total must describe the
guest root filesystem, not the mounted host workspace. Confirm no
`/var/tmp/codelima-usage.*` verification file remains in the guest.

Verify:

- the left pane title is `Nodes` and has no project rows
- the initially selected running node renders `Info [Terminal]` in the right-pane border while keyboard focus remains in the node list
- pressing `i` renders `[Info] Terminal` for the current running node; moving to another running node restores `Info [Terminal]`, ensures exactly one initial guest tab for it, and revisiting that node reuses the tab
- selecting a stopped node renders `[Info] Terminal` without opening a guest tab, and selecting it after its VM starts automatically switches to `Info [Terminal]` with a guest tab
- after stopping the selected node and reopening the TUI, its default right-pane mode is info and no replacement guest shell is created
- node blocks include `qa-v3-root`, `qa-v3-root-two`, `qa-v3-root-clone`, and `qa-v3-child`
- node blocks do not include `qa-v3-prefix`
- every node name is followed by separately indented `Config`, `CWD`, `Status`, `CPU`, `Memory`, and `Disk` property lines
- root blocks show `CWD: .` and the child block shows `CWD: child`, not absolute paths
- CPU percentages refresh once per second, rise under the bounded guest load, fall after it exits, and stay within `0.0%` to `100.0%`
- memory and guest root-disk used/total values refresh once per second, respond to the bounded loads, and never exceed their displayed totals
- clicking any property line selects its owning node, and keyboard scrolling keeps complete seven-line node blocks visible
- `n` opens node creation with the slug-safe current-directory leaf as a muted
  slug default and the current directory as a muted directory default; typing
  in either field replaces its default instead of appending to it
- `a` opens global configuration management and `g` opens global environment management titled `Environments`; its create, manage, and delete surfaces consistently call each reusable command bundle an environment
- configuration selectors list only `xsmall`, `small`, `medium`, `large`, `xlarge` in that order, select `small` by default, and render each row as `<name> (<vCPU> vCPU, <RAM> RAM, <disk> disk)` with the expected built-in resources
- in the configuration update dialog, `Left` and `Right` move the cursor in every editable text and resource field; after moving left, moving right and typing inserts at the expected position, while `Right` still opens the Environments selector
- `Option+t` opens a fresh guest tab and `Option+Shift+t` opens a fresh host tab for the same node without changing tree/fullscreen focus
- the host tab is labeled as a host shell, makes the top bar red only while active, and participates in `Option+Left`/`Option+Right` switching and `Option+w` closing like guest tabs
- `Option+Shift+Left`/`Option+Shift+Right` move the active tab one position without changing the active tab, do not wrap at either edge, and work from both tree and terminal focus
- `Option+Shift+Backtick` no longer opens or toggles a host terminal
- routine focus handoffs do not show `terminal input ownership was taken by another client`
- the first terminal action after every window-focus takeover and after the idle interval succeeds without a broken pipe or `client is observe-only` error
- multiline paste appears without character-by-character delay, preserves its newline, and executes nothing until Enter is pressed explicitly
- arrows and `Ctrl+a`/`Ctrl+e` edit the guest command line without printing control sequences, `Ctrl+c` interrupts the guest job without closing its tab, and bracketed-paste markers never appear as text
- focus-driven terminal width growth neither prints `^L` nor clears earlier terminal history
- ordinary typed characters keep pace with input, remain ordered, and do not cause stale-screen flicker
- idle daemon and TUI `codelima` processes do not pin a CPU core or scale CPU use with hidden tab count
- an open TUI automatically reconnects after daemon update, keeps the original terminal IDs, and accepts fresh input exactly once after authoritative synchronization
- quitting and reopening the TUI preserves surviving per-node operator-defined tab order
- quitting and reopening two disjoint path-scoped TUIs preserves both tabs in each process
- reopening after daemon update preserves wrapped line spacing at the captured terminal width

Quit with `q`.

## Flow 8: macOS VirtioFS descriptor-pressure reclaim

This flow is macOS-only. On Linux, verify `daemon snapshot` reports `virtiofs_reclaim.supported: false` and skip the remaining commands.

Create and start a mounted node, then populate its guest dentry/inode caches:

```sh
./bin/codelima node create \
  --slug qa-v3-mounted \
  --configuration small \
  --directory "$QA_ROOT/work/root" \
  --workspace-mode mounted
./bin/codelima node start qa-v3-mounted
mkdir -p "$QA_ROOT/work/root/.qa-vfs-cache"
i=0; while [ "$i" -lt 10000 ]; do
  : > "$QA_ROOT/work/root/.qa-vfs-cache/file-$i"
  i=$((i + 1))
done
./bin/codelima shell qa-v3-mounted -- sh -lc \
  "find '$QA_ROOT/work/root/.qa-vfs-cache' -type f -print >/dev/null"
```

Temporarily lower the threshold so the flow does not need to consume most of the host file table:

```sh
./bin/codelima daemon stop || true
cp "$CODELIMA_HOME/_config/settings.yaml" "$QA_ROOT/settings.yaml.before-vfs-qa"
perl -0pi -e 's/virtiofs_reclaim_threshold_percent: [0-9]+/virtiofs_reclaim_threshold_percent: 1/' \
  "$CODELIMA_HOME/_config/settings.yaml"
./bin/codelima daemon start
sleep 5
./bin/codelima --json daemon snapshot > "$QA_ROOT/virtiofs-reclaim.json"
cat "$QA_ROOT/virtiofs-reclaim.json"
```

Verify `virtiofs_reclaim` reports `enabled: true`, `supported: true`, threshold `1`, at least one reclaimed node, and a nonzero `last_released_files`. Verify the mounted node remains running and a host write is immediately visible in the guest:

```sh
printf 'still-live\n' > "$QA_ROOT/work/root/.qa-vfs-live"
./bin/codelima shell qa-v3-mounted -- sh -lc \
  "grep -qx still-live '$QA_ROOT/work/root/.qa-vfs-live'"
```

Restore the production threshold before continuing:

```sh
./bin/codelima daemon stop || true
cp "$QA_ROOT/settings.yaml.before-vfs-qa" "$CODELIMA_HOME/_config/settings.yaml"
./bin/codelima daemon start
rm -rf "$QA_ROOT/work/root/.qa-vfs-cache"
rm -f "$QA_ROOT/work/root/.qa-vfs-live"
```

## Flow 9: non-mutating terminal-freeze diagnostics

Keep the daemon and at least one Flow 5 terminal running. Capture its PID and
terminal list, then run the diagnostic skill without rebuilding:

```sh
QA_PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | tr '[:upper:]' '[:lower:]')"
QA_DAEMON_PID_BEFORE="$(cat "$CODELIMA_HOME/_daemon/daemon.pid")"
./bin/"$QA_PLATFORM"/codelima terminal list > "$QA_ROOT/terminals.before-diagnostics"
make diagnose-terminal-freeze \
  DIAG_ARGS="--home \"$CODELIMA_HOME\" --binary \"$PWD/bin/$QA_PLATFORM/codelima\" --output \"$QA_ROOT/terminal-freeze-capture\" --sample-seconds 2"
QA_DAEMON_PID_AFTER="$(cat "$CODELIMA_HOME/_daemon/daemon.pid")"
test "$QA_DAEMON_PID_BEFORE" = "$QA_DAEMON_PID_AFTER"
./bin/"$QA_PLATFORM"/codelima terminal list > "$QA_ROOT/terminals.after-diagnostics"
cmp "$QA_ROOT/terminals.before-diagnostics" "$QA_ROOT/terminals.after-diagnostics"
```

Verify `summary.md`, daemon status/list/snapshot probe outputs, exit-status
files, metadata, and bounded log tails exist. Verify the summary reports a
responsive control plane and terminal actor. On macOS, verify
`daemon-sample.txt` exists and the daemon remained responsive during sampling;
on Linux, verify the available `/proc` artifacts were captured instead. Confirm
the command did not change daemon PID, terminal IDs, input ownership, or shell
contents, and did not create artifacts outside `"$QA_ROOT"`.

## Cleanup

Close any terminal sessions, then remove every QA node before deleting the temporary home:

```sh
for node in qa-v3-mounted qa-v3-root-clone qa-v3-prefix qa-v3-child qa-v3-root-two qa-v3-root; do
  ./bin/codelima node delete "$node" || true
done
./bin/codelima daemon stop || true
rm -rf "$QA_ROOT"
```

Verify `limactl list --json` under the QA `LIMA_HOME` contains no QA instance and `git status --short` contains no QA artifacts.
