defmodule TestLima.WorkspaceTest do
  use TestLima.DataCase

  alias TestLima.Workspace
  alias TestLima.Workspace.{Project, Thread}

  import TestLima.WorkspaceFixtures

  describe "projects" do
    test "list_projects/0 returns persisted projects with preloaded threads" do
      project = project_fixture()
      thread = thread_fixture(%{project: project})

      [listed_project] = Workspace.list_projects()

      assert listed_project.id == project.id
      assert Enum.map(listed_project.threads, & &1.id) == [thread.id]
    end

    test "list_projects/0 reloads the repo module if it was purged" do
      project = project_fixture()

      :code.purge(TestLima.Repo)
      :code.delete(TestLima.Repo)

      [listed_project] = Workspace.list_projects()

      assert listed_project.id == project.id
    after
      Code.ensure_loaded!(TestLima.Repo)
    end

    test "create_project/1 expands directories and derives the slug" do
      directory =
        System.tmp_dir!()
        |> Path.join("test_lima_relative_project_#{System.unique_integer([:positive])}")

      File.mkdir_p!(directory)

      relative_directory = Path.relative_to_cwd(directory)

      assert {:ok, %Project{} = project} =
               Workspace.create_project(%{directory: relative_directory, name: "Orbit Tools"})

      assert project.directory == Path.expand(relative_directory)
      assert project.slug == "orbit-tools"
    end

    test "create_project/1 persists custom setup commands" do
      directory =
        System.tmp_dir!()
        |> Path.join("test_lima_setup_project_#{System.unique_integer([:positive])}")

      File.mkdir_p!(directory)

      setup_commands = "\n  mise install\n  mix setup\n"

      assert {:ok, %Project{} = project} =
               Workspace.create_project(%{
                 directory: directory,
                 name: "Setup Project",
                 setup_commands: setup_commands
               })

      assert project.setup_commands == "mise install\n  mix setup"
    end

    test "create_project/1 rejects missing directories" do
      missing_directory =
        Path.join(System.tmp_dir!(), "missing_#{System.unique_integer([:positive])}")

      assert {:error, changeset} = Workspace.create_project(%{directory: missing_directory})
      assert "must point to an existing directory" in errors_on(changeset).directory
    end

    test "update_project/2 updates setup commands" do
      project = project_fixture()

      assert {:ok, %Project{} = updated_project} =
               Workspace.update_project(project, %{setup_commands: "mise install\nmix setup"})

      assert updated_project.setup_commands == "mise install\nmix setup"
    end
  end

  describe "threads" do
    test "create_thread/2 creates a VM-backed thread and records an event" do
      project = project_fixture()

      assert {:ok, %Thread{} = thread} =
               Workspace.create_thread(project, %{title: "Refactor auth"})

      assert thread.project_id == project.id
      assert thread.status == "created"
      assert String.starts_with?(thread.vm_name, "#{project.slug}-")

      reloaded_thread = Workspace.get_thread!(thread.id)
      assert Enum.any?(reloaded_thread.events, &(&1.kind == "thread.created"))
    end

    test "record_event/4 appends events to a thread timeline" do
      thread = thread_fixture()
      assert {:ok, _event} = Workspace.record_event(thread, "thread.running", "Thread is ready")

      events = Workspace.list_thread_events(thread)
      assert Enum.any?(events, &(&1.message == "Thread is ready"))
    end
  end
end
