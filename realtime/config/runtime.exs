import Config

# Every setting comes from the environment. A variable that is present but
# blank counts as unset (compose passes optional variables through as "").
env = fn name ->
  case System.get_env(name) do
    nil -> nil
    value -> if String.trim(value) == "", do: nil, else: String.trim(value)
  end
end

int = fn name, default ->
  case env.(name) do
    nil ->
      default

    value ->
      case Integer.parse(value) do
        {n, ""} when n >= 0 -> n
        _ -> raise "#{name} must be a non-negative integer"
      end
  end
end

# The Go API's development default, so `go run ./cmd/opendesk serve` and
# `mix phx.server` agree out of the box. Never used in prod.
dev_auth_secret = "development-only-auth-secret-change-me-0000"

auth_secret =
  case {config_env(), env.("AUTH_SECRET")} do
    {:prod, nil} -> raise "AUTH_SECRET is required"
    {:test, nil} -> Application.get_env(:realtime, :auth_secret)
    {_, nil} -> dev_auth_secret
    {_, secret} -> secret
  end

# Same floor as the Go API: HS256 keys shorter than 32 bytes refuse to boot.
# Realtime.Application checks it again at start.
if byte_size(auth_secret) < 32 do
  raise "AUTH_SECRET must be at least 32 bytes"
end

secret_key_base =
  case {config_env(), env.("SECRET_KEY_BASE")} do
    {:prod, nil} ->
      raise "SECRET_KEY_BASE is required (generate one with: openssl rand -base64 48)"

    {:prod, value} when byte_size(value) < 64 ->
      raise "SECRET_KEY_BASE must be at least 64 bytes"

    {_, nil} ->
      Application.get_env(:realtime, RealtimeWeb.Endpoint)[:secret_key_base] ||
        Base.encode64(:crypto.strong_rand_bytes(48))

    {_, value} ->
      value
  end

check_origins =
  case {config_env(), env.("CHECK_ORIGINS")} do
    {:prod, nil} ->
      raise "CHECK_ORIGINS is required: a comma-separated list of allowed browser origins, " <>
              "e.g. https://app.opendesk.sh"

    {_, nil} ->
      Application.get_env(:realtime, :check_origins, :any)

    {_, value} ->
      value |> String.split(",", trim: true) |> Enum.map(&String.trim/1)
  end

port = int.("PORT", 4000)

config :realtime, RealtimeWeb.Endpoint,
  http: [ip: {0, 0, 0, 0, 0, 0, 0, 0}, port: port],
  secret_key_base: secret_key_base

if config_env() == :test do
  config :realtime, RealtimeWeb.Endpoint, http: [ip: {127, 0, 0, 1}, port: 4402]
end

config :realtime,
  auth_secret: auth_secret,
  check_origins: check_origins,
  nats: [url: env.("NATS_URL") || "nats://127.0.0.1:4222", token: env.("NATS_TOKEN")],
  posthog_key: env.("POSTHOG_KEY"),
  posthog_host: env.("POSTHOG_HOST") || "https://us.i.posthog.com",
  cluster_dns: env.("CLUSTER_DNS"),
  max_sockets_per_sub: int.("MAX_SOCKETS_PER_SUB", 20),
  max_sockets_per_ip: int.("MAX_SOCKETS_PER_IP", 200),
  max_socket_age: int.("MAX_SOCKET_AGE", 3600),
  trusted_proxy_hops: int.("TRUSTED_PROXY_HOPS", 0)

# Clustering: ECS service discovery publishes one A record per task under
# CLUSTER_DNS; every node polls it and connects to the others. The node
# basename must match RELEASE_NODE's (rel/env.sh.eex sets realtime@<ip>).
case env.("CLUSTER_DNS") do
  nil ->
    config :libcluster, topologies: []

  query ->
    config :libcluster,
      topologies: [
        opendesk: [
          strategy: Cluster.Strategy.DNSPoll,
          config: [polling_interval: 5_000, query: query, node_basename: "realtime"]
        ]
      ]
end
