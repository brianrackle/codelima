defmodule TestLima.Terminals.ThreadSession do
  @moduledoc false

  use GenServer

  require Logger

  alias TestLima.RuntimePaths
  alias TestLima.Workspace

  def child_spec(thread_id) do
    %{
      id: {__MODULE__, thread_id},
      start: {__MODULE__, :start_link, [thread_id]},
      restart: :temporary
    }
  end

  def start_link(thread_id) do
    GenServer.start_link(__MODULE__, thread_id, name: via(thread_id))
  end

  @impl true
  def init(thread_id) do
    {:ok,
     %{
       thread_id: thread_id,
       access_token: nil,
       bridge_port: nil,
       last_bridge_error: nil,
       log_path: nil,
       output_buffer: "",
       port: nil,
       os_pid: nil,
       stopping?: false,
       vm_name: nil
     }, {:continue, :boot}}
  end

  @impl true
  def handle_continue(:boot, state) do
    RuntimePaths.ensure_directories!()
    thread = Workspace.get_thread!(state.thread_id)
    bridge_port = allocate_port()
    access_token = generate_token()
    log_path = RuntimePaths.thread_log_path(thread.id, thread.vm_name)

    {:ok, _thread} =
      Workspace.set_thread_runtime(thread, %{
        status: "booting",
        terminal_port: bridge_port,
        access_token: access_token,
        log_path: log_path,
        last_error: nil
      })

    Workspace.record_event(
      thread,
      "thread.booting",
      "Starting Lima thread #{thread.vm_name}",
      %{port: bridge_port, log_path: log_path}
    )

    port = open_bridge_port(thread, bridge_port, access_token, log_path)

    os_pid =
      case Port.info(port, :os_pid) do
        {:os_pid, pid} -> pid
        _ -> nil
      end

    {:noreply,
     %{
       state
       | access_token: access_token,
         bridge_port: bridge_port,
         log_path: log_path,
         os_pid: os_pid,
         port: port,
         vm_name: thread.vm_name
     }}
  rescue
    error ->
      Logger.error("failed to boot thread #{state.thread_id}: #{Exception.message(error)}")

      if thread = Workspace.get_thread(state.thread_id) do
        Workspace.set_thread_runtime(thread, %{
          status: "failed",
          terminal_port: nil,
          access_token: nil,
          last_error: Exception.message(error)
        })

        Workspace.record_event(
          thread,
          "thread.failed",
          "The terminal bridge failed to start.",
          %{error: Exception.message(error)}
        )
      end

      {:stop, :boot_failed, state}
  end

  @impl true
  def handle_call(:stop, _from, state) do
    if thread = Workspace.get_thread(state.thread_id) do
      Workspace.record_event(thread, "thread.stopping", "Stopping thread on request.")
    end

    stop_bridge(state)
    {:reply, {:ok, :stopping}, %{state | stopping?: true}}
  end

  @impl true
  def handle_info({_port, {:data, {:eol, line}}}, state) do
    {:noreply, handle_bridge_line(String.trim(line), state)}
  end

  @impl true
  def handle_info({_port, {:data, {:noeol, line}}}, state) do
    {:noreply, %{state | output_buffer: state.output_buffer <> line}}
  end

  @impl true
  def handle_info({_port, {:exit_status, exit_status}}, state) do
    thread = Workspace.get_thread(state.thread_id)

    if thread do
      status = if state.stopping? or exit_status == 0, do: "stopped", else: "failed"
      last_error = exit_last_error(thread, state, exit_status)

      Workspace.set_thread_runtime(thread, %{
        status: status,
        terminal_port: nil,
        access_token: nil,
        last_error: last_error
      })

      record_exit_event(thread, status, exit_status, state.last_bridge_error)
    end

    {:stop, :normal, %{state | port: nil}}
  end

  defp handle_bridge_line("", state), do: state

  defp handle_bridge_line(line, state) do
    line =
      case state.output_buffer do
        "" -> line
        buffer -> String.trim(buffer <> line)
      end

    case Jason.decode(line) do
      {:ok, %{"event" => "ready"}} ->
        if thread = Workspace.get_thread(state.thread_id) do
          Workspace.set_thread_runtime(thread, %{status: "running", last_error: nil})

          Workspace.record_event(
            thread,
            "thread.running",
            "Thread is ready for terminal connections."
          )
        end

        %{state | output_buffer: ""}

      {:ok, %{"event" => "status", "message" => message}} ->
        if thread = Workspace.get_thread(state.thread_id) do
          Workspace.record_event(thread, "thread.status", message)
        end

        %{state | output_buffer: ""}

      {:ok, %{"event" => "error", "message" => message}} ->
        if thread = Workspace.get_thread(state.thread_id) do
          Workspace.set_thread_runtime(thread, %{status: "failed", last_error: message})
          Workspace.record_event(thread, "thread.failed", message)
        end

        %{state | output_buffer: "", last_bridge_error: message}

      _ ->
        Logger.debug("thread bridge #{state.thread_id}: #{line}")
        %{state | output_buffer: ""}
    end
  end

  defp open_bridge_port(thread, bridge_port, access_token, log_path) do
    executable = System.find_executable("node") || raise "node is not installed"

    args = [
      RuntimePaths.node_bridge_path(),
      "--port",
      Integer.to_string(bridge_port),
      "--token",
      access_token,
      "--project-dir",
      thread.project.directory,
      "--vm-name",
      thread.vm_name,
      "--log-path",
      log_path,
      "--setup-commands",
      thread.project.setup_commands || "",
      "--bootstrap",
      RuntimePaths.bootstrap_script_path()
    ]

    Port.open({:spawn_executable, executable}, [
      :binary,
      :exit_status,
      :hide,
      :stderr_to_stdout,
      :use_stdio,
      {:args, args},
      {:line, 4096}
    ])
  end

  defp stop_bridge(%{os_pid: nil}), do: :ok

  defp stop_bridge(%{os_pid: os_pid}) do
    System.cmd("kill", ["-TERM", Integer.to_string(os_pid)])
    :ok
  rescue
    _ -> :ok
  end

  defp allocate_port do
    {:ok, socket} = :gen_tcp.listen(0, [:binary, packet: :raw, active: false, ip: {127, 0, 0, 1}])
    {:ok, port_number} = :inet.port(socket)
    :gen_tcp.close(socket)
    port_number
  end

  defp generate_token do
    18
    |> :crypto.strong_rand_bytes()
    |> Base.url_encode64(padding: false)
  end

  defp via(thread_id) do
    {:via, Registry, {TestLima.Terminals.Registry, thread_id}}
  end

  defp exit_last_error(_thread, %{stopping?: true}, _exit_status), do: nil
  defp exit_last_error(_thread, _state, 0), do: nil

  defp exit_last_error(thread, state, exit_status) do
    thread.last_error || state.last_bridge_error ||
      "Thread exited unexpectedly with status #{exit_status}."
  end

  defp record_exit_event(thread, "stopped", _exit_status, _last_bridge_error) do
    Workspace.record_event(thread, "thread.stopped", "Thread stopped.")
  end

  defp record_exit_event(_thread, "failed", _exit_status, last_bridge_error)
       when is_binary(last_bridge_error) and last_bridge_error != "" do
    :ok
  end

  defp record_exit_event(thread, "failed", exit_status, _last_bridge_error) do
    Workspace.record_event(
      thread,
      "thread.failed",
      "Thread exited unexpectedly with status #{exit_status}.",
      %{exit_status: exit_status}
    )
  end
end
