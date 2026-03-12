defmodule TestLima.Workspace.Project do
  use Ecto.Schema
  import Ecto.Changeset

  schema "projects" do
    field :name, :string
    field :directory, :string
    field :slug, :string
    field :setup_commands, :string, default: ""
    has_many :threads, TestLima.Workspace.Thread

    timestamps(type: :utc_datetime)
  end

  @doc false
  def changeset(project, attrs) do
    project
    |> cast(attrs, [:name, :directory, :slug, :setup_commands])
    |> normalize_directory()
    |> normalize_setup_commands()
    |> put_default_name()
    |> put_default_slug()
    |> validate_required([:name, :directory, :slug])
    |> validate_length(:name, min: 2, max: 80)
    |> validate_length(:slug, min: 2, max: 80)
    |> validate_length(:setup_commands, max: 20_000)
    |> validate_format(:slug, ~r/^[a-z0-9-]+$/)
    |> validate_change(:directory, fn :directory, directory ->
      cond do
        Path.type(directory) != :absolute ->
          [directory: "must resolve to an absolute path"]

        not File.dir?(directory) ->
          [directory: "must point to an existing directory"]

        true ->
          []
      end
    end)
    |> unique_constraint(:directory)
    |> unique_constraint(:slug)
  end

  defp normalize_directory(changeset) do
    update_change(changeset, :directory, &(&1 |> String.trim() |> Path.expand()))
  end

  defp normalize_setup_commands(changeset) do
    update_change(changeset, :setup_commands, &String.trim/1)
  end

  defp put_default_name(changeset) do
    case get_field(changeset, :name) do
      name when is_binary(name) ->
        trimmed_name = String.trim(name)

        if trimmed_name == "" do
          put_name_from_directory(changeset)
        else
          put_change(changeset, :name, trimmed_name)
        end

      _ ->
        put_name_from_directory(changeset)
    end
  end

  defp put_default_slug(changeset) do
    case get_field(changeset, :slug) do
      slug when is_binary(slug) ->
        trimmed_slug = String.trim(slug)

        if trimmed_slug == "" do
          put_slug_from_name(changeset)
        else
          put_change(changeset, :slug, slugify(trimmed_slug))
        end

      _ ->
        put_slug_from_name(changeset)
    end
  end

  defp put_name_from_directory(changeset) do
    case get_field(changeset, :directory) do
      directory when is_binary(directory) and directory != "" ->
        put_change(changeset, :name, directory |> Path.basename() |> normalize_name())

      _ ->
        changeset
    end
  end

  defp put_slug_from_name(changeset) do
    case get_field(changeset, :name) do
      name when is_binary(name) and name != "" ->
        put_change(changeset, :slug, slugify(name))

      _ ->
        changeset
    end
  end

  defp normalize_name(""), do: "project"
  defp normalize_name(name), do: name

  defp slugify(value) do
    value
    |> String.downcase()
    |> String.trim()
    |> String.replace(~r/[^a-z0-9]+/u, "-")
    |> String.trim("-")
    |> case do
      "" -> "project"
      slug -> slug
    end
  end
end
