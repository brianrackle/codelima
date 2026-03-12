defmodule TestLima.Workspace.ThreadEvent do
  use Ecto.Schema
  import Ecto.Changeset

  schema "thread_events" do
    field :kind, :string
    field :message, :string
    field :metadata, :map, default: %{}

    belongs_to :thread, TestLima.Workspace.Thread

    timestamps(type: :utc_datetime)
  end

  @doc false
  def changeset(thread_event, attrs) do
    thread_event
    |> cast(attrs, [:kind, :message, :metadata, :thread_id])
    |> validate_required([:kind, :message, :thread_id])
    |> validate_length(:kind, min: 3, max: 80)
    |> validate_length(:message, min: 2, max: 500)
    |> foreign_key_constraint(:thread_id)
  end
end
