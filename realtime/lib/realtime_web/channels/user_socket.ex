defmodule RealtimeWeb.UserSocket do
  @moduledoc """
  The agents' socket at `/socket` (`contracts/realtime.md`). Connect with
  `?vsn=2.0.0` and the token in `Sec-WebSocket-Protocol` (the client's
  `authToken`); only `ws` tokens (users and API keys) are accepted,
  and browser origins must be in `CHECK_ORIGINS`. The chat widget uses
  `RealtimeWeb.WidgetSocket` instead.

  The token is verified once at connect and then forgotten: only its claims are
  kept in assigns. A token in the URL is never read, and connection logging is
  off.
  """

  use Phoenix.Socket, log: false

  channel "workspace:*", RealtimeWeb.WorkspaceChannel
  channel "team:*", RealtimeWeb.TeamChannel
  channel "user:*", RealtimeWeb.UserChannel
  channel "ticket:*", RealtimeWeb.TicketChannel

  @impl true
  def connect(params, socket, connect_info),
    do: RealtimeWeb.SocketAuth.connect(params, socket, connect_info, [:user, :api_key])

  @impl true
  def id(socket), do: "user_socket:" <> socket.assigns.sub
end
