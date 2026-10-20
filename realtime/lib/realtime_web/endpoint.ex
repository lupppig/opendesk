defmodule RealtimeWeb.Endpoint do
  @moduledoc """
  WebSocket gateway endpoint. Serves `/socket` and `/socket/widget` (Phoenix Channels,
  JSON v2 serializer) and `GET /health`; everything else is 404.

  `/socket` (agents): a browser upgrade whose `Origin` is not in
  `CHECK_ORIGINS` is refused with 403 (`RealtimeWeb.Origin`); clients that send
  no `Origin` (servers, CLIs) are allowed, as the token authorizes them.

  `/socket/widget` (chat widget on customer sites): any Origin, `widget_ws`
  tokens only, `contact:` topics only.
  """

  use Phoenix.Endpoint, otp_app: :realtime

  @websocket [
    serializer: [{Phoenix.Socket.V2.JSONSerializer, "~> 2.0.0"}],
    timeout: 60_000,
    # Client frames are small (joins, presence, typing).
    max_frame_size: 16_384,
    compress: false,
    connect_info: [:peer_data, :x_headers]
  ]

  # The chat widget is embedded on customer sites, so its socket allows any
  # Origin; it accepts only widget_ws tokens and routes only contact: topics.
  # The token rides in Sec-WebSocket-Protocol (the client's authToken), not in the URL,
  # where proxies and access logs would keep it. auth_token is a socket option: Phoenix
  # sets the transport's from it.
  socket "/socket/widget", RealtimeWeb.WidgetSocket,
    auth_token: true,
    websocket: [{:check_origin, false} | @websocket],
    longpoll: false

  socket "/socket", RealtimeWeb.UserSocket,
    auth_token: true,
    websocket: [{:check_origin, {RealtimeWeb.Origin, :allowed?, []}} | @websocket],
    longpoll: false

  plug :health
  plug :not_found

  # A browser Origin outside CHECK_ORIGINS is refused here, before the socket transport:
  # Phoenix's own refusal reads the auth token header after sending its 403 and raises.
  def call(%Plug.Conn{request_path: "/socket/websocket"} = conn, opts) do
    conn = secure_headers(conn)

    case Plug.Conn.get_req_header(conn, "origin") do
      [origin | _] ->
        if RealtimeWeb.Origin.allowed?(URI.parse(origin)),
          do: super(conn, opts),
          else: conn |> Plug.Conn.send_resp(403, "") |> Plug.Conn.halt()

      [] ->
        super(conn, opts)
    end
  end

  def call(conn, opts), do: conn |> secure_headers() |> super(opts)

  # Every answer of the gateway (refusals, health, 404) says what browsers may do with it.
  defp secure_headers(conn) do
    conn
    |> Plug.Conn.put_resp_header("x-content-type-options", "nosniff")
    |> Plug.Conn.put_resp_header(
      "strict-transport-security",
      "max-age=63072000; includeSubDomains"
    )
    |> Plug.Conn.put_resp_header("referrer-policy", "no-referrer")
    |> Plug.Conn.put_resp_header(
      "content-security-policy",
      "default-src 'none'; frame-ancestors 'none'"
    )
  end

  defp health(%Plug.Conn{method: "GET", request_path: "/health"} = conn, _opts) do
    conn
    |> Plug.Conn.put_resp_content_type("text/plain")
    |> Plug.Conn.send_resp(200, "ok")
    |> Plug.Conn.halt()
  end

  defp health(conn, _opts), do: conn

  defp not_found(conn, _opts) do
    conn
    |> Plug.Conn.put_resp_content_type("text/plain")
    |> Plug.Conn.send_resp(404, "not found")
    |> Plug.Conn.halt()
  end
end
