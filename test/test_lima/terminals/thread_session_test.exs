defmodule TestLima.Terminals.ThreadSessionTest do
  use TestLima.DataCase, async: false

  alias TestLima.Workspace
  alias TestLima.Workspace.{Thread, ThreadEvent}
  alias TestLima.Terminals

  import TestLima.WorkspaceFixtures

  setup do
    original_path = System.get_env("PATH")
    original_lima_host_bin_paths = System.get_env("LIMA_HOST_BIN_PATHS")
    fake_bin_dir = unique_fake_bin_dir()

    node_executable =
      System.find_executable("node") || raise "node is required for terminal tests"

    node_dir = Path.dirname(node_executable)

    File.mkdir_p!(fake_bin_dir)
    write_fake_limactl(fake_bin_dir)

    System.put_env(
      "PATH",
      Enum.join(Enum.uniq([fake_bin_dir, node_dir, "/usr/bin", "/bin"]), ":")
    )

    System.put_env("LIMA_HOST_BIN_PATHS", fake_bin_dir)

    on_exit(fn ->
      restore_path(original_path)
      restore_env("LIMA_HOST_BIN_PATHS", original_lima_host_bin_paths)
      File.rm_rf!(fake_bin_dir)
    end)

    :ok
  end

  test "preserves detailed bootstrap failures as the thread error" do
    project = project_fixture()
    thread = thread_fixture(%{project: project})
    thread_id = thread.id
    missing_command_message = "Missing required command: lima"

    Workspace.subscribe()

    assert {:ok, pid} = Terminals.start_thread(thread_id)
    ref = Process.monitor(pid)

    assert_receive {:thread_saved, %Thread{id: ^thread_id, status: "booting"}}, 1_000

    assert_receive {:thread_saved,
                    %Thread{
                      id: ^thread_id,
                      status: "failed",
                      last_error: ^missing_command_message
                    }},
                   10_000

    assert_receive {:thread_event, ^thread_id,
                    %ThreadEvent{
                      kind: "thread.failed",
                      message: ^missing_command_message
                    }},
                   10_000

    assert %Thread{status: "failed", last_error: ^missing_command_message} =
             Workspace.get_thread!(thread_id)

    assert_receive {:DOWN, ^ref, :process, ^pid, :normal}, 10_000
    refute_receive {:thread_saved, %Thread{id: ^thread_id, status: "booting"}}, 500
  end

  defp unique_fake_bin_dir do
    System.tmp_dir!()
    |> Path.join("test_lima_thread_session_bin_#{System.unique_integer([:positive])}")
  end

  defp restore_path(nil), do: System.delete_env("PATH")
  defp restore_path(path), do: System.put_env("PATH", path)

  defp restore_env(name, nil), do: System.delete_env(name)
  defp restore_env(name, value), do: System.put_env(name, value)

  defp write_fake_limactl(fake_bin_dir) do
    fake_bin_dir
    |> Path.join("limactl")
    |> File.write!("""
    #!/usr/bin/env bash
    printf 'Missing required command: limactl\\n' >&2
    exit 127
    """)

    File.chmod!(Path.join(fake_bin_dir, "limactl"), 0o755)
  end
end
