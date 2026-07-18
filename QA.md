# Manual QA

Run these flows from the repository root after `make verify`. Keep all disposable artifacts under `./tmp/qa-v3` and remove them when finished.

These checks assume the host can run Microsandbox. The pinned SDK ensures its matching `0.6.6` runtime support files are installed on the first dependency check, so that first command may download them. CodeLima uses the Go SDK directly; an `msb` executable does not need to be on `PATH`.

## Setup

```sh
export QA_ROOT="$PWD/tmp/qa-v3"
export CODELIMA_HOME="$QA_ROOT/home"
rm -rf "$QA_ROOT"
mkdir -p "$QA_ROOT/work/root/child" "$QA_ROOT/work/prefix"
printf 'root\n' > "$QA_ROOT/work/root/README.md"
printf 'child\n' > "$QA_ROOT/work/root/child/README.md"
```

Use a short root. CodeLima and Microsandbox derive Unix-domain socket paths, and deeply nested QA paths can exceed the kernel limit.

## Flow 1: schema-v3 surface and clean break

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
- schema version is `3`
- the home contains `configurations`, `environments`, and `nodes`, with no `projects` directory
- `default` exists with 2 CPUs, 4096 MiB memory, 20480 MiB disk, the Debian systemd image, `codex-cli`, and ordered environments `codex` then `claude-code`

Check schema-v2 rejection:

```sh
mkdir -p "$QA_ROOT/v2/_config"
printf '2\n' > "$QA_ROOT/v2/_config/schema.version"
if ./bin/codelima --home "$QA_ROOT/v2" configuration list > "$QA_ROOT/v2.out" 2> "$QA_ROOT/v2.err"; then
  echo 'unexpected schema-v2 success' >&2
  exit 1
fi
cat "$QA_ROOT/v2.err"
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
```

Verify daemon startup succeeds, `session.json` is version 2 with no terminals, exactly one version-1 quarantine file exists, and the recovery warning names that file. Then verify both terminals target the same `node:<id>` and have different kinds. Send `pwd` to the host terminal and verify it resolves to the node's host directory. Send `pwd` to the guest terminal and verify it resolves to the node workspace. Close both terminal IDs before continuing.

## Flow 6: dynamic `{node}.localhost` forwarding

Start a guest-loopback server in the running node. This uses Perl's core socket module because the default image does not promise Python:

```sh
./bin/codelima shell qa-v3-root -- sh -lc \
  'nohup perl -MIO::Socket::INET -e '\''$s=IO::Socket::INET->new(LocalAddr=>"127.0.0.1",LocalPort=>18080,Listen=>5,Reuse=>1); while($c=$s->accept){<$c>; while(<$c>){last if /^\r?$/}; print $c "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nroot\n"; close $c}'\'' > .qa-http.log 2>&1 &'
```

Wait for daemon discovery, then:

```sh
curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://qa-v3-root.localhost:18080/"
```

Verify the response contains `root`. Stop the node and verify the route is removed. This flow uses no static 8080/5173 mapping.

## Flow 7: path-scoped flat TUI

Run in a real terminal:

```sh
./bin/codelima node start qa-v3-root
./bin/codelima "$QA_ROOT/work/root"
```

After the TUI renders, launch the same command from a second real terminal. Confirm the second TUI starts normally and the first reports that terminal input ownership was taken by another client, then quit the first TUI. Leave the second TUI idle for at least 35 seconds before opening a new terminal tab or switching between guest and host terminals.

Verify:

- the left pane title is `Nodes` and has no project rows
- rows include `qa-v3-root`, `qa-v3-root-two`, `qa-v3-root-clone`, and `qa-v3-child`
- rows do not include `qa-v3-prefix`
- root rows show directory `.` and the child row shows `child`, not absolute paths
- each row shows a configuration label
- `n` opens node creation with a blank directory field and muted current-directory placeholder
- `a` opens global configuration management and `g` opens global environment management
- `Option+t` opens a fresh guest tab and `Option+Shift+t` opens a fresh host tab for the same node without changing tree/fullscreen focus
- the host tab is labeled as a host shell, makes the top bar red only while active, and participates in `Option+Left`/`Option+Right` switching and `Option+w` closing like guest tabs
- `Option+Shift+Backtick` no longer opens or toggles a host terminal
- the first terminal action after takeover and the idle interval succeeds without a broken pipe or `client is observe-only` error

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

Verify no QA sandbox remains in the Microsandbox runtime and `git status --short` contains no QA artifacts.
