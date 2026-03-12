defmodule TestLima.Workspace do
  @moduledoc """
  Project and conversation-thread persistence for the Lima orchestrator.
  """

  import Ecto.Query, warn: false

  alias Phoenix.PubSub
  alias TestLima.Repo
  alias TestLima.Terminals
  alias TestLima.Workspace.{Project, Thread, ThreadEvent}

  @topic "workspace"
  @event_limit 18

  def subscribe do
    PubSub.subscribe(TestLima.PubSub, @topic)
  end

  def list_projects do
    projects_query()
    |> repo().all()
    |> repo().preload(threads: threads_query())
  end

  def get_project!(id) do
    Project
    |> repo().get!(id)
    |> repo().preload(threads: threads_query())
  end

  def create_project(attrs) do
    %Project{}
    |> Project.changeset(attrs)
    |> repo().insert()
    |> notify(:project_saved)
  end

  def update_project(%Project{} = project, attrs) do
    project
    |> Project.changeset(attrs)
    |> repo().update()
    |> notify(:project_saved)
  end

  def delete_project(%Project{} = project) do
    project = repo().preload(project, :threads)
    Enum.each(project.threads, &stop_thread/1)

    project
    |> repo().delete()
    |> notify(:project_deleted)
  end

  def change_project(%Project{} = project, attrs \\ %{}) do
    Project.changeset(project, attrs)
  end

  def list_threads do
    repo().all(threads_query())
  end

  def get_thread(id) when is_integer(id) do
    case repo().get(Thread, id) do
      nil -> nil
      thread -> preload_thread(thread)
    end
  end

  def get_thread!(id) do
    Thread
    |> repo().get!(id)
    |> preload_thread()
  end

  def create_thread(%Project{} = project, attrs \\ %{}) do
    attrs =
      attrs
      |> Map.new(fn {key, value} -> {to_string(key), value} end)
      |> Map.put_new("title", next_thread_title(project))
      |> Map.put_new("status", "created")
      |> Map.put_new("vm_name", build_vm_name(project))
      |> Map.put_new("project_id", project.id)

    %Thread{}
    |> Thread.changeset(attrs)
    |> repo().insert()
    |> case do
      {:ok, thread} ->
        record_event(thread, "thread.created", "Thread registered for #{project.name}")
        notify({:ok, preload_thread(thread)}, :thread_saved)

      error ->
        error
    end
  end

  def update_thread(%Thread{} = thread, attrs) do
    thread
    |> Thread.changeset(attrs)
    |> repo().update()
    |> notify(:thread_saved)
  end

  def delete_thread(%Thread{} = thread) do
    stop_thread(thread)

    thread
    |> repo().delete()
    |> notify(:thread_deleted)
  end

  def change_thread(%Thread{} = thread, attrs \\ %{}) do
    Thread.changeset(thread, attrs)
  end

  def start_thread(%Thread{} = thread) do
    thread = preload_thread(thread)

    case Terminals.start_thread(thread.id) do
      {:ok, _pid} ->
        {:ok, get_thread!(thread.id)}

      {:error, reason} ->
        {:error, reason}
    end
  end

  def stop_thread(%Thread{} = thread) do
    case Terminals.stop_thread(thread.id) do
      {:error, reason} -> {:error, reason}
      _result -> {:ok, get_thread(thread.id)}
    end
  end

  def set_thread_runtime(%Thread{} = thread, attrs) do
    thread
    |> Thread.runtime_changeset(attrs)
    |> repo().update()
    |> notify(:thread_saved)
  end

  def record_event(%Thread{} = thread, kind, message, metadata \\ %{}) do
    %ThreadEvent{}
    |> ThreadEvent.changeset(%{
      kind: kind,
      message: message,
      metadata: metadata,
      thread_id: thread.id
    })
    |> repo().insert()
    |> case do
      {:ok, event} ->
        broadcast({:thread_event, event.thread_id, event})
        {:ok, event}

      error ->
        error
    end
  end

  def list_thread_events(%Thread{} = thread, limit \\ @event_limit) do
    ThreadEvent
    |> where([event], event.thread_id == ^thread.id)
    |> order_by([event], desc: event.inserted_at)
    |> limit(^limit)
    |> repo().all()
    |> Enum.reverse()
  end

  def reconcile_runtime! do
    from(thread in Thread, where: thread.status in ["booting", "running"])
    |> repo().update_all(
      set: [
        status: "stopped",
        terminal_port: nil,
        access_token: nil,
        last_error: "The Phoenix app restarted and detached the terminal bridge."
      ]
    )
  end

  def workspace_topic, do: @topic

  defp preload_thread(thread) do
    repo().preload(thread, [
      :project,
      events:
        from(event in ThreadEvent,
          order_by: [desc: event.inserted_at],
          limit: ^@event_limit
        )
    ])
    |> reverse_events()
  end

  defp reverse_events(%Thread{} = thread) do
    %{thread | events: Enum.reverse(thread.events)}
  end

  defp projects_query do
    from(project in Project, order_by: [asc: project.name, asc: project.inserted_at])
  end

  defp threads_query do
    from(thread in Thread,
      order_by: [desc: thread.updated_at, desc: thread.inserted_at]
    )
  end

  defp next_thread_title(project) do
    count =
      from(thread in Thread, where: thread.project_id == ^project.id, select: count(thread.id))
      |> repo().one()

    "Thread #{count + 1}"
  end

  defp build_vm_name(project) do
    suffix =
      6
      |> :crypto.strong_rand_bytes()
      |> Base.url_encode64(padding: false)
      |> binary_part(0, 8)
      |> String.downcase()

    "#{project.slug}-#{suffix}"
  end

  defp notify({:ok, struct}, event) do
    broadcast({event, struct})
    {:ok, struct}
  end

  defp notify(error, _event), do: error

  defp broadcast(message) do
    PubSub.broadcast(TestLima.PubSub, @topic, message)
  end

  defp repo do
    Code.ensure_loaded!(Repo)
    Repo
  end
end
