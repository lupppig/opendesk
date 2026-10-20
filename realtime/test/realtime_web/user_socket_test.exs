defmodule RealtimeWeb.UserSocketTest do
  use RealtimeWeb.ChannelCase, async: true

  alias RealtimeWeb.{UserSocket, WidgetSocket}

  defp info, do: %{peer_data: %{address: Tokens.random_ip(), port: 1}, x_headers: []}
  defp with_token(token), do: Map.put(info(), :auth_token, token)

  test "a ws token connects with the claims as assigns and id user_socket:<sub>" do
    claims = Tokens.ws_claims()
    assert {:ok, socket} = connect_claims(claims)

    assert socket.assigns.kind == :user
    assert socket.assigns.sub == claims["sub"]
    assert socket.assigns.wid == claims["wid"]
    assert socket.assigns.role == "agent"
    assert socket.assigns.name == "Anna"
    assert socket.assigns.avatar == "https://example.test/a.png"
    refute Map.has_key?(socket.assigns, :token)
    assert UserSocket.id(socket) == "user_socket:" <> claims["sub"]
  end

  test "the token is read from the auth_token header, never the query string" do
    claims = Tokens.ws_claims()
    header = Map.put(info(), :auth_token, Tokens.sign(claims))
    assert {:ok, socket} = connect(UserSocket, %{}, connect_info: header)
    assert socket.assigns.sub == claims["sub"]

    assert connect(UserSocket, %{"token" => Tokens.sign(claims)}, connect_info: info()) == :error
  end

  test "a widget_ws token connects to the widget socket as a contact" do
    claims = Tokens.widget_claims()
    assert {:ok, socket} = connect_claims(claims)
    assert socket.handler == WidgetSocket
    assert socket.assigns.kind == :contact
    assert WidgetSocket.id(socket) == "user_socket:" <> claims["sub"]
  end

  test "the agents' socket refuses widget_ws tokens" do
    token = Tokens.sign(Tokens.widget_claims())
    assert connect(UserSocket, %{}, connect_info: with_token(token)) == :error
  end

  test "the widget socket refuses ws tokens of users and API keys" do
    for kind <- ["user", "api_key"] do
      token = Tokens.sign(Tokens.ws_claims(%{"kind" => kind}))
      assert connect(WidgetSocket, %{}, connect_info: with_token(token)) == :error
    end
  end

  test "the widget socket routes only contact: topics" do
    claims = Tokens.widget_claims()
    {:ok, socket} = connect_claims(claims)

    for topic <- [
          "workspace:#{claims["wid"]}",
          "ticket:#{claims["wid"]}:#{Tokens.uuid()}",
          "user:#{claims["sub"]}"
        ] do
      assert_raise RuntimeError, ~r/no channel found/, fn -> subscribe_and_join(socket, topic) end
    end

    assert {:ok, _, _} = subscribe_and_join(socket, "contact:#{claims["wid"]}:#{claims["sub"]}")
  end

  test "the agents' socket does not route contact: topics" do
    claims = Tokens.ws_claims()
    {:ok, socket} = connect_claims(claims)

    assert_raise RuntimeError, ~r/no channel found/, fn ->
      subscribe_and_join(socket, "contact:#{claims["wid"]}:#{Tokens.uuid()}")
    end
  end

  test "bad tokens are refused" do
    info = %{peer_data: %{address: Tokens.random_ip(), port: 1}, x_headers: []}

    for token <- [
          nil,
          "",
          Tokens.sign(Tokens.ws_claims(%{"purpose" => "access"})),
          Tokens.sign(Tokens.ws_claims(%{"exp" => Tokens.now() - 10})),
          Tokens.sign(Map.delete(Tokens.ws_claims(), "exp")),
          Tokens.sign(Tokens.ws_claims(), alg: "HS512"),
          Tokens.unsigned(Tokens.ws_claims())
        ] do
      assert connect(UserSocket, %{}, connect_info: Map.put(info, :auth_token, token)) == :error
    end

    assert connect(UserSocket, %{}, connect_info: info) == :error
  end
end
