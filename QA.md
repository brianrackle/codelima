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
- seed version is `9`
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
  --environment codex \
  --environment claude-code \
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
./bin/codelima shell qa-v3-root -- sh -lc '
  set -eu
  bwrap --version
  guest_user="$(id -un)"
  test "$guest_user" != root
  guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"
  test -n "$guest_home"
  test "$HOME" = "$guest_home"
  test "$(stat -Lc %U "$guest_home/.local/bin/codex")" = "$guest_user"
  test "$(stat -Lc %U "$guest_home/.local/bin/claude")" = "$guest_user"
  readlink -f "$guest_home/.local/bin/codex" | grep -F "$guest_home/.codex/packages/standalone/"
  readlink -f "$guest_home/.local/bin/claude" | grep -F "$guest_home/.local/share/claude/"
  test "$(readlink /usr/local/bin/codex)" = "$guest_home/.local/bin/codex"
  test "$(readlink /usr/local/bin/claude)" = "$guest_home/.local/bin/claude"
  test "$(command -v codex)" = "$guest_home/.local/bin/codex"
  test "$(command -v claude)" = "$guest_home/.local/bin/claude"
  codex --version
  claude --version
  sudo -n id -un
'
./bin/codelima node status qa-v3-root
./bin/codelima node stop qa-v3-root
./bin/codelima node status qa-v3-root
```

Verify bootstrap prints `bootstrap-ok` and `bwrap --version` succeeds as the
login user;
`codelima shell` runs as the Lima login user rather than root, with that user's
`$HOME`; both agent version commands succeed with no privilege wrapper; both
resolve out of `~/.local/bin`, which the Ubuntu `~/.profile` prepends to `PATH`
ahead of the `/usr/local/bin` links that target the same binaries; both native
binaries are owned by that user and resolve inside the native installer layouts;
the two `/usr/local/bin` links target that user's `~/.local/bin`; passwordless `sudo` still
reports `root`, so a user who wants root has it on request; the first status is
running; and the final status is stopped. Review runtime diagnostics to confirm
the VM uses the node's frozen 3 CPU / 5120 MiB / 24576 MiB values; CodeLima must
not invoke an `msb` subprocess.

Confirm the login user owns and can write its workspace in both modes. `mounted`
is `qa-v3-root`, already started above; `copy` is `qa-v3-prefix`, whose tree is
seeded by `limactl cp` over the same SSH login:

```sh
./bin/codelima node start qa-v3-prefix
for node in qa-v3-root qa-v3-prefix; do
  ./bin/codelima shell "$node" -- sh -lc '
    test "$(stat -c %U .)" = "$(id -un)"
    printf ok > .qa-workspace-write
    rm -f .qa-workspace-write
    pwd
  '
done
```

Verify each command prints the node's own workspace path, the workspace is owned
by the login user in both modes, and the unprivileged write succeeds. A
pre-existing node whose workspace was made root-owned by a custom root bootstrap
command is repaired by hand from its own terminal — seeding is once-only and
CodeLima never re-chowns a workspace on start:

```sh
./bin/codelima shell qa-v3-prefix -- sh -lc 'sudo chown -R "$(id -un):$(id -gn)" "$PWD"'
```

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
HOST_TERMINAL_ID="$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); print $j->{data}{terminal_id}' "$QA_ROOT/host-terminal.json")"
export HOST_TERMINAL_ID
```

Before the large-history update, attach the TUI to this home, select the host
tab and resize the outer window several times. Leave the shell idle after
output completes; handoff must stop its reader without requiring another key.
This covers the descriptor-mode regression in ADR 142.

