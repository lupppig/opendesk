defmodule RealtimeWeb.SocketAuth do
  @moduledoc """
  Connect logic shared by both sockets: per-IP connect throttle, token
  verification, the socket's accepted identity kinds, and the socket caps.
  """

  require Logger

  alias Realtime.Auth
  alias Realtime.ClientIP
  alias Realtime.Metrics
  alias Realtime.RateLimiter
  alias Realtime.Sockets

  @doc "Connects `socket` if the token's kind is one of `kinds`."
  def connect(_params, socket, connect_info, kinds) do
    ip = ClientIP.from_connect_info(connect_info)

    with :ok <-
           RateLimiter.check(
             {:connect, ip},
             Application.fetch_env!(:realtime, :connect_rate_per_ip)
           ),
         {:ok, identity} <- Auth.verify(token(connect_info)),
         :ok <- accept_kind(identity, kinds),
         :ok <- Sockets.admit(identity.sub, ip) do
      Metrics.incr({:connected, identity.kind})
      {:ok, Phoenix.Socket.assign(socket, identity)}
    else
      {:error, reason} ->
        Metrics.incr({:rejected, reason})
        Logger.info("socket rejected", reason: reason)
        :error
    end
  end

  # The token from the Sec-WebSocket-Protocol header (Phoenix's auth_token). A token in
  # the URL is never read: proxies and access logs keep URLs.
  defp token(%{auth_token: token}) when is_binary(token) and token != "", do: token
  defp token(_connect_info), do: nil

  defp accept_kind(%{kind: kind}, kinds) do
    if kind in kinds, do: :ok, else: {:error, :wrong_purpose}
  end
end
