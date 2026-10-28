# Runtime QA, 2026-10-27

The goal was to make the local KVM environment usable for real CodeLima tests and verify that the existing libghostty terminal behavior survives the repair. Real Ubuntu guests now boot under KVM, and guest and host terminal checks pass. macOS malware qualification requires a native Mac.

HEAD was `b4e55a5206780b57911f1f0228ab64335915b783`. Runtime tests first used a pinned baseline binary pair, then the coordinator's rebuilt working-tree pair containing the embedded tab reordering change. HEAD alone does not identify those uncommitted changes.

| Executable | Baseline SHA-256 | Rebuilt SHA-256 |
| --- | --- | --- |
| codelima | 4662baa72077427e48b68cec3bb67ee6d29a8c865c8c8555a1de13ac947eefdd | ee7df603c6d83dc69589bf11b418f1afead1cc8d0e59943dc9543e7fe4620abb |
| codelima-renderer-worker | 3a856054707524fa545d59cc1af0bd3f8474e84964295db2162766b19c15e659 | 10e0e2633041183311e7dfcf8c7a288301f81fca83429788d94cca5b4f06fbc7 |

The host was Linux/aarch64, kernel `7.0.0-38-generic`, with 2 vCPUs and approximately 3.8 GiB RAM. Lima was 2.1.0. Managed QEMU was 10.2.1. All successful guest boots used one vCPU, 1024 MiB RAM, a 10 GiB virtual disk, an isolated shell profile, and disabled host-auth import. The built-in Codex and Claude installers were not exercised.

| QA.md flow | Result | Observed evidence or limit |
| --- | --- | --- |
| 1, schema and seeded surface | PASS | Help has settings/environment/configuration/node and no project command. Schema 4, seed 9, five presets in order with exact specified resources. Schema-v3 operation failed with fresh-directory guidance and unchanged version bytes. |
| 2, reusable recipes and frozen values | PASS | A node kept 3 vCPUs, 5120 MiB RAM, and 24576 MiB disk after its recipe changed to 4/6144/28672. Default deletion/rename and deletion of an in-use recipe returned PreconditionFailed. The QA recipe used a shell profile. |
| 3, directories and cloning | PASS | Two nodes shared the root directory. Child and separate-prefix nodes retained their respective canonical directories. Copy mode omitted the mount path. A real stopped clone retained resources, directory, configuration ID, and parent_node_id. Missing create/clone slugs returned InvalidArgument. |
| 4, lifecycle and workspaces | ISSUES | Mounted and copy guests booted to Lima READY under KVM, bootstrapped, validated, and stopped. Login-user HOME, passwordless sudo, workspace ownership, write access, copied README, and bootstrap markers passed. All rendered templates had nestedVirtualization:false. The full 3-vCPU/5-GiB runtime and both coding-agent installations were not run on this 3.8-GiB host. Default virtual ITS required the local override below. |
| 5, daemon terminals and containment | PASS | Version-1 session quarantined exactly once, with original bytes retained and exact filename in the warning. Host and guest terminals shared a node target and had distinct kinds. A history journal above 900 KiB survived live update. Daemon PID changed and live_handoff:true reported three preserved sessions. Host shell PID and exported variable survived. SIGSTOP of only its renderer caused replacement and reaping while preserving the shell PID and accepting fresh output. |
| 5b, two-window input seats | BLOCKED | Shared input and geometry arbitration between two concurrent frontends were not exercised. One real tmux PTY frontend was driven. |
| 6, forwarding | ISSUES | A real guest-loopback HTTP server returned root through node.localhost, localhost, 127.0.0.1, and both forced IPv6 host routes. Multiple-guest ownership transfer, callback 1455, IPv6-only guest listeners, published TCP, migration, and Ports pinning were not exercised. |
| 7, embedded terminal and input | ISSUES | Real rebuilt TUI operated at 150x38 and 80x24. Three guest/host/guest tabs displayed distinct titles. Alt+Shift Left/Right changed daemon-persisted order, preserved the active guest identity, and did not wrap at the left edge. F6 input remained in that guest. Bracketed multiline paste did not execute before Enter. Arrows and Ctrl+A/E edited abcd to abXcd. Ctrl+C interrupted sleep while retaining the tab. Punctuation, accents, CJK, and emoji reached the guest intact. The broad manager flows remain unqualified below. |
| 7, reconnect and stop | PASS | Attached TUI reconnected after live update. Fresh input produced one exact output line in the original guest ID. Stop removed guest sessions while the host session ID, PID, and variable survived. |
| 7, deletion without frontend, ADR 199 | PASS | CLI deletion of a stopped disposable node closed its managed host process, removed the live/persisted session, refused reopening the deleted canonical target, and retained its host workspace sentinel. Provider-failure and racing-launch cases were not manually injected. |
| 7, other manager workflows | BLOCKED | Command palette, contextual help, custom bindings, complete Overview navigation, raw attachment, System controls, lifecycle review dialogs, batch execution, clone startup, environment tests/editors, full Metrics/Logs/Spec exploration, first-run guide, and historical full-screen stress sequences were not requalified. Existing automated gates and prior QA records do not substitute for these manual flows. |
| 8, VirtioFS reclaim | PASS | Linux snapshot reported enabled:true and supported:false. Remaining timer/reclaim checks are macOS-only and blocked here. |
| 9, non-mutating diagnostics | PASS | CLI capture retained 70 files and responsive daemon status/list/snapshot/cached-read probes. Daemon PID, terminal list, and input owner were unchanged. Available Linux /proc evidence existed. Three retained probe failures were an absent rotated log and two unprivileged /proc stack reads. |
| 10, terminal adoption regressions | ISSUES | Actual snapshot cells proved bold colored ANSI text and intact cafe/accent, CJK, and emoji in host and guest shells. OSC titles/progress/prompt metadata, size changes, large-history handoff, and renderer replacement passed. Clipboard delivery, mouse selection, graphics, physical-terminal themes, native macOS, and the full upstream suite were not run by this QA slice. |
| 11, Homebrew install and upgrade | BLOCKED | No native supported Homebrew host or signed/public candidate was exercised. No package was published or installed. |
| Resource gauges and spinner | BLOCKED | Full truecolor/ANSI threshold, focus, clipping, and animation qualification was not performed. |