```sh
./bin/codelima terminal send "$HOST_TERMINAL_ID" --text \
  $'head -c 1100000 /dev/zero | tr \'\\0\' x; printf \'\\nlarge-handoff-%s\\n\' ready\r'
attempt=0
while :; do
  ./bin/codelima terminal read "$HOST_TERMINAL_ID" --source recent > "$QA_ROOT/large-handoff-read.txt"
  grep -q 'large-handoff-ready' "$QA_ROOT/large-handoff-read.txt" && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 40 ] || exit 1
  sleep 0.25
done
./bin/codelima --json daemon snapshot > "$QA_ROOT/large-handoff-before.json"
QA_HANDOFF_JOURNAL_BYTES="$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{HOST_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{journal_bytes}' "$QA_ROOT/large-handoff-before.json")"
test "$QA_HANDOFF_JOURNAL_BYTES" -ge 900000
./bin/codelima --json daemon update > "$QA_ROOT/daemon-update.json"
cat "$QA_ROOT/daemon-update.json"
```

Verify daemon startup succeeds, `session.json` is version 2 with no terminals, exactly one version-1 quarantine file exists, and the recovery warning names that file. Then verify both terminals target the same `node:<id>` and have different kinds. The no-argument update must replace the daemon PID, report `live_handoff: true`, and preserve both terminal IDs. On macOS it must not report `protocol not supported`, `legacy daemon did not stop`, or `daemon exited before becoming ready`; temporary endpoint files are not shutdown readiness signals. Send `pwd` to the host terminal and verify it resolves to the node's host directory. Send `pwd` to the guest terminal and verify it resolves to the node workspace.
The update must also preserve `large-handoff-ready` from the host terminal whose
renderer journal exceeded 900 KiB; no `handoff message size` error is allowed.

Before closing the host terminal, verify renderer containment. Extract its
shell PID, renderer PID, and renderer generation from a daemon snapshot, stop
only the renderer, then generate shell output:

```sh
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

### Flow 5b: seat arbitration and shared input (interactive, two windows)

Requires two interactive terminal windows on the host — the second may be an
SSH session from another machine. In window A run `./bin/codelima`, open a
guest terminal tab, and start typing. In window B run `./bin/codelima`
against the same home and select the same tab, then verify all of the
following, in order:

1. Typing in window B is accepted immediately — no
   `client is observe-only` error, no dropped keystrokes — and window A can
   keep typing right after without refocusing. Characters from both windows
   interleave in the shared shell.
2. The window that last gained focus drives the terminal's geometry; the
   other window renders that geometry cropped or padded. Refocusing each
   window moves the geometry to it (the seat) without either window ever
   rejecting input.
3. While typing in window A, run
   `./bin/codelima terminal send "$TERMINAL_ID" --text $'printf cli-interleaved\\n\r'`
   from a third shell. The text executes, window A's typing keeps working
   with no error, and `./bin/codelima --json daemon status` shows
   `input_owner` unchanged.
4. Kill window B's TUI process (or drop its SSH connection) while it holds
   the seat. Window A must keep accepting input with no error and reclaim
   the geometry on its next focus.
5. Repeat step 1 with window A hosted by a terminal without focus reporting
   (Terminal.app, or tmux without `focus-events`). Typing must still always
   work in both windows; only the geometry may lag until focus or
   `terminal takeover` moves the seat.

## Flow 6: dynamic generic and `{node}.localhost` forwarding

Start a guest-loopback server in the running node. This uses Perl's core socket module because the default image does not promise Python:

```sh
./bin/codelima shell qa-v3-root -- sh -lc \
  'nohup perl -MIO::Socket::INET -e '\''$s=IO::Socket::INET->new(LocalAddr=>"127.0.0.1",LocalPort=>18080,Listen=>5,Reuse=>1); while($c=$s->accept){<$c>; while(<$c>){last if /^\r?$/}; print $c "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nroot\n"; close $c}'\'' > .qa-http.log 2>&1 &'

