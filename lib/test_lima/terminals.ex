defmodule TestLima.Terminals do
  @moduledoc false

  alias TestLima.Workspace
  alias TestLima.Terminals.ThreadSession

  def start_thread(thread_id) when is_integer(thread_id) do
    case Registry.lookup(TestLima.Terminals.Registry, thread_id) do
      [{pid, _value}] ->
        {:ok, pid}

      [] ->
        spec = {ThreadSession, thread_id}
        DynamicSupervisor.start_child(TestLima.Terminals.SessionSupervisor, spec)
    end
  end

  def stop_thread(thread_id) when is_integer(thread_id) do
    case Registry.lookup(TestLima.Terminals.Registry, thread_id) do
      [{pid, _value}] ->
        GenServer.call(pid, :stop, 15_000)

      [] ->
        case Workspace.get_thread(thread_id) do
          nil ->
            {:error, :not_found}

          thread ->
            Workspace.set_thread_runtime(thread, %{
              status: "stopped",
              terminal_port: nil,
              access_token: nil
            })
        end
    end
  end
end
