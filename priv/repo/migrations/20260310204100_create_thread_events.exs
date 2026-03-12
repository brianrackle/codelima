defmodule TestLima.Repo.Migrations.CreateThreadEvents do
  use Ecto.Migration

  def change do
    create table(:thread_events) do
      add :kind, :string, null: false
      add :message, :text, null: false
      add :metadata, :map, null: false, default: %{}
      add :thread_id, references(:threads, on_delete: :delete_all), null: false

      timestamps(type: :utc_datetime)
    end

    create index(:thread_events, [:thread_id])
  end
end
