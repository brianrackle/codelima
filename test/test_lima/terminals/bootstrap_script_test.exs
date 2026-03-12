defmodule TestLima.Terminals.BootstrapScriptTest do
  use ExUnit.Case, async: false

  @script_path Path.expand("../../../priv/scripts/run_lima_codex.sh", __DIR__)

  setup do
    tmp_dir =
      System.tmp_dir!()
      |> Path.join("test_lima_bootstrap_#{System.unique_integer([:positive])}")

    fake_bin_dir = Path.join(tmp_dir, "bin")
    project_dir = Path.join(tmp_dir, "project")
    trace_file = Path.join(tmp_dir, "trace.log")
    setup_file = Path.join(tmp_dir, "setup-commands.log")

    File.mkdir_p!(fake_bin_dir)
    File.mkdir_p!(project_dir)

    write_fake_limactl(fake_bin_dir)
    write_fake_lima(fake_bin_dir)

    on_exit(fn -> File.rm_rf!(tmp_dir) end)

    {:ok,
     fake_bin_dir: fake_bin_dir,
     project_dir: project_dir,
     setup_file: setup_file,
     tmp_dir: tmp_dir,
     trace_file: trace_file}
  end

  test "runs project setup commands before launching codex", context do
    setup_commands = "mise install\nmix ecto.setup"
    vm_name = "atlas-vm"

    {output, 0} =
      System.cmd("bash", [@script_path, context.project_dir, vm_name, setup_commands],
        env: shell_env(context, vm_name),
        stderr_to_stdout: true
      )

    assert File.read!(context.setup_file) == "set -euo pipefail\n#{setup_commands}"
    assert File.read!(context.trace_file) =~ "lima:bash\nlima:codex\n"
    assert output =~ "[bootstrap] running project setup commands"
    assert output =~ "[bootstrap] launching codex-cli"
  end

  test "creates missing VMs from Lima's current default template locator", context do
    vm_name = "atlas-vm"

    {_output, 0} =
      System.cmd("bash", [@script_path, context.project_dir, vm_name, ""],
        env:
          List.keystore(shell_env(context, vm_name), "TEST_VM_EXISTS", 0, {"TEST_VM_EXISTS", "0"}),
        stderr_to_stdout: true
      )

    assert File.read!(context.trace_file) =~
             "limactl:start -y --set .nestedVirtualization=true --name=#{vm_name} --mount-only #{context.project_dir}:w template:default\n"
  end

  test "skips the project setup step when no commands are configured", context do
    vm_name = "atlas-vm"

    {_output, 0} =
      System.cmd("bash", [@script_path, context.project_dir, vm_name, ""],
        env: shell_env(context, vm_name),
        stderr_to_stdout: true
      )

    refute File.exists?(context.setup_file)
    assert File.read!(context.trace_file) =~ "lima:codex\n"
    refute File.read!(context.trace_file) =~ "lima:bash\n"
  end

  test "finds Lima host commands via HOMEBREW_PREFIX when PATH omits them", context do
    vm_name = "atlas-vm"
    homebrew_prefix = Path.join(context.tmp_dir, "homebrew")
    homebrew_bin_dir = Path.join(homebrew_prefix, "bin")

    File.mkdir_p!(homebrew_bin_dir)
    write_fake_limactl(homebrew_bin_dir)
    write_fake_lima(homebrew_bin_dir)

    {output, 0} =
      System.cmd("bash", [@script_path, context.project_dir, vm_name, ""],
        env: [
          {"HOMEBREW_PREFIX", homebrew_prefix},
          {"PATH", "/usr/bin:/bin"},
          {"TEST_CODEX_INSTALLED", "1"},
          {"TEST_NODE_INSTALLED", "1"},
          {"TEST_SETUP_FILE", context.setup_file},
          {"TEST_TRACE_FILE", context.trace_file},
          {"TEST_VM_EXISTS", "1"},
          {"TEST_VM_NAME", vm_name}
        ],
        stderr_to_stdout: true
      )

    assert File.read!(context.trace_file) =~ "limactl:list --format {{.Name}}\n"
    assert File.read!(context.trace_file) =~ "lima:codex\n"
    assert output =~ "[bootstrap] launching codex-cli"
  end

  test "finds Lima host commands in the default Linuxbrew prefix when PATH omits them", context do
    vm_name = "atlas-vm"
    linuxbrew_prefix = Path.join(context.tmp_dir, "linuxbrew")
    linuxbrew_home = Path.join(linuxbrew_prefix, ".linuxbrew")
    linuxbrew_bin_dir = Path.join(linuxbrew_home, "bin")

    File.mkdir_p!(linuxbrew_bin_dir)
    write_fake_limactl(linuxbrew_bin_dir)
    write_fake_lima(linuxbrew_bin_dir)

    {output, 0} =
      System.cmd("bash", [@script_path, context.project_dir, vm_name, ""],
        env: [
          {"HOME", linuxbrew_prefix},
          {"PATH", "/usr/bin:/bin"},
          {"TEST_CODEX_INSTALLED", "1"},
          {"TEST_NODE_INSTALLED", "1"},
          {"TEST_SETUP_FILE", context.setup_file},
          {"TEST_TRACE_FILE", context.trace_file},
          {"TEST_VM_EXISTS", "1"},
          {"TEST_VM_NAME", vm_name}
        ],
        stderr_to_stdout: true
      )

    assert File.read!(context.trace_file) =~ "limactl:list --format {{.Name}}\n"
    assert File.read!(context.trace_file) =~ "lima:codex\n"
    assert output =~ "[bootstrap] launching codex-cli"
  end

  defp shell_env(context, vm_name) do
    [
      {"LIMA_HOST_BIN_PATHS", context.fake_bin_dir},
      {"PATH", "#{context.fake_bin_dir}:#{System.get_env("PATH")}"},
      {"TEST_CODEX_INSTALLED", "1"},
      {"TEST_NODE_INSTALLED", "1"},
      {"TEST_SETUP_FILE", context.setup_file},
      {"TEST_TRACE_FILE", context.trace_file},
      {"TEST_VM_EXISTS", "1"},
      {"TEST_VM_NAME", vm_name}
    ]
  end

  defp write_fake_limactl(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "limactl")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      set -euo pipefail

      printf 'limactl:%s' "$1" >> "$TEST_TRACE_FILE"
      shift

      if (($# > 0)); then
        printf ' %s' "$@" >> "$TEST_TRACE_FILE"
      fi

      printf '\\n' >> "$TEST_TRACE_FILE"

      case "${1:-}" in
        list)
          if [[ "${TEST_VM_EXISTS:-0}" == "1" ]]; then
            printf '%s\\n' "${TEST_VM_NAME}"
          fi
          ;;
        start)
          ;;
        shell)
          shift

          if [[ "${1:-}" == "-y" ]]; then
            shift
          fi

          shift

          if [[ "${1:-}" == "command" && "${2:-}" == "-v" && "${3:-}" == "node" ]]; then
            [[ "${TEST_NODE_INSTALLED:-0}" == "1" ]]
          elif [[ "${1:-}" == "npm" && "${2:-}" == "list" && "${3:-}" == "-g" && "${4:-}" == "@openai/codex" ]]; then
            [[ "${TEST_CODEX_INSTALLED:-0}" == "1" ]]
          fi
          ;;
      esac
      """
    )

    File.chmod!(path, 0o755)
  end

  defp write_fake_lima(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "lima")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      set -euo pipefail

      case "${1:-}" in
        bash)
          printf 'lima:bash\\n' >> "$TEST_TRACE_FILE"
          printf '%s' "${3:-}" > "$TEST_SETUP_FILE"
          ;;
        codex)
          printf 'lima:codex\\n' >> "$TEST_TRACE_FILE"
          ;;
      esac
      """
    )

    File.chmod!(path, 0o755)
  end
end
