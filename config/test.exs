import Config

test_db_dir = Path.join(System.tmp_dir!(), "test_lima")
File.mkdir_p!(test_db_dir)

test_partition = System.get_env("MIX_TEST_PARTITION")

test_db_name =
  if test_partition in [nil, ""],
    do: "test_lima_test.db",
    else: "test_lima_test#{test_partition}.db"

# Configure your database
#
# The MIX_TEST_PARTITION environment variable can be used
# to provide built-in test partitioning in CI environment.
# Run `mix help test` for more information.
config :test_lima, TestLima.Repo,
  database: Path.join(test_db_dir, test_db_name),
  pool_size: 5,
  pool: Ecto.Adapters.SQL.Sandbox

# We don't run a server during test. If one is required,
# you can enable the server option below.
config :test_lima, TestLimaWeb.Endpoint,
  http: [ip: {127, 0, 0, 1}, port: 4002],
  secret_key_base: "K3ZCoMOe1D900+IyEjfBTrW0iKk8YSIOvORBqe7VdhGL16LooMs1s60jh+EJ4wpR",
  server: false

# In test we don't send emails
config :test_lima, TestLima.Mailer, adapter: Swoosh.Adapters.Test

# Disable swoosh api client as it is only required for production adapters
config :swoosh, :api_client, false

# Print only warnings and errors during test
config :logger, level: :warning

# Initialize plugs at runtime for faster test compilation
config :phoenix, :plug_init_mode, :runtime

# Enable helpful, but potentially expensive runtime checks
config :phoenix_live_view,
  enable_expensive_runtime_checks: true

# Sort query params output of verified routes for robust url comparisons
config :phoenix,
  sort_verified_routes_query_params: true

config :test_lima,
  enable_terminal_reconciler: false,
  state_dir: Path.expand("../tmp/test_lima_test", __DIR__)
