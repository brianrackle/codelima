# Lima Return Spike

Date: 2026-07-20

Status: Linux/aarch64 implementation gate passed; native macOS and remaining
release-qualification flows are deferred in `TODO.md`.

## Environment

```text
host: Linux aarch64, kernel 6.17.0-40-generic
acceleration: /dev/kvm
Lima: 2.1.0
driver: qemu, accel=kvm
guest template: template:ubuntu
resolved guest: Ubuntu 25.10 arm64 cloud image
mount: 9p
```

The checkout lives on a parent-VM 9p mount. A project-rooted `LIMA_HOME`
correctly failed when OpenSSH attempted to create Lima's control socket there
(`muxserver_listen ... Bad file descriptor`). The successful run therefore
used the isolated native-filesystem path
`/home/brianrackle.guest/.codelima-lima-qa`; CodeLima metadata and workspaces
remained under `./tmp/`. Both the external runtime directory and project-local
artifacts were removed after verification.

## Commands and Results

Template resolution and native validation:

```sh
make test-lima-native
```

PASS. `limactl template copy --fill template:ubuntu` produced a structural
template and `limactl validate` accepted the CodeLima-rendered result.

The runtime lifecycle used an isolated schema-v4 home and the production
binary:

```sh
LIMA_HOME=/home/brianrackle.guest/.codelima-lima-qa \
  ./bin/codelima --home "$PWD/tmp/ch-native" node create \
  --configuration lima-native --directory "$PWD/tmp/cw-native" \
  --slug lima-native --workspace-mode mounted
LIMA_HOME=/home/brianrackle.guest/.codelima-lima-qa \
  ./bin/codelima --home "$PWD/tmp/ch-native" node start lima-native
```

PASS. The rendered instance used QEMU/KVM, 2 CPUs, exactly 2048 MiB RAM and
10240 MiB disk, one writable workspace mount, disabled containerd, TCP SSH,
and a final ignore-all automatic-port rule. The node reached `running`.

Guest/root, mounted workspace, and persistence checks:

```sh
./bin/codelima ... node shell lima-native -- sh -lc \
  'test "$(id -u)" = 0 && test "$(cat .../from-host)" = from-host && printf lima-root-ok > .../from-guest'
./bin/codelima ... node stop lima-native
./bin/codelima ... node start lima-native
./bin/codelima ... node shell lima-native -- sh -lc \
  'test "$(cat .../from-guest)" = lima-root-ok'
```

PASS. Guest commands retained the pre-migration root contract, host-to-guest
and guest-to-host mounted writes worked, and the file persisted across a
stop/start cycle.

Running-source clone:

```sh
./bin/codelima ... node clone lima-native --slug lima-clone
```

PASS. CodeLima stopped `lima-native`, cloned it as a stopped `lima-clone`, and
restored `lima-native` to running. Direct `limactl list --json` confirmed both
states.

Observation and forwarding:

```sh
./bin/codelima ... daemon start
curl -H 'Host: lima-native.localhost:18080' \
  http://127.0.0.1:18080/from-host
./bin/codelima ... daemon snapshot
```

PASS. The response was `from-host`. The snapshot reported one SSH peer, a
serving node-qualified route, and `lima_observation.connected: true` with two
cached instances. The daemon used one `limactl watch --json` process and did
not spawn a per-node SSH helper.

Delete and cleanup:

```sh
./bin/codelima ... daemon stop
./bin/codelima ... node delete lima-clone
./bin/codelima ... node delete lima-native
limactl list --json
```

PASS. The isolated Lima home reported no instances. All verification-only
metadata, workspaces, runtime files, and processes were cleaned up. Unrelated
Lima instances in the default host home were never addressed.

A second isolated node used `--workspace-mode copy`. The production start path
seeded a regular file, an executable script, and a relative symbolic link with
`limactl copy`; guest checks confirmed the content, executable bit, symlink
target, and successful script execution. That node and its isolated home were
also deleted and cleaned.

`make smoke` then passed the production three-layer flow: create/start/stop,
stopped clone, child start/stop, grandchild clone/start/stop, list, and trapped
deletion. The isolated smoke `LIMA_HOME` listed no instances afterward and was
removed.

Interactive shell regression qualification on 2026-07-21 used a fresh
Linux/aarch64 QEMU/KVM guest, the production `codelima node shell` path, and a
real controlling PTY. `Ctrl+a`, `Ctrl+e`, and Left edited `cho abXc` into
`echo abZXc`; a two-line bracketed paste produced both expected lines without
literal `^[[200~` or `^[[201~` text; and `Ctrl+c` interrupted `sleep 60` while
the same guest tab stayed open and ran `echo TTY-AFTER-INTERRUPT`. The isolated
node, CodeLima home, Lima home, workspace, and processes were removed after the
test. `TestRunInteractiveCommandKeepsPTYInForeground` independently reproduces
the host process-group failure under a real PTY and guards the fix.

## Matrix

| ID | Result | Evidence |
|---|---|---|
| L0.1 | PASS | Lima version and JSON-lines list parsing validated by unit, fake-process, and native runs. |
| L0.2 | PARTIAL | Linux QEMU/KVM boot passed; macOS VZ pending. |
| L0.3 | PASS (Linux transport) | Real PTY verified arrows, Readline controls, bracketed paste, and guest interrupt; full macOS Ghostty TUI matrix remains release QA. |
| L0.4 | PASS (Linux) | One 9p mount; bidirectional writes passed. |
| L0.5 | PASS (Linux) | Native copy preserved files, executable mode, and a relative symlink. |
| L0.6 | PASS (Linux) | Mounted file survived stop/start. Host reboot remains release QA. |
| L0.7 | PASS | Running source was stopped, cloned, and restored; target remained stopped. |
| L0.8 | PARTIAL | Ignore-all and dynamic route passed; full explicit conflict matrix remains QA. |
| L0.9 | PASS | Persistent Go SSH forwarding returned the expected HTTP response. |
| L0.10 | PASS | Watch connected and cached two instances in the daemon snapshot. |
| L0.11 | PENDING | Five-minute native idle measurement remains release qualification. |
| L0.12 | PENDING | macOS sleep/wake requires native macOS. |
| L0.13 | AUTOMATED | Create/clone rollback and incomplete-cleanup tests pass. |
| L0.14 | PENDING | Native macOS VirtioFS pressure qualification remains. |
