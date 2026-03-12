defmodule TestLima.WorkspaceFixtures do
  @moduledoc false

  alias TestLima.Workspace

  def project_fixture(attrs \\ %{}) do
    directory =
      attrs[:directory] ||
        attrs["directory"] ||
        unique_project_directory()

    name = attrs[:name] || attrs["name"] || "Example Project"

    {:ok, project} =
      attrs
      |> Enum.into(%{
        directory: directory,
        name: name
      })
      |> Workspace.create_project()

    project
  end

  def thread_fixture(attrs \\ %{}) do
    project = attrs[:project] || attrs["project"] || project_fixture()

    {:ok, thread} =
      attrs
      |> Map.new(fn {key, value} -> {key, value} end)
      |> Map.drop([:project, "project"])
      |> then(&Workspace.create_thread(project, &1))

    thread
  end

  defp unique_project_directory do
    directory =
      System.tmp_dir!()
      |> Path.join("test_lima_workspace_#{System.unique_integer([:positive])}")

    File.mkdir_p!(directory)
    directory
  end
end
