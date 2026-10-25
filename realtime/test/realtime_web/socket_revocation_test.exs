defmodule RealtimeWeb.SocketRevocationTest do
  @moduledoc "A token issued before a revocation of its user or session cannot open a socket."
  use RealtimeWeb.ChannelCase, async: true

  alias Phoenix.Socket.Broadcast
  alias Realtime.{Fanout, Metrics}

  defp disconnect(body) do
    id = "user_socket:" <> body["user_id"]
    assert {:ok, [^id]} = Fanout.handle("od.control.disconnect", Jason.encode!(body))
  end

  test "a user-wide revocation refuses that user's tokens issued before it" do
    claims = Tokens.ws_claims()
    assert {:ok, _} = connect_claims(claims)

    disconnect(%{"user_id" => claims["sub"], "session_id" => "", "at" => claims["iat_ms"] + 1})
    assert connect_claims(claims) == :error
    # Any of the user's sessions, and a token issued in the same ms as the revocation.
    assert connect_claims(%{claims | "sid" => Tokens.uuid()}) == :error
    assert connect_claims(%{claims | "iat_ms" => claims["iat_ms"] + 1}) == :error
    assert Metrics.snapshot()[{:rejected, :revoked}] >= 1

    # A token issued after it connects, with whatever the API now says.
    fresh = %{claims | "iat_ms" => claims["iat_ms"] + 2, "role" => "viewer"}
    assert {:ok, socket} = connect_claims(fresh)
    assert socket.assigns.role == "viewer"
  end

  test "a session's revocation refuses that session's tokens only" do
    claims = Tokens.ws_claims()
    sid = claims["sid"]
    disconnect(%{"user_id" => claims["sub"], "session_id" => sid, "at" => claims["iat_ms"]})

    assert connect_claims(claims) == :error
    assert {:ok, _} = connect_claims(%{claims | "sid" => Tokens.uuid()})
    assert {:ok, _} = connect_claims(%{claims | "iat_ms" => claims["iat_ms"] + 1})
  end

  test "it still closes the user's open sockets" do
    claims = Tokens.ws_claims()
    id = "user_socket:" <> claims["sub"]
    :ok = RealtimeWeb.Endpoint.subscribe(id)
    disconnect(%{"user_id" => claims["sub"], "session_id" => claims["sid"]})
    assert_receive %Broadcast{topic: ^id, event: "disconnect"}
  end

  test "a token without iat_ms is compared by its iat" do
    claims = Tokens.ws_claims() |> Map.delete("iat_ms")
    issued = claims["iat"] * 1000

    disconnect(%{"user_id" => claims["sub"], "session_id" => "", "at" => issued - 1})
    assert {:ok, _} = connect_claims(claims)

    disconnect(%{"user_id" => claims["sub"], "session_id" => "", "at" => issued})
    assert connect_claims(claims) == :error
  end

  test "a disconnect without a time counts from when the gateway received it" do
    claims = Tokens.ws_claims()
    disconnect(%{"user_id" => claims["sub"]})

    assert connect_claims(claims) == :error
    assert {:ok, _} = connect_claims(%{claims | "iat_ms" => Tokens.now_ms() + 1_000})
  end

  test "an API key's revocation refuses its tokens" do
    claims = Tokens.ws_claims(%{"kind" => "api_key"}) |> Map.delete("sid")
    disconnect(%{"user_id" => claims["sub"], "session_id" => "", "at" => claims["iat_ms"]})
    assert connect_claims(claims) == :error
  end

  test "a revocation of someone else changes nothing" do
    claims = Tokens.ws_claims()
    disconnect(%{"user_id" => Tokens.uuid(), "session_id" => "", "at" => claims["iat_ms"] + 1})

    disconnect(%{
      "user_id" => claims["sub"],
      "session_id" => Tokens.uuid(),
      "at" => claims["iat_ms"] + 1
    })

    assert {:ok, _} = connect_claims(claims)
  end
end