The baseline make verify and test-qemu-guest results were provided by the coordinator. This runtime slice does not replace its final automated gates. The remaining manual checks require explicit tracking before release qualification.

The KVM diagnosis used the same Ubuntu 25.10 image and kernel `6.17.0-14-generic` throughout. Default QEMU `-machine virt,accel=kvm` booted Linux but stalled before SSH, with the console at kernel time 6.248818. QMP stayed responsive. Matching live instruction bytes to the extracted exact kernel Image and System.map identified `cpu_do_idle+0x14`, immediately after WFI, with `arch_cpu_idle` as its caller. Disabling ACPI did not resolve the wait. A direct KVM kernel boot without block devices reached the expected no-root VFS panic, so KVM could execute Linux.

Changing only the virtual ITS setting to `-machine virt,accel=kvm,its=off` allowed the EFI guest to reach systemd, SSH, cloud-init, and Lima guest-agent readiness. That change is sufficient on this nested host. The exact upstream kernel, QEMU, or outer-hypervisor fault is not established. The successful result does not require TCG or permanent polling idle.

The final ordinary CodeLima restart used Lima's documented debugging override directly, without the temporary QA QEMU wrapper:

```sh
export PATH="$PWD/.tooling/linux-aarch64/qemu/bin:$PATH"
export QEMU_SYSTEM_AARCH64="$PWD/.tooling/linux-aarch64/qemu/bin/qemu-system-aarch64 -machine virt,accel=kvm,its=off"
bash scripts/with_kvm.sh env \
  CODELIMA_HOME="$QA_ROOT/s/h" \
  LIMA_HOME="$QA_ROOT/s/l" \
  XDG_CACHE_HOME="$QA_ROOT/cache" \
  TMPDIR="$QA_ROOT/t" \
  QEMU_SYSTEM_AARCH64="$QEMU_SYSTEM_AARCH64" \
  "$QA_ROOT/bin-final/codelima" node start r2
```

Lima removes its entire conflicting -machine argument. Specifying only `-machine its=off` therefore fails. The complete `virt,accel=kvm,its=off` override is required. Lima explicitly logs that extra QEMU argv are for debugging. With that override, ordinary CodeLima start reached READY in approximately 25 seconds, skipped already completed bootstrap, and reran validation. A subsequent first boot of the copy-workspace node also succeeded.

Storage was a separate test-environment issue. The checkout's VirtioFS supports Unix socket bind, but OpenSSH's socket hard-link handoff failed with Bad file descriptor. The disposable Lima home therefore used an ext4 loop mount backed by a sparse file inside tmp/repairqa. Native default Linux homes do not require that fixture arrangement. No external VM, user default daemon, or outer host setting was modified.

One isolation mistake occurred before the helper's environment fix. The old with_kvm.sh passed through sudo env_reset, which stripped CODELIMA_HOME and LIMA_HOME. The first settings show, doctor --repair, and configuration list reached the existing default home. Doctor reported zero quarantined records, zero recovered stranded nodes, and "seeded built-in metadata and repaired stale files". It also reported that the default home's permissions were broader than user-private. No before-image was recorded, so exact repaired bytes cannot be reconstructed. No default VM or daemon was created. All subsequent calls used explicit env assignments after the helper, and the coordinator added environment-preservation tests and a fix.

The tested binary exposed an existing CLI help mismatch. terminal read advertised format=styled, while its daemon handler recognized format=ansi for styled output. The coordinator corrected that help after this runtime binary was built. Color verification used actual snapshot cells instead.

Prove It Works changed verification from text-only reads to actual cell attributes, real guest processes, persisted terminal order, and exact shell PID checks. Fix Root Causes changed the KVM investigation from permission-only success to a reproducible virtual-ITS boundary and an ordinary successful CodeLima boot.

Cleanup was completed. All six owned Lima instances were deleted. The daemon's final session.json had version 2 and zero terminals. Isolated limactl list --json returned no instances. Owned daemon, renderer, shell, tmux, hostagent, QEMU, and SSH master processes exited. The ext4 mount and loop device were removed. tmp/repairqa, its caches, copied images, test binaries, sockets, screenshots/captures, workspaces, and known host inputrc files created by these sessions were removed. Only this explicitly requested report remains. The pre-existing cached Ubuntu image SHA-256 still matched ee462342e2f4827f9e6008bc17776ad14086c8b8971e4ac24e54bc2b92c139b8. No tracked file was edited by this QA slice.
