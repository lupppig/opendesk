defmodule RealtimeWeb.EndpointTest do
  use ExUnit.Case, async: true

  import Plug.Test
  import Plug.Conn

  alias Realtime.Test.Tokens
  alias RealtimeWeb.Endpoint

  defp upgrade_conn(token, origin, path \\ "/socket") do
    conn =
      conn(:get, path <> "/websocket?vsn=2.0.0")
      |> put_req_header(
        "sec-websocket-protocol",
        "phoenix, base64url.bearer.phx." <> Base.encode64(token, padding: false)
      )
      |> put_req_header("connection", "Upgrade")
      |> put_req_header("upgrade", "websocket")
      |> put_req_header("sec-websocket-version", "13")
      |> put_req_header("sec-websocket-key", Base.encode64(:crypto.strong_rand_bytes(16)))

    conn = if origin, do: put_req_header(conn, "origin", origin), else: conn
    # The upgrade validation reads the Host header, which Plug.Test keeps in conn.host.
    %{conn | remote_ip: Tokens.random_ip(), req_headers: [{"host", "gw.test"} | conn.req_headers]}
  end

  test "GET /health is 200 ok" do
    conn = Endpoint.call(conn(:get, "/health"), [])
    assert conn.status == 200
    assert conn.resp_body == "ok"
  end

  test "unknown paths are 404" do
    assert Endpoint.call(conn(:get, "/"), []).status == 404
  end

  test "every answer carries the security headers" do
    for conn <- [Endpoint.call(conn(:get, "/"), []), Endpoint.call(conn(:get, "/health"), [])] do
      assert get_resp_header(conn, "x-content-type-options") == ["nosniff"]
      assert [_] = get_resp_header(conn, "strict-transport-security")
    end

    refused =
      Endpoint.call(upgrade_conn(Tokens.sign(Tokens.ws_claims()), "https://evil.example"), [])

    assert refused.status == 403
    assert get_resp_header(refused, "x-content-type-options") == ["nosniff"]
  end

  test "an allowed Origin upgrades" do
    token = Tokens.sign(Tokens.ws_claims())
    conn = Endpoint.call(upgrade_conn(token, "https://app.example.test"), [])
    assert conn.state == :upgraded
  end

  test "no Origin (a non-browser client) upgrades" do
    token = Tokens.sign(Tokens.ws_claims())
    conn = Endpoint.call(upgrade_conn(token, nil), [])
    assert conn.state == :upgraded
  end

  test "a foreign Origin is refused with 403" do
    token = Tokens.sign(Tokens.ws_claims())
    conn = Endpoint.call(upgrade_conn(token, "https://evil.example"), [])
    assert conn.status == 403
  end

  test "a bad token is refused with 403" do
    token = Tokens.sign(Tokens.ws_claims(%{"purpose" => "access"}))
    conn = Endpoint.call(upgrade_conn(token, "https://app.example.test"), [])
    assert conn.status == 403
  end

  describe "/socket/widget" do
    test "a widget_ws token upgrades from any Origin, or none" do
      for origin <- ["https://customer-shop.example", "https://app.example.test", nil] do
        token = Tokens.sign(Tokens.widget_claims())
        conn = Endpoint.call(upgrade_conn(token, origin, "/socket/widget"), [])
        assert conn.state == :upgraded, inspect(origin)
      end
    end

    test "a ws token is refused" do
      token = Tokens.sign(Tokens.ws_claims())

      conn =
        Endpoint.call(upgrade_conn(token, "https://customer-shop.example", "/socket/widget"), [])

      assert conn.status == 403
    end
  end

  test "a widget_ws token is refused on /socket, from an allowed Origin too" do
    token = Tokens.sign(Tokens.widget_claims())
    conn = Endpoint.call(upgrade_conn(token, "https://app.example.test"), [])
    assert conn.status == 403
  end
end
