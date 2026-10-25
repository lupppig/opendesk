defmodule RealtimeWeb.SocketAgeTest do
  @moduledoc "An agents' socket closes at its maximum age; the widget's has none."
  # Changes :max_socket_age for the whole node, so it runs on its own.
  use RealtimeWeb.ChannelCase, async: false

  alias Phoenix.Socket.Broadcast
  alias RealtimeWeb.SocketAuth

  setup do
    saved = Application.get_env(:realtime, :max_socket_age)
    Application.put_env(:realtime, :max_socket_age, 1)
    on_exit(fn -> Application.put_env(:realtime, :max_socket_age, saved) end)
  end

  test "the age is a random 80 to 100 % of MAX_SOCKET_AGE, and 0 is none" do
    ages = for _ <- 1..500, do: SocketAuth.max_age_ms(3600)
    assert Enum.all?(ages, &(&1 >= 2_880_000 and &1 <= 3_600_000))
    assert length(Enum.uniq(ages)) > 1
    assert SocketAuth.max_age_ms(0) == nil
  end

  test "an agent's or API key's socket is sent the disconnect that closes it" do
    for kind <- ["user", "api_key"] do
      claims = Tokens.ws_claims(%{"kind" => kind})
      id = "user_socket:" <> claims["sub"]
      # connect/3 runs in the calling process, which stands in for the socket's.
      assert {:ok, _} = connect_claims(claims)
      refute_receive %Broadcast{topic: ^id}, 700
      assert_receive %Broadcast{topic: ^id, event: "disconnect", payload: %{}}, 800
    end
  end

  test "with no maximum age nothing is scheduled" do
    Application.put_env(:realtime, :max_socket_age, 0)
    assert {:ok, _} = connect_claims(Tokens.ws_claims())
    refute_receive %Broadcast{event: "disconnect"}, 1_200
  end

  test "the widget's socket is not aged" do
    assert {:ok, _} = connect_claims(Tokens.widget_claims())
    refute_receive %Broadcast{event: "disconnect"}, 1_200
  end
end
