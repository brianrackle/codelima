defmodule TestLima.Repo.Migrations.CreateThreads do
  use Ecto.Migration

  def change do
    create table(:threads) do
      add :title, :string, null: false
      add :status, :string, null: false, default: "created"
      add :vm_name, :string, null: false
      add :terminal_port, :integer
      add :access_token, :string
      add :log_path, :string
      add :last_error, :text
      add :project_id, references(:projects, on_delete: :delete_all), null: false

      timestamps(type: :utc_datetime)
    end

    create index(:threads, [:project_id])
    create unique_index(:threads, [:vm_name])
    create unique_index(:threads, [:access_token])
  end
end
