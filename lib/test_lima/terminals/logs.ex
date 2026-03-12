defmodule TestLima.Terminals.Logs do
  @moduledoc false

  alias TestLima.RuntimePaths

  def tail(nil, _limit), do: []
  def tail(%{log_path: nil}, _limit), do: []

  def tail(%{log_path: log_path}, limit) do
    if File.exists?(log_path) do
      log_path
      |> File.stream!([], :line)
      |> Enum.take(-limit)
      |> Enum.flat_map(&decode_line/1)
    else
      []
    end
  end

  def tail_lima_serial(nil, _limit), do: []
  def tail_lima_serial(%{vm_name: nil}, _limit), do: []

  def tail_lima_serial(%{vm_name: vm_name}, limit) do
    vm_name
    |> RuntimePaths.lima_serial_log_glob()
    |> Path.wildcard()
    |> Enum.sort()
    |> Enum.flat_map(&tail_text_file(&1, limit))
    |> Enum.take(-limit)
  end

  def lima_serial_tail_command(nil), do: nil
  def lima_serial_tail_command(%{vm_name: nil}), do: nil

  def lima_serial_tail_command(%{vm_name: vm_name}) do
    "tail -f #{shell_escape(RuntimePaths.lima_serial_log_glob(vm_name))}"
  end

  defp decode_line(line) do
    case Jason.decode(String.trim(line)) do
      {:ok, record} -> [Map.update(record, "data", "", &strip_ansi/1)]
      _ -> []
    end
  end

  defp tail_text_file(path, limit) do
    path
    |> File.stream!([], :line)
    |> Enum.take(-limit)
    |> Enum.map(&normalize_line/1)
  end

  defp normalize_line(line) do
    line
    |> String.trim_trailing("\n")
    |> String.trim_trailing("\r")
  end

  defp shell_escape(path) do
    escaped_path = String.replace(path, "'", ~s('"'"'))
    "'#{escaped_path}'"
  end

  defp strip_ansi(text) do
    Regex.replace(~r/\e\[[\d;?]*[ -\/]*[@-~]/, text, "")
  end
end
