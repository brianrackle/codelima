defmodule TestLima.Repo do
  use Ecto.Repo,
    otp_app: :test_lima,
    adapter: Ecto.Adapters.SQLite3
end
