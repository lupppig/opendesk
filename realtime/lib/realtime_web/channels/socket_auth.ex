defmodule RealtimeWeb.SocketAuth do
  @moduledoc """
  Connect logic shared by both sockets: per-IP connect throttle, token
  verification, revocations (`Realtime.Revocations`), the socket's accepted
  identity kinds, the socket caps and, on the agents' socket, the socket's
  maximum age.

  The maximum age (`MAX_SOCKET_AGE` seconds, `0` for none) bounds how long a
  socket keeps the claims of the token it connected with: when it is reached the
  socket closes with code 1001, and the client reconnects with a fresh token.
  Each socket takes a random 80 to 100 % of it, so sockets opened together (after
  a deploy) do not all reconnect at once.
  """

  require Logger

  alias Realtime.Auth
  alias Realtime.ClientIP
  alias Realtime.Metrics
  alias Realtime.RateLimiter
  alias Realtime.Revocations
  alias Realtime.Sockets

  @doc """
  Connects `socket` if the token's kind is one of `kinds`. With `max_age: true`,
  the socket closes once it reaches its maximum age.

  Runs in the socket process, which `Realtime.Sockets.admit/2` and the age timer rely on.
  """
  def connect(_params, socket, connect_info, kinds, opts \\ []) do
    ip = ClientIP.from_connect_info(connect_info)

    with :ok <-
           RateLimiter.check(
             {:connect, ip},
             Application.fetch_env!(:realtime, :connect_rate_per_ip)
           ),
         {:ok, identity} <- Auth.verify(token(connect_info)),
         :ok <- accept_kind(identity, kinds),
         :ok <- not_revoked(identity),
         :ok <- Sockets.admit(identity.sub, ip) do
      Metrics.incr({:connected, identity.kind})
      socket = Phoenix.Socket.assign(socket, identity)
      if Keyword.get(opts, :max_age, false), do: schedule_max_age(socket)
      {:ok, socket}
    else
      {:error, reason} ->
        Metrics.incr({:rejected, reason})
        Logger.info("socket rejected", reason: reason)
        :error
    end
  end

  @doc """
  The age in ms after which a socket closes: a random 80 to 100 % of `seconds`,
  or `nil` for none.
  """
  @spec max_age_ms(non_neg_integer()) :: pos_integer() | nil
  def max_age_ms(seconds) when is_integer(seconds) and seconds > 0 do
    full = seconds * 1000
    full - :rand.uniform(div(full, 5) + 1) + 1
  end

  def max_age_ms(_seconds), do: nil

  # Phoenix.Socket closes on a "disconnect" broadcast to the socket id (code 1001, which
  # phoenix.js answers by reconnecting), as for od.control.disconnect.
  defp schedule_max_age(socket) do
    case max_age_ms(Application.get_env(:realtime, :max_socket_age, 3600)) do
      nil ->
        :ok

      ms ->
        msg = %Phoenix.Socket.Broadcast{
          topic: socket.handler.id(socket),
          event: "disconnect",
          payload: %{}
        }

        Process.send_after(self(), msg, ms)
    end
  end

  # The token from the Sec-WebSocket-Protocol header (Phoenix's auth_token). A token in
  # the URL is never read: proxies and access logs keep URLs.
  defp token(%{auth_token: token}) when is_binary(token) and token != "", do: token
  defp token(_connect_info), do: nil

  defp accept_kind(%{kind: kind}, kinds) do
    if kind in kinds, do: :ok, else: {:error, :wrong_purpose}
  end

  defp not_revoked(identity) do
    if Revocations.revoked?(identity), do: {:error, :revoked}, else: :ok
  end
end
