defmodule TestLima.Terminals.Supervisor do
  @moduledoc false

  use Supervisor

  def start_link(opts \\ []) do
    Supervisor.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    children =
      [
        {Registry, keys: :unique, name: TestLima.Terminals.Registry},
        {DynamicSupervisor, strategy: :one_for_one, name: TestLima.Terminals.SessionSupervisor}
      ] ++ maybe_reconciler()

    Supervisor.init(children, strategy: :one_for_all)
  end

  defp maybe_reconciler do
    if Application.get_env(:test_lima, :enable_terminal_reconciler, true) do
      [TestLima.Terminals.Reconciler]
    else
      []
    end
  end
end