./bin/codelima shell qa-v3-root -- sh -lc \
  'nohup perl -MIO::Socket::INET -e '\''$s=IO::Socket::INET->new(LocalAddr=>"127.0.0.1",LocalPort=>1455,Listen=>5,Reuse=>1); while($c=$s->accept){<$c>; while(<$c>){last if /^\r?$/}; print $c "HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\nroot-codex\n"; close $c}'\'' > .qa-codex-callback.log 2>&1 &'
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
curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://localhost:1455/"
```

Verify all five responses contain `root`, proving the first listener claims
both generic host forms and both hostname routes work when forced through host
IPv6 loopback. Verify the callback-port response contains `root-codex`. In
`./bin/codelima --json daemon snapshot`, verify port 18080's
forwarding `addresses` include both `127.0.0.1:18080` and `[::1]:18080`. Start
a second VM on the same guest port with a distinct response:

```sh
./bin/codelima node start qa-v3-root-two
./bin/codelima shell qa-v3-root-two -- sh -lc \
  'nohup perl -MIO::Socket::INET -e '\''$s=IO::Socket::INET->new(LocalAddr=>"127.0.0.1",LocalPort=>18080,Listen=>5,Reuse=>1); while($c=$s->accept){<$c>; while(<$c>){last if /^\r?$/}; print $c "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\ntwo\n"; close $c}'\'' > .qa-http.log 2>&1 &'

./bin/codelima shell qa-v3-root-two -- sh -lc \
  'nohup perl -MIO::Socket::INET -e '\''$s=IO::Socket::INET->new(LocalAddr=>"127.0.0.1",LocalPort=>1455,Listen=>5,Reuse=>1); while($c=$s->accept){<$c>; while(<$c>){last if /^\r?$/}; print $c "HTTP/1.1 200 OK\r\nContent-Length: 10\r\nConnection: close\r\n\r\ntwo-codex\n"; close $c}'\'' > .qa-codex-callback.log 2>&1 &'

curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://qa-v3-root-two.localhost:18080/"
curl "http://qa-v3-root.localhost:18080/"
curl "http://localhost:18080/"
curl "http://127.0.0.1:18080/"
curl --retry 10 --retry-delay 1 --retry-connrefused \
  "http://localhost:1455/"
