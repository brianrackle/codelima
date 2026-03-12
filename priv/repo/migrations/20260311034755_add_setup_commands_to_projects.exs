defmodule TestLima.Repo.Migrations.AddSetupCommandsToProjects do
  use Ecto.Migration

  def change do
    alter table(:projects) do
      add :setup_commands, :text, null: false, default: ""
    end
  end
end
