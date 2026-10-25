defmodule Realtime.Application do
  @moduledoc false

  use Application

  require Logger

  @impl true
  def start(_type, _args) do
    with :ok <- check_secret() do
      maybe_attach_posthog()
      Supervisor.start_link(children(), strategy: :one_for_one, name: Realtime.Supervisor)
    end
  end

  # AUTH_SECRET is checked in runtime.exs too; this covers any other way the
  # application could be configured.
  defp check_secret do
    case Realtime.Auth.validate_secret(Application.get_env(:realtime, :auth_secret)) do
      :ok ->
        :ok

      {:error, reason} ->
        Logger.error(
          "refusing to boot: AUTH_SECRET must be at least #{Realtime.Auth.min_secret_bytes()} bytes"
        )

        {:error, reason}
    end
  end

  defp children do
    cluster() ++
      [
        {Phoenix.PubSub, name: Realtime.PubSub},
        Realtime.Sockets,
        Realtime.RateLimiter,
        Realtime.Metrics,
        Realtime.Revocations,
        Realtime.Fanout.Dedupe
      ] ++
      Realtime.PostHog.children() ++
      [RealtimeWeb.Presence] ++
      nats() ++
      [RealtimeWeb.Endpoint]
  end

  defp cluster do
    case Application.get_env(:libcluster, :topologies, []) do
      [] -> []
      topologies -> [{Cluster.Supervisor, [topologies, [name: Realtime.ClusterSupervisor]]}]
    end
  end

  defp nats do
    if Application.get_env(:realtime, :nats_enabled, true), do: [Realtime.Nats.Consumer], else: []
  end

  defp maybe_attach_posthog do
    if Realtime.PostHog.enabled?() do
      :logger.add_handler(:posthog, Realtime.PostHog.LoggerHandler, %{level: :error})
    end
  end

  @impl true
  def config_change(changed, _new, removed) do
    RealtimeWeb.Endpoint.config_change(changed, removed)
    :ok
  end
end
