defmodule TestLima.SetupScriptTest do
  use ExUnit.Case, async: false

  @script_path Path.expand("../../script/setup", __DIR__)

  setup do
    tmp_dir =
      System.tmp_dir!()
      |> Path.join("test_lima_setup_script_#{System.unique_integer([:positive])}")

    fake_bin_dir = Path.join(tmp_dir, "bin")
    install_prefix = Path.join(tmp_dir, "install")
    trace_file = Path.join(tmp_dir, "trace.log")

    File.mkdir_p!(fake_bin_dir)

    write_dummy_executable(fake_bin_dir, "elixir")
    write_dummy_executable(fake_bin_dir, "mix")
    write_dummy_executable(fake_bin_dir, "node")
    write_dummy_executable(fake_bin_dir, "npm")
    write_fake_sudo(fake_bin_dir)
    write_fake_apt_cache(fake_bin_dir)
    write_fake_apt_get(fake_bin_dir)
    write_fake_brew(fake_bin_dir)
    write_fake_curl(fake_bin_dir)
    write_fake_tar(fake_bin_dir)

    on_exit(fn -> File.rm_rf!(tmp_dir) end)

    {:ok, fake_bin_dir: fake_bin_dir, install_prefix: install_prefix, trace_file: trace_file}
  end

  test "installs Lima from the official archive on Linux without using Homebrew", context do
    platform =
      "#{String.trim(System.cmd("uname", ["-s"]) |> elem(0))}-#{String.trim(System.cmd("uname", ["-m"]) |> elem(0))}"

    {output, 0} =
      System.cmd("bash", [@script_path, "--skip-project-setup"],
        env: [
          {"LIMA_INSTALL_PREFIX", context.install_prefix},
          {"PATH", "#{context.fake_bin_dir}:/usr/bin:/bin"},
          {"TEST_TRACE_FILE", context.trace_file}
        ],
        stderr_to_stdout: true
      )

    trace = File.read!(context.trace_file)

    assert trace =~ "apt-get:update\n"
    assert trace =~ "apt-get:install -y"
    assert trace =~ "qemu-system-arm"
    assert trace =~ "curl:https://api.github.com/repos/lima-vm/lima/releases/latest\n"

    assert trace =~
             "curl:https://github.com/lima-vm/lima/releases/download/v1.2.3/lima-1.2.3-#{platform}.tar.gz\n"

    assert trace =~
             "curl:https://github.com/lima-vm/lima/releases/download/v1.2.3/lima-additional-guestagents-1.2.3-#{platform}.tar.gz\n"

    refute trace =~ "brew\n"

    assert File.exists?(Path.join(context.install_prefix, "bin/lima"))
    assert File.exists?(Path.join(context.install_prefix, "bin/limactl"))
    assert output =~ "[setup] Environment is ready"
  end

  defp write_dummy_executable(fake_bin_dir, name) do
    path = Path.join(fake_bin_dir, name)

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      exit 0
      """
    )

    File.chmod!(path, 0o755)
  end

  defp write_fake_sudo(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "sudo")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      exec "$@"
      """
    )

    File.chmod!(path, 0o755)
  end

  defp write_fake_apt_cache(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "apt-cache")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      set -euo pipefail

      if [[ "${1:-}" == "show" && "${2:-}" == "lima" ]]; then
        exit 1
      fi

      exit 0
      """
    )

    File.chmod!(path, 0o755)
  end

  defp write_fake_apt_get(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "apt-get")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      set -euo pipefail

      printf 'apt-get:%s' "${1:-}" >> "$TEST_TRACE_FILE"
      shift

      if (($# > 0)); then
        printf ' %s' "$@" >> "$TEST_TRACE_FILE"
      fi

      printf '\\n' >> "$TEST_TRACE_FILE"
      exit 0
      """
    )

    File.chmod!(path, 0o755)
  end

  defp write_fake_brew(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "brew")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      set -euo pipefail

      printf 'brew\\n' >> "$TEST_TRACE_FILE"
      exit 99
      """
    )

    File.chmod!(path, 0o755)
  end

  defp write_fake_curl(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "curl")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      set -euo pipefail

      target=""
      url=""

      while [[ "$#" -gt 0 ]]; do
        case "$1" in
          -o)
            target="$2"
            shift 2
            ;;
          -fsSL)
            shift
            ;;
          *)
            url="$1"
            shift
            ;;
        esac
      done

      printf 'curl:%s\\n' "$url" >> "$TEST_TRACE_FILE"

      if [[ -n "$target" ]]; then
        printf 'archive' > "$target"
      else
        printf '{"tag_name":"v1.2.3"}'
      fi
      """
    )

    File.chmod!(path, 0o755)
  end

  defp write_fake_tar(fake_bin_dir) do
    path = Path.join(fake_bin_dir, "tar")

    File.write!(
      path,
      """
      #!/usr/bin/env bash
      set -euo pipefail

      prefix=""

      while [[ "$#" -gt 0 ]]; do
        case "$1" in
          -C)
            prefix="$2"
            shift 2
            ;;
          *)
            shift
            ;;
        esac
      done

      mkdir -p "$prefix/bin"

      for command_name in lima limactl; do
        printf '#!/usr/bin/env bash\\nexit 0\\n' > "$prefix/bin/$command_name"
        chmod +x "$prefix/bin/$command_name"
      done
      """
    )

    File.chmod!(path, 0o755)
  end
end
