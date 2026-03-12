defmodule TestLima.Terminals.LogsTest do
  use ExUnit.Case, async: false

  alias TestLima.RuntimePaths
  alias TestLima.Terminals.Logs

  setup do
    lima_home =
      Path.join(System.tmp_dir!(), "test_lima_logs_home_#{System.unique_integer([:positive])}")

    Application.put_env(:test_lima, :lima_home, lima_home)

    on_exit(fn ->
      Application.delete_env(:test_lima, :lima_home)
      File.rm_rf!(lima_home)
    end)

    :ok
  end

  test "tail_lima_serial/2 returns the newest serial log lines" do
    vm_name = "atlas-vm"
    instance_dir = RuntimePaths.lima_instance_dir(vm_name)

    File.mkdir_p!(instance_dir)
    File.write!(Path.join(instance_dir, "serial.log"), "one\ntwo\nthree\n")

    assert Logs.tail_lima_serial(%{vm_name: vm_name}, 2) == ["two", "three"]

    assert Logs.lima_serial_tail_command(%{vm_name: vm_name}) ==
             "tail -f '#{RuntimePaths.lima_serial_log_glob(vm_name)}'"
  end
end
