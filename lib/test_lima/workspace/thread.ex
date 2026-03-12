defmodule TestLima.Workspace.Thread do
  use Ecto.Schema
  import Ecto.Changeset

  @statuses ~w(created booting running stopped failed)

  schema "threads" do
    field :title, :string
    field :status, :string, default: "created"
    field :vm_name, :string
    field :terminal_port, :integer
    field :access_token, :string
    field :log_path, :string
    field :last_error, :string

    belongs_to :project, TestLima.Workspace.Project
    has_many :events, TestLima.Workspace.ThreadEvent

    timestamps(type: :utc_datetime)
  end

  def statuses, do: @statuses

  @doc false
  def changeset(thread, attrs) do
    thread
    |> cast(attrs, [
      :title,
      :status,
      :vm_name,
      :terminal_port,
      :access_token,
      :log_path,
      :last_error,
      :project_id
    ])
    |> validate_required([:title, :status, :vm_name, :project_id])
    |> validate_length(:title, min: 2, max: 120)
    |> validate_inclusion(:status, @statuses)
    |> foreign_key_constraint(:project_id)
    |> unique_constraint(:vm_name)
    |> unique_constraint(:access_token)
  end

  def runtime_changeset(thread, attrs) do
    thread
    |> cast(attrs, [:status, :terminal_port, :access_token, :log_path, :last_error])
    |> validate_inclusion(:status, @statuses)
    |> unique_constraint(:access_token)
  end
end
