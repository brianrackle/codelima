defmodule TestLima.RuntimePaths do
  @moduledoc false

  @app :test_lima

  def project_root do
    Application.fetch_env!(@app, :project_root)
  end

  def state_dir do
    Application.fetch_env!(@app, :state_dir)
  end

  def lima_home do
    Application.get_env(@app, :lima_home, Path.join(System.user_home!(), ".lima"))
  end

  def thread_logs_dir do
    Path.join(state_dir(), "thread_logs")
  end

  def node_bridge_path do
    Path.join(project_root(), "priv/node/thread_terminal.mjs")
  end

  def bootstrap_script_path do
    Path.join(project_root(), "priv/scripts/run_lima_codex.sh")
  end

  def thread_log_path(thread_id, vm_name) do
    safe_vm_name = vm_name |> to_string() |> String.replace(~r/[^a-zA-Z0-9_-]/, "-")
    Path.join(thread_logs_dir(), "thread-#{thread_id}-#{safe_vm_name}.jsonl")
  end

  def lima_instance_dir(vm_name) do
    Path.join(lima_home(), to_string(vm_name))
  end

  def lima_serial_log_glob(vm_name) do
    Path.join(lima_instance_dir(vm_name), "serial*.log")
  end

  def ensure_directories! do
    File.mkdir_p!(state_dir())
    File.mkdir_p!(thread_logs_dir())
  end
end
