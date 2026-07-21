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
find "$CODELIMA_HOME" -maxdepth 3 -type d | sort
./bin/codelima configuration list
./bin/codelima configuration show default
```

Verify:

- help lists `settings`, `environment`, `configuration`, and `node`, with no project command
- schema version is `4`
- the home contains `configurations`, `environments`, and `nodes`, with no `projects` directory
- `default` exists with 2 CPUs, 4096 MiB memory, 20480 MiB disk, image `template:ubuntu`, `codex-cli`, and ordered environments `codex` then `claude-code`

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

./bin/codelima configuration update default \
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
if ./bin/codelima configuration delete default; then exit 1; fi
if ./bin/codelima configuration update default --slug renamed-default; then exit 1; fi
if ./bin/codelima configuration delete qa-large; then exit 1; fi
```

Verify all three fail with `PreconditionFailed`.

## Flow 3: multiple directory-bound nodes and cloning

```sh
./bin/codelima node create \
  --slug qa-v3-root-two \
  --configuration default \
  --directory "$QA_ROOT/work/root"

./bin/codelima node create \
  --slug qa-v3-child \
  --configuration default \
  --directory "$QA_ROOT/work/root/child"

./bin/codelima node create \
  --slug qa-v3-prefix \
  --configuration default \
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
./bin/codelima shell qa-v3-root -- sh -lc 'test -f .qa-tools-installed && printf bootstrap-ok'
./bin/codelima node status qa-v3-root
./bin/codelima node stop qa-v3-root
./bin/codelima node status qa-v3-root
```

Verify bootstrap prints `bootstrap-ok`, the first status is running, and the final status is stopped. Review runtime diagnostics to confirm the VM uses the node's frozen 3 CPU / 5120 MiB / 24576 MiB values; CodeLima must not invoke an `msb` subprocess.

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

Verify daemon startup succeeds, `session.json` is version 2 with no terminals, exactly one version-1 quarantine file exists, and the recovery warning names that file. Then verify both terminals target the same `node:<id>` and have different kinds. The no-argument update must replace the daemon PID, report `live_handoff: true`, and preserve both terminal IDs. On macOS it must not report `protocol not supported`, `legacy daemon did not stop`, or `daemon exited before becoming ready`; temporary endpoint files are not shutdown readiness signals. Send `pwd` to the host terminal and verify it resolves to the node's host directory. Send `pwd` to the guest terminal and verify it resolves to the node workspace. Close both terminal IDs before continuing.

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
```

Verify all three responses contain `root`, proving the first listener claims both generic host forms. Start a second VM on the same guest port with a distinct response:

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

Type `printf 'typing-responsive\\n'` quickly into the same shell without pasting. Verify input keeps pace with typing, characters remain ordered, the TUI chrome remains responsive, and the command runs exactly once only after Enter is pressed.

Leave both TUIs and all their terminal tabs idle for at least 30 seconds. In Activity Monitor, inspect every `codelima` process (the daemon and both TUI clients): none may remain near 100% CPU, and idle clients should settle near zero rather than consuming CPU in proportion to their open tab count. `msb` and the Virtual Machine Service are separate VM-runtime processes and are not part of this client/daemon idle assertion.

While one TUI remains open, run `./bin/codelima --json daemon update` from the second real terminal. Confirm the open TUI reports that CodeLima was updated and must be reopened. Leave it at that message for at least 30 seconds and verify its `codelima` process remains idle instead of pinning a core on the closed event stream; then quit and reopen it before continuing.

For restart and handoff restoration, open at least three tabs on `qa-v3-root` in a recognizable guest/host/guest order. In one guest tab, print several lines longer than the visible pane width so they wrap. Quit both TUIs, reopen `./bin/codelima "$QA_ROOT/work/root"`, and verify the three tabs retain the same left-to-right order. Quit again, run `./bin/codelima --json daemon update` from the second real terminal, then reopen the TUI at the same window size. Verify the same tab order remains and the wrapped lines have the same row boundaries: no line may be offset, combined with its neighbor, or split using an 80-column stride.

For cross-scope restoration, run `./bin/codelima "$QA_ROOT/work/root"` in one real terminal and `./bin/codelima "$QA_ROOT/work/prefix"` in another. Open two tabs for `qa-v3-root` in the root-scoped TUI and two tabs for `qa-v3-prefix` in the prefix-scoped TUI. Leave both open through at least one two-second refresh, quit both processes, and reopen both commands. Again leave them open through a refresh and verify each node still has both tabs; neither scoped window may close the other window's daemon tabs.

Verify:

- the left pane title is `Nodes` and has no project rows
- the initially selected running node renders `Info [Terminal]` in the right-pane border while keyboard focus remains in the node list
- pressing `i` renders `[Info] Terminal`, moving to another node preserves that explicit info selection, and pressing `i` again restores terminal mode
- after stopping the selected node and reopening the TUI, its default right-pane mode is info and no replacement guest shell is created
- rows include `qa-v3-root`, `qa-v3-root-two`, `qa-v3-root-clone`, and `qa-v3-child`
- rows do not include `qa-v3-prefix`
- root rows show directory `.` and the child row shows `child`, not absolute paths
- each row shows a configuration label
- `n` opens node creation with a blank directory field and muted current-directory placeholder
- `a` opens global configuration management and `g` opens global environment management
- `Option+t` opens a fresh guest tab and `Option+Shift+t` opens a fresh host tab for the same node without changing tree/fullscreen focus
- the host tab is labeled as a host shell, makes the top bar red only while active, and participates in `Option+Left`/`Option+Right` switching and `Option+w` closing like guest tabs
- `Option+Shift+Backtick` no longer opens or toggles a host terminal
- routine focus handoffs do not show `terminal input ownership was taken by another client`
- the first terminal action after every window-focus takeover and after the idle interval succeeds without a broken pipe or `client is observe-only` error
- multiline paste appears without character-by-character delay, preserves its newline, and executes nothing until Enter is pressed explicitly
- arrows and `Ctrl+a`/`Ctrl+e` edit the guest command line without printing control sequences, `Ctrl+c` interrupts the guest job without closing its tab, and bracketed-paste markers never appear as text
- ordinary typed characters keep pace with input, remain ordered, and do not cause stale-screen flicker
- idle daemon and TUI `codelima` processes do not pin a CPU core or scale CPU use with hidden tab count
- an open TUI left at the post-update reconnect message remains idle rather than retrying the closed event stream
- quitting and reopening the TUI preserves surviving per-node tab order
- quitting and reopening two disjoint path-scoped TUIs preserves both tabs in each process
- reopening after daemon update preserves wrapped line spacing at the captured terminal width

Quit with `q`.

## Flow 8: macOS VirtioFS descriptor-pressure reclaim

This flow is macOS-only. On Linux, verify `daemon snapshot` reports `virtiofs_reclaim.supported: false` and skip the remaining commands.

Create and start a mounted node, then populate its guest dentry/inode caches:

```sh
./bin/codelima node create \
  --slug qa-v3-mounted \
  --configuration default \
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
