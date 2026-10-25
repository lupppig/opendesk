import Config

config :realtime, RealtimeWeb.Endpoint,
  adapter: Bandit.PhoenixAdapter,
  url: [host: "localhost"],
  render_errors: [formats: [json: RealtimeWeb.ErrorJSON], layout: false],
  pubsub_server: Realtime.PubSub,
  server: true

config :realtime,
  # Concurrent sockets per token subject and per client IP (cluster-wide).
  max_sockets_per_sub: 20,
  max_sockets_per_ip: 200,
  # Seconds an agents' socket lives before it reconnects with a fresh token (0 = no limit);
  # each socket takes a random 80 to 100 % of it.
  max_socket_age: 3600,
  # Token buckets: {per_second, burst}.
  connect_rate_per_ip: {2, 30},
  join_rate_per_socket: {10, 50},
  push_rate_per_socket: {10, 20},
  # How many X-Forwarded-For hops to trust (0 = use the TCP peer address).
  trusted_proxy_hops: 0,
  nats: [url: "nats://127.0.0.1:4222", token: nil],
  posthog_key: nil,
  posthog_host: "https://us.i.posthog.com",
  cluster_dns: nil

config :phoenix, :json_library, Jason
# Socket and join params are never logged (see UserSocket and channels), and in
# case anything else does: any key with these names prints as [FILTERED].
config :phoenix, :filter_parameters, ["token", "password", "secret", "key"]

config :logger, :default_formatter,
  format: "$time $metadata[$level] $message\n",
  metadata: [:request_id, :reason]

import_config "#{config_env()}.exs"
