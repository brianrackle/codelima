defmodule TestLimaWeb.DashboardLiveTest do
  use TestLimaWeb.ConnCase

  alias TestLima.RuntimePaths
  alias TestLima.Workspace

  import Phoenix.LiveViewTest
  import TestLima.WorkspaceFixtures

  setup do
    lima_home =
      Path.join(System.tmp_dir!(), "test_lima_lima_home_#{System.unique_integer([:positive])}")

    Application.put_env(:test_lima, :lima_home, lima_home)

    on_exit(fn ->
      Application.delete_env(:test_lima, :lima_home)
      File.rm_rf!(lima_home)
    end)

    :ok
  end

  test "renders tracked projects and threads", %{conn: conn} do
    project = project_fixture(%{name: "Atlas"})
    thread = thread_fixture(%{project: project, title: "Atlas Thread"})

    {:ok, _view, html} = live(conn, ~p"/threads/#{thread.id}")

    assert html =~ "Run Codex threads inside isolated Lima VMs."
    assert html =~ "Atlas"
    assert html =~ "Atlas Thread"
    assert html =~ thread.vm_name
  end

  test "shows a Lima serial log tab for the selected thread", %{conn: conn} do
    project = project_fixture(%{name: "Atlas"})
    thread = thread_fixture(%{project: project, title: "Atlas Thread"})
    instance_dir = RuntimePaths.lima_instance_dir(thread.vm_name)

    File.mkdir_p!(instance_dir)
    File.write!(Path.join(instance_dir, "serial.log"), "boot line 1\nboot line 2\n")

    {:ok, view, _html} = live(conn, ~p"/threads/#{thread.id}")

    assert has_element?(view, "#thread-tab-terminal")
    assert has_element?(view, "#thread-tab-lima-logs")

    view
    |> element("#thread-tab-lima-logs")
    |> render_click()

    assert has_element?(view, "#lima-log-output-#{thread.id}")
    refute has_element?(view, "#terminal-container-#{thread.id}")
    assert render(view) =~ "boot line 2"
    assert render(view) =~ RuntimePaths.lima_serial_log_glob(thread.vm_name)
  end

  test "creates a project with custom VM setup commands", %{conn: conn} do
    directory =
      System.tmp_dir!()
      |> Path.join("dashboard_project_#{System.unique_integer([:positive])}")

    File.mkdir_p!(directory)

    setup_commands = "mise install\nmix ecto.setup"

    {:ok, view, _html} = live(conn, ~p"/")

    assert has_element?(view, "#project-form")

    view
    |> element("#project-form")
    |> render_submit(%{
      "project" => %{
        "directory" => directory,
        "name" => "Orbital",
        "setup_commands" => setup_commands
      }
    })

    [project] = Workspace.list_projects()

    assert project.setup_commands == setup_commands
    assert has_element?(view, "#project-card-#{project.id}")
    assert has_element?(view, "#project-setup-commands-#{project.id}")
  end

  test "edits a project's custom VM setup commands", %{conn: conn} do
    project = project_fixture(%{name: "Atlas", setup_commands: "mix deps.get"})

    {:ok, view, _html} = live(conn, ~p"/")

    view
    |> element("#project-edit-#{project.id}")
    |> render_click()

    assert has_element?(view, "#project-cancel-edit")

    view
    |> element("#project-form")
    |> render_submit(%{
      "project" => %{
        "directory" => project.directory,
        "name" => project.name,
        "setup_commands" => "mise install\nmix setup"
      }
    })

    updated_project = Workspace.get_project!(project.id)

    assert updated_project.setup_commands == "mise install\nmix setup"
    assert has_element?(view, "#project-setup-commands-#{project.id}")
  end
end
