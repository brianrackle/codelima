# This file is responsible for configuring your application
# and its dependencies with the aid of the Config module.
#
# This configuration file is loaded before any dependency and
# is restricted to this project.

# General application configuration
import Config

config :test_lima,
  ecto_repos: [TestLima.Repo],
  generators: [timestamp_type: :utc_datetime],
  project_root: Path.expand("..", __DIR__),
  state_dir: Path.expand("../tmp/test_lima", __DIR__),
  enable_terminal_reconciler: true

# Configure the endpoint
config :test_lima, TestLimaWeb.Endpoint,
  url: [host: "localhost"],
  adapter: Bandit.PhoenixAdapter,
  render_errors: [
    formats: [html: TestLimaWeb.ErrorHTML, json: TestLimaWeb.ErrorJSON],
    layout: false
  ],
  pubsub_server: TestLima.PubSub,
  live_view: [signing_salt: "DZ7ZXP06"]

# Configure the mailer
#
# By default it uses the "Local" adapter which stores the emails
# locally. You can see the emails in your browser, at "/dev/mailbox".
#
# For production it's recommended to configure a different adapter
# at the `config/runtime.exs`.
config :test_lima, TestLima.Mailer, adapter: Swoosh.Adapters.Local

config :phoenix_live_view, :colocated_js,
  target_directory: Path.expand("../assets/node_modules/phoenix-colocated", __DIR__)

# Configure esbuild (the version is required)
config :esbuild,
  version: "0.25.4",
  test_lima: [
    args:
      ~w(js/app.js --bundle --target=es2022 --outdir=../priv/static/assets/js --external:/fonts/* --external:/images/* --alias:@=.),
    cd: Path.expand("../assets", __DIR__),
    env: %{"NODE_PATH" => [Path.expand("../deps", __DIR__), Mix.Project.build_path()]}
  ]

# Configure tailwind (the version is required)
config :tailwind,
  version: "4.1.12",
  test_lima: [
    args: ~w(
      --input=assets/css/app.css
      --output=priv/static/assets/css/app.css
    ),
    cd: Path.expand("..", __DIR__)
  ]

# Configure Elixir's Logger
config :logger, :default_formatter,
  format: "$time $metadata[$level] $message\n",
  metadata: [:request_id]

# Use Jason for JSON parsing in Phoenix
config :phoenix, :json_library, Jason

# Import environment specific config. This must remain at the bottom
# of this file so it overrides the configuration defined above.
import_config "#{config_env()}.exs"