curl "http://qa-v3-root.localhost:1455/"
curl "http://qa-v3-root-two.localhost:1455/"
```

Verify the node-specific URLs return `two` and `root` respectively while generic `localhost` and `127.0.0.1` still return `root`.

For the Codex login callback port, verify generic `localhost:1455` instead
returns `two-codex`, while the two node-qualified callback URLs return
`root-codex` and `two-codex`. This proves a newly started Codex login listener
in another VM takes generic callback ownership without changing ordinary
first-claimant service routing.

Stop the first claimant and wait for the one-second reconciliation retry to
transfer the ordinary generic route:

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

In an active guest tab, run `id -un` and `echo "$HOME"`. Verify the tab is the
Lima login user with that user's home, not `root` and not `/root`. Run `sudo id
-un` and verify it answers `root` without a password prompt. Then run `claude
--dangerously-skip-permissions` and verify it starts instead of refusing: Claude
Code declines that flag when it is run as root, which is the whole point of the
login-user terminal. Exit the agent before continuing.

Copy and paste these two lines into an active guest or host shell, without pressing Enter:

```sh
printf 'paste-one\n'
printf 'paste-two\n'
```

Verify both lines appear promptly as one paste and neither command runs: the terminal must not print `paste-one` or `paste-two`. Press `Ctrl+c` to clear the pasted input.

Type punctuation and international text directly (not via `terminal send`),
including `>`, `%`, `:`, `$`, accented letters, CJK and emoji. Verify each
character appears intact, then clear the input. Type and execute
`printf '%s\n' 'punctuation: > % $'`; verify the exact output. Repeat in a
legacy host/tmux and a host with Kitty keyboard reporting, including holding
and releasing a printable key. No printable-key encoding warning should appear.

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

Resize the outer window repeatedly in both directions, in tree and terminal
focus and with a menu/dialog open. The borders and footer must follow the new
window size immediately. Shrink below 60x14, verify the size notice, then grow
again and confirm the UI recovers. In a host and guest shell, print a long
wrapped line and run `stty size` after each resize; the reported rows/columns
must match the visible terminal body and remain stable after further redraws.
Open/close search and change the host font size as well. Verify text reflows,
the prompt remains visible, and earlier output remains readable in scrollback.
Run `make test-tui-resize` for the automated event/draw regressions.

With Kitty keyboard event reporting enabled in the outer terminal, tap and
release each focus shortcut in both directions. Focus must change once on
press and remain there after release. Hold each shortcut through key repeat:
focus must stay stable until a fresh press. Two quick separate presses must
toggle twice without a delay or missed press, and no shortcut text should
appear in the shell. Repeat with legacy escape-prefixed Option input.

From both tree and terminal focus, tap `Option+t` and `Option+Shift+t`,
including releasing each key: each tap must add exactly one tab of the requested
kind. Hold each shortcut through reported repeats; the count must stay fixed
after its first press. Two rapid separate presses must add two tabs. Tap and
hold `Option+w`: only the active tab closes, the adjacent tab remains usable,
and the release must not close it. Close the final tab and verify its release
does not produce an error after focus returns to the tree. Repeat with legacy
Option input and the macOS Option glyph fallbacks. Legacy protocols cannot
distinguish hold repeats from fresh presses. Check `terminal list` alongside
the tab bar: ordinary guest tabs display `shell` and host tabs `host`, without
node names or numbers, even when several tabs have identical names. Shell
username/path title updates must keep these defaults and must not create
another terminal ID.

With two tabs open, run these commands in one tab. Clearing `PROMPT_COMMAND`
and using a plain `PS1` in this disposable shell prevents the prompt from
immediately replacing the test title:

```sh
unset PROMPT_COMMAND
PS1='$ '
printf '\033]2;Test this | codelima\033\\'
```

Verify its label is `Test this | codelima` with no added node name or tab
number (`host:Test this | codelima` for a host tab). Update it with
`printf '\033]2;Renamed task\033\\'` and verify the same tab changes in place.
Run `sleep 3; printf '\007'` and switch to the other tab before it completes.
Verify the background tab shows `Renamed task 🔔` (with `host:` for a host
tab), with no `·` before the emoji. Visit that tab and verify the emoji clears.
Switch away again without another bell; the emoji must stay cleared. Repeat
the delayed bell and verify a new emoji appears and clears on the next visit.
Run `printf '\007'` while viewing the tab and verify no indicator remains.
Clear the title with `printf '\033]2;\033\\'` and verify `shell` or `host`
returns without a number. Repeat in the other tab and verify titles and alerts
belong to the correct tab. In a host that reports
window focus, repeat while CodeLima is unfocused: returning to the visible
tab acknowledges its bell. Info panes and overlays must not acknowledge bells
for terminals they hide. Each attached TUI window acknowledges its own visits.

After an application title, emit ordinary shell titles in the same disposable
shell with `printf '\033]2;user@node: /a/long/project/path\033\\'`,
`printf '\033]2;user: ~/project\033\\'`, and
`printf '\033]2;bash\033\\'`. Each must restore `shell` (or `host`).
Repeat with two same-kind tabs, move one, and close one: names must stay
unnumbered, selection must follow the same terminal ID, and the remaining tab
must still accept input. Restore a task title to confirm it still appears.

Verify all tab metadata keeps updating while another tab is selected. In the
same disposable shell with a plain prompt, run this sequence, then switch away
during its initial delay:

```sh
(
  sleep 3
  printf '\033]2;⠋ Working\033\\\033]9;4;1;10\033\\'
  sleep 3
  printf '\033]2;⠧ Working\033\\'
  sleep 3
  printf '\033]2;⠧ Renamed\033\\\033]9;4;1;60\033\\'
  sleep 3
  printf '\033]2;Done\033\\\033]9;4;0;0\033\\\007'
)
```

Verify the background tab changes spinner frame and title, progresses from
`10%` to `60%`, retains its working state between updates, then shows `Done 🔔`
when the program reports completion. Repeat while remaining on that tab:
title/spinner/progress updates must match, with the bell acknowledged because
the tab is being viewed. Repeat in an unfocused host window when focus
reporting is available. Returning to a tab must show its latest reported state.

Verify the remaining shortcut lifecycles with press/repeat/release reporting:

- Tap and hold `F7`: search stays open after its opening press. A fresh `F7`
  or `Esc` closes it once without sending followups to the shell. `i` toggles
  Info once per press from tree focus.
- Open a form with a node/menu shortcut and hold the opening letter: it must
  not type into the form. After release, a fresh press of that same letter
  types normally. Repeat with a menu action that opens another form.
- Open a selector within a form. Tap/hold `Enter`: the value is chosen and the
  parent form stays open, without submitting. Tap/hold `Esc` or `Ctrl+[` in
  the selector: only the selector closes. A fresh submit/cancel press then
  acts on the parent. `Ctrl+s` and selector-field `Right` also act once.
- Hold multi-select `Space`: the choice toggles once. `Ctrl+u` clears only on
  press. Confirm/cancel followups must not affect a newly exposed screen.
- Hold arrows, `Tab`, tab-switch/move shortcuts, message scroll keys and
  search `Enter`/`Shift+Enter`: they repeat while held and stop on release,
  with no extra step on release. Text editing also retains repeats.
- Close a dialog over terminal focus, lifting its modifier before releasing
  the key. No followup may reach the shell; a fresh press of the same key
  works. Paste into a form and search without submitting or closing them,
  and verify ordinary shell `Ctrl+c`, repeat/release events and bracketed
  paste still work. Repeat the relevant taps with legacy terminal input.

Type `printf 'typing-responsive\\n'` quickly into the same shell without pasting. Verify input keeps pace with typing, characters remain ordered, the TUI chrome remains responsive, and the command runs exactly once only after Enter is pressed.
The cursor must advance with each echoed character without first jumping to an
older position, jumping backward, or briefly appearing ahead of the echo.

In that terminal, run `cmatrix -u 0`. From the second real terminal, identify
the active terminal ID with `./bin/codelima terminal list`, leave `cmatrix`
running for at least 15 seconds, and compare its renderer diagnostics:

```sh
export QA_CMATRIX_TERMINAL_ID='<active-terminal-id>'
./bin/codelima --json daemon snapshot > "$QA_ROOT/cmatrix-before.json"
sleep 15
./bin/codelima --json daemon snapshot > "$QA_ROOT/cmatrix-after.json"
test "$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{QA_CMATRIX_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_pid}' "$QA_ROOT/cmatrix-before.json")" = \
  "$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{QA_CMATRIX_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_pid}' "$QA_ROOT/cmatrix-after.json")"
test "$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{QA_CMATRIX_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_restart_count}' "$QA_ROOT/cmatrix-before.json")" = \
  "$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{QA_CMATRIX_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_restart_count}' "$QA_ROOT/cmatrix-after.json")"
test "$(perl -MJSON::PP -0777 -ne '$j=decode_json($_); $id=$ENV{QA_CMATRIX_TERMINAL_ID}; print $j->{data}{terminal_runtimes}{$id}{renderer_state}' "$QA_ROOT/cmatrix-after.json")" = ready
```

Verify the animation keeps moving, the TUI chrome and another terminal remain
responsive, and no repeated daemon-disconnected/reconnected message appears.
Press `Ctrl+c`; it must stop only `cmatrix` and return to the existing prompt.

Ensure the node has an adjacent terminal tab, run `cmatrix -u 0` again in the
active tab, and press `Option+w` while its output is still unthrottled. The busy
tab must disappear on the next frame, focus must move to the adjacent tab, and
typing `printf 'close-responsive\n'` there must work immediately. From the
second real terminal, verify the closed terminal ID disappears from
`./bin/codelima terminal list` after its bounded daemon cleanup. Repeating the
close shortcut must not produce a reconnect message or close the newly active
tab.

Leave both TUIs and all their terminal tabs idle for at least 30 seconds. In Activity Monitor, inspect every `codelima` process (the daemon and both TUI clients): none may remain near 100% CPU, and idle clients should settle near zero rather than consuming CPU in proportion to their open tab count. `msb` and the Virtual Machine Service are separate VM-runtime processes and are not part of this client/daemon idle assertion.

After that idle interval, inspect the isolated QA logs:

```sh
if grep -q 'tui refresh failed.*i/o timeout' "$CODELIMA_HOME/_logs/codelima.log"; then
  exit 1
fi
if grep -Eq 'renderer call (started|completed)' "$CODELIMA_HOME/_daemon/daemon.log"; then
  exit 1
fi
```

Verify the next terminal action still succeeds on the attached TUI, proving the
request connection outlived its handshake timeout. Normal renderer calls must
not emit per-operation info logs; renderer failures and calls slower than the
diagnostic threshold may still appear.

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

For cross-scope restoration, run `./bin/codelima "$QA_ROOT/work/root"` in one real terminal and `./bin/codelima "$QA_ROOT/work/prefix"` in another. Open two tabs for `qa-v3-root` in the root-scoped TUI and two tabs for `qa-v3-prefix` in the prefix-scoped TUI. Leave both open for at least fifteen seconds so a node-list refresh (pushed on change, or the ten-second fallback tick) has run, quit both processes, and reopen both commands. Again leave them open through a refresh and verify each node still has both tabs; neither scoped window may close the other window's daemon tabs.

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
- the cursor follows echoed output monotonically without pre-echo or backward jumps
- `cmatrix -u 0` runs for at least 15 seconds without renderer replacement,
  terminal error floods, daemon reconnect messages, or loss of `Ctrl+c`
- `Option+w` removes a busy terminal on the next frame, selects the adjacent
  tab, and allows immediate input without a duplicate close or reconnect
- idle daemon and TUI `codelima` processes do not pin a CPU core or scale CPU use with hidden tab count
- an idle TUI request connection survives beyond its handshake timeout without recurring `tui refresh failed` socket timeouts
- normal renderer operations do not emit per-call start/completion log pairs
- an open TUI automatically reconnects after daemon update, keeps the original terminal IDs, and accepts fresh input exactly once after authoritative synchronization
- quitting and reopening the TUI preserves surviving per-node operator-defined tab order
- quitting and reopening two disjoint path-scoped TUIs preserves both tabs in each process
- reopening after daemon update preserves wrapped line spacing at the captured terminal width

Quit with `q`.

## Flow 8: macOS VirtioFS periodic reclaim

This flow is macOS-only. On Linux, verify `daemon snapshot` reports `virtiofs_reclaim.supported: false` and skip the remaining commands.

The reclaim is a workaround for an Apple Virtualization VirtioFS defect and runs on an unconditional 60-second timer, so this flow waits for a tick rather than provoking one; there is no threshold to lower and no host file-table state to arrange.

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

Confirm the settings file carries the on/off switch and no retired threshold key, then wait out one interval and capture the snapshot:

```sh
grep -n 'virtiofs_reclaim' "$CODELIMA_HOME/_config/settings.yaml"
sleep 70
./bin/codelima --json daemon snapshot > "$QA_ROOT/virtiofs-reclaim.json"
cat "$QA_ROOT/virtiofs-reclaim.json"
```

Verify `settings.yaml` contains `virtiofs_reclaim: true` and no `virtiofs_reclaim_threshold_percent`, and that `virtiofs_reclaim` reports `enabled: true`, `supported: true`, `interval_seconds: 60`, a `last_run_at` within the last minute, a `next_run_at` 60 seconds after it, at least one reclaimed node, and no `last_error`. Verify the mounted node remains running and a host write is immediately visible in the guest:

```sh
printf 'still-live\n' > "$QA_ROOT/work/root/.qa-vfs-live"
./bin/codelima shell qa-v3-mounted -- sh -lc \
  "grep -qx still-live '$QA_ROOT/work/root/.qa-vfs-live'"
```

Verify the cadence does not depend on host activity: capture a second snapshot one interval later and confirm `last_run_at` advanced by roughly 60 seconds with the host otherwise idle.

```sh
sleep 70
./bin/codelima --json daemon snapshot > "$QA_ROOT/virtiofs-reclaim.second.json"
cat "$QA_ROOT/virtiofs-reclaim.second.json"
rm -rf "$QA_ROOT/work/root/.qa-vfs-cache"
rm -f "$QA_ROOT/work/root/.qa-vfs-live"
```

Verify a settings refresh preserves operator edits. Add a comment and a user-owned key, restart the daemon so the seed-and-repair pass rewrites the file, and confirm only the `daemon` block changed:

```sh
cp "$CODELIMA_HOME/_config/settings.yaml" "$QA_ROOT/settings.yaml.before-refresh"
printf '# qa operator note\nfuture_qa_key: 7\n' >> "$CODELIMA_HOME/_config/settings.yaml"
./bin/codelima daemon stop || true
./bin/codelima daemon start
cat "$CODELIMA_HOME/_config/settings.yaml"
```

Verify the refreshed file still contains `# qa operator note` and `future_qa_key: 7`.

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

## Flow 10: libghostty-vt adoption regression checks

Run the automated native/package contracts before the interactive checks:

```sh
make test-installers
make test-pkgconf
make test-renderer-boundary
make test-ghostty-vt-schema
make test-ghostty-vt-build
make test-ghostty-vt
make test-ghostty-bridge
make test-ghostty-adapter GHOSTTY_TEST_FILTER='Ghostty|CloneColors|ValidateColors|ValidateInteraction'
make test-vaxis-fork
make test-package
make benchmark-ghostty-compression
```

The package check must initialize and read the real packaged worker with an
empty `PATH` and an unavailable legacy Ghostty library override. Neither
archive may require a `.so`/`.dylib` or a wrapper launcher. A filtered native
unit run is useful for diagnosis but does not replace the full upstream suite.
Record unavailable hosts and compiler resource failures explicitly.

For the clean-host build prerequisite regression, run `make test-pkgconf` and
`PKG_CONFIG=/unavailable/host-pkg-config make build`. The installer tests use an
explicit PATH without pkg-config; Make must provision and select its managed
resolver. Repeat `make build` on a native macOS host without Homebrew's bin
directory in PATH, retaining the already-verified Ghostty cache. The cached
archive must remain reusable, and both executables must build. Do not infer
that native macOS result from Linux installer fixtures.

2026-09-07 Linux/aarch64 qualification: the four-patch source at
`82232ecde55405559dec29c5466cb9e39938cb41` passed the ABI schema check (159
types), native bridge contracts, and the broad adapter filter above both
normally and with `GOFLAGS=-race`. Its installed static ReleaseSmall build
identity is `2e205255ce6d39bbfdf6f314824a31f12c8de3bbf32fac535a3d01e13289626c`.
The final unfiltered `make test-ghostty-vt GHOSTTY_VT_TEST_JOBS=1` retry used
the same source/features/four patches with Zig 0.16.0 and Debug optimization,
while the parent Go gates were held. Compilation ended with `process
terminated with signal KILL`; the build reported 40/45 steps succeeded and two
failed steps. The approximately 3.8 GiB guest did **not** complete the full
upstream unit suite. Do not treat the passing narrower checks as that gate;
rerun on an adequately sized host as tracked in TODO item 41.

In a real host terminal, use the existing isolated QA home and node tabs:

1. Print wrapped text containing ASCII, combining characters, CJK, emoji, an
   OSC 8 link, and SGR 53 overlined text. Drag a normal mouse selection, then
   double/triple click and copy. Verify copied text, link hit testing, wrapping,
   highlights, and overline agree with the cells displayed. Shift-drag remains
   the outer terminal's selection bypass, including inside a mouse-capturing
   application. Restore any clipboard contents you need after this check.
2. Press `F7`, search for a repeated word in visible text and scrollback, and
   use Enter/Shift+Enter to navigate. Verify the UI stays responsive while the
   match count catches up; Esc closes search without sending search keystrokes
   to the shell. Repeat after output, resizing and renderer replacement.
3. Change between light/dark host themes. Verify default foreground/background
   and indexed colors update without blocking input. A host that cannot answer
   color queries must retain usable fallback colors, not invent a black reply.
4. On a Kitty-graphics-capable host, print this bounded in-band red image:

   ```sh
   printf '\033_Ga=T,f=24,s=1,v=1,i=42,c=8,r=4;/wAA\033\\'
   ```

   Verify the image stays clipped to its terminal pane while resizing,
   scrolling, switching tabs, opening an overlay and reconnecting. Exercise a
   larger PNG requiring multiple upload frames while typing; text redraws must
   continue and no partial image, corrupt continuation, stale placement or
   unbounded encoding-worker growth may appear. Verify positive/negative z,
   replacement of an image ID, and deletion with
   `printf '\033_Ga=d,d=I,i=42\033\\'`. Non-graphics hosts must remain usable.
5. Repeat Flow 5's large-history live update and renderer-stop containment.
   Both terminal IDs and shell PIDs must survive; the new worker/native build
   identity must match. A raw fallback must be marked partial when continuity
   is unavailable. Verify checkpoint-incompatible graphics are not silently
   reported as a complete restored native snapshot.
6. With two attached windows, only the geometry-owning seat may receive a
   terminal-originated clipboard write. Background and CLI clients must not
   copy it. Switch focus and repeat with distinct probe text, then disconnect
   and reconnect the owning frontend and repeat; stale connections must not
   receive or replay a copy. Verify Kitty acknowledged clipboard requests fail explicitly when
   the host cannot truthfully acknowledge delivery. Do not use real secrets as
   clipboard test data.
7. In a local macOS frontend, run Codex `/copy` repeatedly for distinct short
   responses and a response near (but below) 64 KiB. Paste into a host editor
   and confirm each full response replaces the previous clipboard contents.
   Repeat with a Unicode response, a tmux-hosted frontend, and a frontend over
   SSH. Both local and remote TUIs must forward through the outer terminal,
   without invoking native desktop clipboard commands. Verify the outer
   terminal permits OSC 52. Also print this from a guest
   shell and paste on the host: `printf '\033]52;c;%s\007' "$(printf 'clipboard smoke' | base64 | tr -d '\n')"`.
   Requests above 64 KiB remain outside the supported clipboard limit. Restore
   any clipboard contents you need afterward.
8. Click once in an embedded terminal without dragging, including after a
   previous selection. Confirm the old selection clears, Messages shows no
   `copy terminal selection: native result -4`, and the host clipboard is
   unchanged. Then select a word or drag a range and verify copying still works.

Keep these interactive results separate from automated bridge/fixture passes.
Record per-flow results and blockers in the release QA report; do not mark the
adoption release-qualified until its native Linux/macOS and physical-terminal
checks have actually run.

## Flow 11: Homebrew installation and regular upgrade

Run `make test-release` and `make test-package` first. On each supported native
Homebrew host, install a locally generated candidate formula from a disposable
tap using a `file://` URL for its matching archive under the QA root. After
publication, repeat using the public tap and verify its URL/checksum against
the release manifest. Confirm GitHub marks it regular and Latest, and the tap
contains `Formula/codelima.rb` with no `Formula/codelima-beta.rb`.
Record the current `command -v codelima` and installed formulae before testing.
Use a fresh Homebrew host with neither the CodeLima tap nor a CodeLima trust
entry for the initial install. Run the fully qualified install directly; verify
it adds the tap and grants formula-specific trust without `invalid syntax in
tap` or an untrusted-formula error. Do not pre-trust the entire tap. On another
fresh host, also verify the README's explicit formula-trust-then-tap sequence.

```sh
brew install brianrackle/codelima/codelima
brew test codelima
codelima --version
```

Confirm the version matches the regular tag and libexec contains both executable
files. Run with an isolated `CODELIMA_HOME` under the QA root and repeat Flow 5's
host-shell creation/output/read/close and daemon stop. For upgrade qualification,
start with the previous regular release, create a live host terminal, run
`brew update && brew upgrade codelima`, then `codelima daemon update`. Confirm
the CLI/daemon version and terminal continuity, and repeat the package checks.

For the retired beta migration, install or upgrade regular `codelima`, invoke
`"$(brew --prefix codelima)/bin/codelima" daemon update`, then uninstall
`codelima-beta`. Remove any beta-specific
`PATH` entry, confirm regular command selection, and verify the existing home
and terminal state remain usable.

Uninstall packages only if this flow installed them solely for verification.
Remove dependencies/caches downloaded solely for this flow without removing
pre-existing installations. Stop the isolated daemon and remove its home.

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
