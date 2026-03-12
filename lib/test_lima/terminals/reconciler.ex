defmodule TestLima.Terminals.Reconciler do
  @moduledoc false

  use GenServer

  alias TestLima.RuntimePaths
  alias TestLima.Workspace

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(state) do
    send(self(), :reconcile)
    {:ok, state}
  end

  @impl true
  def handle_info(:reconcile, state) do
    RuntimePaths.ensure_directories!()
    Workspace.reconcile_runtime!()
    {:noreply, state}
  end
end
