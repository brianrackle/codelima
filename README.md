# TestLima

`TestLima` is a Phoenix + LiveView orchestrator for Lima-backed Codex development threads.

Each tracked thread is tied to:

- a registered project directory on the host
- a dedicated Lima VM name
- a browser terminal powered by `ghostty-web`
- a persisted lifecycle timeline plus a JSONL terminal transcript

When a thread starts, the app launches a local PTY bridge, boots the VM, and runs the Codex flow inside Lima:

```bash
limactl start --set '.nestedVirtualization=true' --mount-only .:w
lima sudo snap install node --classic
lima sudo npm install -g @openai/codex
lima codex
```

The implementation scopes those commands to a per-thread VM by setting `LIMA_INSTANCE` and creating the VM from `template:default` when needed.

## Prerequisites

- Elixir and Erlang
- Node.js and npm
- Lima installed on the host (`limactl` and `lima` in `PATH`)
- a working Lima guest environment with `snap` available inside the VM

On Linux hosts, `./script/setup` installs Lima from the official release archive when `apt` does not provide a `lima` package.

## Setup

```bash
./script/setup
mix phx.server
```

On Linux, the setup script prefers `apt-get` for system packages and falls back to the official Lima binary release for `lima`/`limactl` instead of using Homebrew.

Open `http://localhost:4000`.

If you already manage host dependencies yourself, you can skip package installation:

```bash
./script/setup --skip-system-packages
```

## What the App Does

- Add projects by absolute or relative directory path.
- Store optional per-project VM setup commands that run before Codex starts.
- Create multiple conversation threads per project.
- Start and stop thread-specific Lima/Codex sessions.
- Reconnect to active terminals through a `ghostty-web` terminal surface.
- Persist thread metadata in SQLite.
- Persist terminal transcript chunks to `tmp/test_lima/thread_logs/*.jsonl`.

## Runtime Notes

- The PTY bridge is a small Node process started by Phoenix for each active thread.
- The browser terminal connects to that bridge over a local WebSocket using a per-thread access token.
- If Phoenix restarts, active threads are marked as stopped so the UI does not claim the terminal bridge is still attached.

## Verification

```bash
mix test
mix assets.build
```
