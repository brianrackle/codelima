defmodule TestLimaWeb.DashboardLive do
  use TestLimaWeb, :live_view

  alias TestLima.Terminals.Logs
  alias TestLima.Workspace
  alias TestLima.Workspace.Project

  @impl true
  def mount(_params, _session, socket) do
    if connected?(socket) do
      Workspace.subscribe()
      :timer.send_interval(1_000, self(), :poll_selected_thread)
    end

    {:ok,
     socket
     |> assign(:page_title, "Lima Codex Orchestrator")
     |> assign(:selected_thread_id, nil)
     |> assign(:selected_thread, nil)
     |> assign(:selected_thread_tab, "terminal")
     |> assign(:selected_lima_log_command, nil)
     |> assign(:selected_lima_log_lines, [])
     |> assign(:projects, [])
     |> assign(:transcript_preview, [])
     |> assign_project_form()
     |> refresh_workspace()}
  end

  @impl true
  def handle_params(params, _uri, socket) do
    selected_thread_id = parse_integer(params["id"])

    {:noreply,
     socket
     |> assign(:selected_thread_id, selected_thread_id)
     |> refresh_workspace()}
  end

  @impl true
  def handle_event("validate_project", %{"project" => params}, socket) do
    form =
      socket.assigns.project_form_project
      |> Workspace.change_project(params)
      |> Map.put(:action, :validate)
      |> to_form(as: :project)

    {:noreply, assign(socket, :project_form, form)}
  end

  @impl true
  def handle_event("save_project", %{"project" => params}, socket) do
    case persist_project(socket.assigns.project_form_project, params) do
      {:ok, _project} ->
        flash =
          if socket.assigns.project_form_project.id do
            "Project updated."
          else
            "Project added."
          end

        {:noreply,
         socket
         |> put_flash(:info, flash)
         |> assign_project_form()
         |> refresh_workspace()}

      {:error, changeset} ->
        {:noreply, assign(socket, :project_form, to_form(changeset, as: :project))}
    end
  end

  @impl true
  def handle_event("edit_project", %{"project_id" => project_id}, socket) do
    project = Workspace.get_project!(parse_integer(project_id))
    {:noreply, assign_project_form(socket, project)}
  end

  @impl true
  def handle_event("cancel_project_edit", _params, socket) do
    {:noreply, assign_project_form(socket)}
  end

  @impl true
  def handle_event("new_thread", %{"project_id" => project_id}, socket) do
    project = Workspace.get_project!(parse_integer(project_id))

    with {:ok, thread} <- Workspace.create_thread(project),
         {:ok, _thread} <- Workspace.start_thread(thread) do
      {:noreply,
       socket
       |> put_flash(:info, "Thread created and started.")
       |> push_patch(to: ~p"/threads/#{thread.id}")}
    else
      {:error, _reason} ->
        {:noreply, put_flash(socket, :error, "Unable to create the thread.")}
    end
  end

  @impl true
  def handle_event("start_thread", %{"thread_id" => thread_id}, socket) do
    thread = Workspace.get_thread!(parse_integer(thread_id))

    case Workspace.start_thread(thread) do
      {:ok, started_thread} ->
        {:noreply,
         socket
         |> put_flash(:info, "Thread starting.")
         |> push_patch(to: ~p"/threads/#{started_thread.id}")}

      {:error, _reason} ->
        {:noreply, put_flash(socket, :error, "Unable to start the thread.")}
    end
  end

  @impl true
  def handle_event("stop_thread", %{"thread_id" => thread_id}, socket) do
    thread = Workspace.get_thread!(parse_integer(thread_id))
    {:ok, _thread} = Workspace.stop_thread(thread)

    {:noreply, put_flash(socket, :info, "Thread stop requested.")}
  end

  @impl true
  def handle_event("delete_thread", %{"thread_id" => thread_id}, socket) do
    thread = Workspace.get_thread!(parse_integer(thread_id))
    {:ok, _thread} = Workspace.delete_thread(thread)

    socket =
      if socket.assigns.selected_thread_id == thread.id do
        push_patch(socket, to: ~p"/")
      else
        socket
      end

    {:noreply, put_flash(socket, :info, "Thread deleted.")}
  end

  @impl true
  def handle_event("select_thread_tab", %{"tab" => tab}, socket)
      when tab in ["terminal", "lima_logs"] do
    {:noreply, assign(socket, :selected_thread_tab, tab)}
  end

  @impl true
  def handle_event("select_thread_tab", _params, socket) do
    {:noreply, socket}
  end

  @impl true
  def handle_info(:poll_selected_thread, socket) do
    {:noreply, refresh_workspace(socket)}
  end

  @impl true
  def handle_info(_message, socket) do
    {:noreply, refresh_workspace(socket)}
  end

  @impl true
  def render(assigns) do
    ~H"""
    <div class="orchestrator-shell">
      <.flash kind={:info} flash={@flash} />
      <.flash kind={:error} flash={@flash} />

      <div class="mx-auto flex min-h-screen max-w-7xl flex-col gap-6 px-4 py-6 lg:px-8">
        <section class="orchestrator-hero">
          <div>
            <p class="orchestrator-kicker">Phoenix + Lima + Ghostty</p>
            <h1 class="orchestrator-title">Run Codex threads inside isolated Lima VMs.</h1>
            <p class="orchestrator-copy">
              Register a project directory, spin up a dedicated VM-backed thread, and reconnect to the
              same terminal session from a browser whenever you need to continue the work.
            </p>
          </div>

          <div class="orchestrator-hero-stats">
            <div>
              <span class="orchestrator-stat-label">Projects</span>
              <span class="orchestrator-stat-value">{Enum.count(@projects)}</span>
            </div>
            <div>
              <span class="orchestrator-stat-label">Threads</span>
              <span class="orchestrator-stat-value">{thread_count(@projects)}</span>
            </div>
            <div>
              <span class="orchestrator-stat-label">Active</span>
              <span class="orchestrator-stat-value">{active_thread_count(@projects)}</span>
            </div>
          </div>
        </section>

        <div class="grid gap-6 xl:grid-cols-[22rem,minmax(0,1fr)]">
          <aside class="space-y-6">
            <section class="orchestrator-panel">
              <div class="orchestrator-panel-header">
                <div>
                  <p class="orchestrator-eyebrow">Workspace</p>
                  <h2>{project_form_title(@project_form_project)}</h2>
                </div>
                <span class="orchestrator-chip">
                  {if @project_form_project.id, do: "Editing", else: "Persistent"}
                </span>
              </div>

              <.form
                for={@project_form}
                id="project-form"
                phx-change="validate_project"
                phx-submit="save_project"
                class="space-y-3"
              >
                <.input
                  field={@project_form[:directory]}
                  label="Directory"
                  placeholder="/Users/you/src/my-project"
                  autocomplete="off"
                />
                <.input
                  field={@project_form[:name]}
                  label="Display name"
                  placeholder="Optional"
                  autocomplete="off"
                />
                <.input
                  field={@project_form[:setup_commands]}
                  type="textarea"
                  label="VM setup commands"
                  rows="6"
                  placeholder="mise install\nmix deps.get\nmix ecto.setup"
                />
                <p class="text-xs leading-6 text-zinc-400">
                  Optional. These commands run inside every Lima VM for this project before Codex
                  starts.
                </p>

                <div class="flex flex-wrap gap-2">
                  <button
                    id="project-form-save"
                    type="submit"
                    class="orchestrator-button orchestrator-button-primary flex-1"
                  >
                    {project_submit_copy(@project_form_project)}
                  </button>
                  <button
                    :if={@project_form_project.id}
                    id="project-cancel-edit"
                    type="button"
                    class="orchestrator-button orchestrator-button-subtle"
                    phx-click="cancel_project_edit"
                  >
                    Cancel
                  </button>
                </div>
              </.form>
            </section>

            <section class="orchestrator-panel">
              <div class="orchestrator-panel-header">
                <div>
                  <p class="orchestrator-eyebrow">Inventory</p>
                  <h2>Projects and threads</h2>
                </div>
                <span class="orchestrator-chip">{thread_count(@projects)} tracked</span>
              </div>

              <div :if={@projects == []} class="orchestrator-empty">
                Add a project directory to create your first Lima-backed Codex thread.
              </div>

              <div :for={project <- @projects} class="space-y-4">
                <article id={"project-card-#{project.id}"} class="project-card">
                  <div class="flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <div class="flex flex-wrap items-center gap-2">
                        <h3>{project.name}</h3>
                        <span :if={project_setup_configured?(project)} class="orchestrator-chip">
                          custom setup
                        </span>
                      </div>
                      <p>{project.directory}</p>
                    </div>

                    <div class="flex flex-wrap justify-end gap-2">
                      <button
                        id={"project-edit-#{project.id}"}
                        type="button"
                        class="orchestrator-button orchestrator-button-subtle"
                        phx-click="edit_project"
                        phx-value-project_id={project.id}
                      >
                        Edit
                      </button>
                      <button
                        id={"project-new-thread-#{project.id}"}
                        type="button"
                        class="orchestrator-button orchestrator-button-subtle"
                        phx-click="new_thread"
                        phx-value-project_id={project.id}
                      >
                        New thread
                      </button>
                    </div>
                  </div>

                  <div :if={project_setup_configured?(project)} class="mt-4 space-y-2">
                    <p class="orchestrator-eyebrow">VM setup commands</p>
                    <pre
                      id={"project-setup-commands-#{project.id}"}
                      class="rounded-2xl border border-white/10 bg-black/20 p-4 font-mono text-xs leading-6 whitespace-pre-wrap break-words text-zinc-300"
                    >{project.setup_commands}</pre>
                  </div>

                  <div class="mt-4 space-y-3">
                    <div :if={project.threads == []} class="text-sm text-zinc-400">
                      No threads yet.
                    </div>

                    <div
                      :for={thread <- project.threads}
                      class={[
                        "thread-card",
                        thread.id == @selected_thread_id && "thread-card-selected"
                      ]}
                    >
                      <.link patch={~p"/threads/#{thread.id}"} class="flex-1 min-w-0">
                        <div class="flex items-center justify-between gap-3">
                          <div class="min-w-0">
                            <p class="truncate font-semibold text-zinc-100">{thread.title}</p>
                            <p class="truncate text-xs uppercase tracking-[0.24em] text-zinc-500">
                              {thread.vm_name}
                            </p>
                          </div>
                          <span class={["status-badge", status_badge_class(thread.status)]}>
                            {thread.status}
                          </span>
                        </div>
                      </.link>

                      <div class="thread-card-actions">
                        <button
                          :if={thread.status in ["created", "stopped", "failed"]}
                          type="button"
                          class="orchestrator-button orchestrator-button-subtle"
                          phx-click="start_thread"
                          phx-value-thread_id={thread.id}
                        >
                          Start
                        </button>
                        <button
                          :if={thread.status in ["booting", "running"]}
                          type="button"
                          class="orchestrator-button orchestrator-button-subtle"
                          phx-click="stop_thread"
                          phx-value-thread_id={thread.id}
                        >
                          Stop
                        </button>
                        <button
                          type="button"
                          class="orchestrator-button orchestrator-button-danger"
                          phx-click="delete_thread"
                          phx-value-thread_id={thread.id}
                          data-confirm="Delete this thread and its transcript?"
                        >
                          Delete
                        </button>
                      </div>
                    </div>
                  </div>
                </article>
              </div>
            </section>
          </aside>

          <div class="space-y-6">
            <section class="orchestrator-panel terminal-panel">
              <%= if @selected_thread do %>
                <div class="orchestrator-panel-header">
                  <div>
                    <p class="orchestrator-eyebrow">Thread workspace</p>
                    <h2>{@selected_thread.title}</h2>
                    <p class="terminal-meta">
                      {@selected_thread.project.name} · {@selected_thread.project.directory}
                    </p>
                  </div>

                  <div class="terminal-header-actions">
                    <span class={["status-badge", status_badge_class(@selected_thread.status)]}>
                      {@selected_thread.status}
                    </span>
                    <code class="terminal-vm-name">{@selected_thread.vm_name}</code>
                  </div>
                </div>

                <div class="thread-view-tabs" role="tablist" aria-label="Thread views">
                  <button
                    id="thread-tab-terminal"
                    type="button"
                    role="tab"
                    phx-click="select_thread_tab"
                    phx-value-tab="terminal"
                    aria-selected={@selected_thread_tab == "terminal"}
                    class={thread_tab_class(@selected_thread_tab == "terminal")}
                  >
                    <span>Terminal</span>
                    <span class="thread-view-tab-copy">Interactive Codex session</span>
                  </button>

                  <button
                    id="thread-tab-lima-logs"
                    type="button"
                    role="tab"
                    phx-click="select_thread_tab"
                    phx-value-tab="lima_logs"
                    aria-selected={@selected_thread_tab == "lima_logs"}
                    class={thread_tab_class(@selected_thread_tab == "lima_logs")}
                  >
                    <span>Lima logs</span>
                    <span class="thread-view-tab-copy">Read-only serial tail</span>
                  </button>
                </div>

                <div class="terminal-frame">
                  <div class="terminal-frame-bar">
                    <span class="terminal-light terminal-light-red"></span>
                    <span class="terminal-light terminal-light-amber"></span>
                    <span class="terminal-light terminal-light-green"></span>
                    <span class="ml-3 truncate text-sm text-zinc-400">
                      <%= if @selected_thread_tab == "terminal" do %>
                        {terminal_status_copy(@selected_thread)}
                      <% else %>
                        {log_status_copy(@selected_lima_log_command)}
                      <% end %>
                    </span>
                  </div>

                  <%= if @selected_thread_tab == "terminal" do %>
                    <div
                      id={"terminal-container-#{@selected_thread.id}"}
                      class="terminal-stage"
                      phx-hook="GhosttyTerminal"
                      data-port={@selected_thread.terminal_port}
                      data-status={@selected_thread.status}
                      data-terminal-id={@selected_thread.id}
                      data-token={@selected_thread.access_token || ""}
                    >
                      <div id={"terminal-canvas-#{@selected_thread.id}"} phx-update="ignore">
                        <div class="terminal-empty">
                          <%= if @selected_thread.status in ["created", "stopped", "failed"] do %>
                            Start the thread to boot the Lima VM and attach `codex`.
                          <% else %>
                            Connecting to the terminal bridge...
                          <% end %>
                        </div>
                      </div>
                    </div>
                  <% else %>
                    <div class="terminal-log-stage">
                      <div class="terminal-log-summary">
                        <div>
                          <p class="orchestrator-eyebrow">Host-side serial console</p>
                          <code :if={@selected_lima_log_command} class="lima-log-command">
                            {@selected_lima_log_command}
                          </code>
                        </div>
                        <span class="orchestrator-chip">
                          {Enum.count(@selected_lima_log_lines)} lines
                        </span>
                      </div>

                      <%= if @selected_lima_log_lines == [] do %>
                        <div class="terminal-empty">
                          Lima serial logs will appear here once the instance starts writing
                          `serial*.log`.
                        </div>
                      <% else %>
                        <pre
                          id={"lima-log-output-#{@selected_thread.id}"}
                          class="lima-log-output"
                          phx-hook="AutoScrollBottom"
                        ><%= Enum.join(@selected_lima_log_lines, "\n") %></pre>
                      <% end %>
                    </div>
                  <% end %>
                </div>
              <% else %>
                <div class="terminal-placeholder">
                  <p class="orchestrator-eyebrow">Terminal</p>
                  <h2>Select a thread</h2>
                  <p>
                    Choose an existing thread or create a new one to launch a Lima VM and open the
                    Codex session in-browser.
                  </p>
                </div>
              <% end %>
            </section>

            <%= if @selected_thread do %>
              <div class="grid gap-6 lg:grid-cols-[minmax(0,1.1fr),minmax(18rem,0.9fr)]">
                <section class="orchestrator-panel">
                  <div class="orchestrator-panel-header">
                    <div>
                      <p class="orchestrator-eyebrow">Transcript</p>
                      <h2>Recent terminal activity</h2>
                    </div>
                    <span class="orchestrator-chip">{Enum.count(@transcript_preview)} samples</span>
                  </div>

                  <div :if={@transcript_preview == []} class="orchestrator-empty">
                    Transcript data will appear here after the bridge starts receiving input and output.
                  </div>

                  <div :for={entry <- @transcript_preview} class="space-y-3">
                    <article class="transcript-card">
                      <div class="flex items-center justify-between gap-3">
                        <span class={["transcript-kind", transcript_kind_class(entry["direction"])]}>
                          {entry["direction"]}
                        </span>
                        <span class="text-xs uppercase tracking-[0.24em] text-zinc-500">
                          {format_timestamp(entry["timestamp"])}
                        </span>
                      </div>
                      <pre>{entry["data"]}</pre>
                    </article>
                  </div>
                </section>

                <section class="orchestrator-panel">
                  <div class="orchestrator-panel-header">
                    <div>
                      <p class="orchestrator-eyebrow">Lifecycle</p>
                      <h2>Thread events</h2>
                    </div>
                    <span class="orchestrator-chip">{Enum.count(@selected_thread.events)} items</span>
                  </div>

                  <div class="space-y-3">
                    <article :for={event <- @selected_thread.events} class="event-card">
                      <p class="text-xs uppercase tracking-[0.24em] text-zinc-500">{event.kind}</p>
                      <p class="mt-2 text-sm text-zinc-200">{event.message}</p>
                      <p class="mt-2 text-xs text-zinc-500">{format_timestamp(event.inserted_at)}</p>
                    </article>
                  </div>
                </section>
              </div>
            <% end %>
          </div>
        </div>
      </div>
    </div>
    """
  end

  defp refresh_workspace(socket) do
    projects = Workspace.list_projects()

    selected_thread =
      if socket.assigns.selected_thread_id,
        do: Workspace.get_thread(socket.assigns.selected_thread_id)

    assign(socket,
      projects: projects,
      selected_thread: selected_thread,
      transcript_preview: Logs.tail(selected_thread, 12),
      selected_lima_log_command: Logs.lima_serial_tail_command(selected_thread),
      selected_lima_log_lines: Logs.tail_lima_serial(selected_thread, 180)
    )
  end

  defp assign_project_form(socket) do
    assign_project_form(socket, %Project{})
  end

  defp assign_project_form(socket, %Project{} = project) do
    assign(socket,
      project_form: to_form(Workspace.change_project(project), as: :project),
      project_form_project: project
    )
  end

  defp parse_integer(nil), do: nil
  defp parse_integer(value) when is_integer(value), do: value

  defp parse_integer(value) do
    case Integer.parse(to_string(value)) do
      {integer, _rest} -> integer
      :error -> nil
    end
  end

  defp thread_count(projects) do
    Enum.reduce(projects, 0, fn project, total -> total + Enum.count(project.threads) end)
  end

  defp persist_project(%Project{id: nil}, params), do: Workspace.create_project(params)

  defp persist_project(%Project{} = project, params),
    do: Workspace.update_project(project, params)

  defp project_form_title(%Project{id: nil}), do: "Add a project directory"
  defp project_form_title(%Project{}), do: "Edit project environment"

  defp project_submit_copy(%Project{id: nil}), do: "Add Project"
  defp project_submit_copy(%Project{}), do: "Save Project"

  defp project_setup_configured?(%Project{setup_commands: setup_commands}) do
    String.trim(setup_commands || "") != ""
  end

  defp active_thread_count(projects) do
    projects
    |> Enum.flat_map(& &1.threads)
    |> Enum.count(&(&1.status in ["booting", "running"]))
  end

  defp status_badge_class("running"), do: "status-badge-running"
  defp status_badge_class("booting"), do: "status-badge-booting"
  defp status_badge_class("failed"), do: "status-badge-failed"
  defp status_badge_class(_status), do: "status-badge-idle"

  defp terminal_status_copy(thread) do
    case thread.status do
      "running" -> "Connected on port #{thread.terminal_port}"
      "booting" -> "Bootstrapping VM and Codex"
      "failed" -> thread.last_error || "Thread failed"
      _ -> "Terminal offline"
    end
  end

  defp transcript_kind_class("input"), do: "transcript-kind-input"
  defp transcript_kind_class(_kind), do: "transcript-kind-output"

  defp thread_tab_class(true), do: "thread-view-tab thread-view-tab-active"
  defp thread_tab_class(false), do: "thread-view-tab"

  defp log_status_copy(nil), do: "tail -f unavailable until the thread has a Lima VM name"
  defp log_status_copy(command), do: command

  defp format_timestamp(nil), do: "pending"

  defp format_timestamp(%NaiveDateTime{} = datetime) do
    Calendar.strftime(datetime, "%b %d %H:%M:%S")
  end

  defp format_timestamp(%DateTime{} = datetime) do
    Calendar.strftime(datetime, "%b %d %H:%M:%S")
  end

  defp format_timestamp(value) when is_binary(value) do
    case DateTime.from_iso8601(value) do
      {:ok, datetime, _offset} -> format_timestamp(datetime)
      _ -> value
    end
  end
end
