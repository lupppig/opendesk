defmodule RealtimeWeb.ChannelCase do
  @moduledoc "Channel tests: connect through `UserSocket.connect/3` with real tokens."

  use ExUnit.CaseTemplate

  alias Realtime.Test.Tokens

  using do
    quote do
      import Phoenix.ChannelTest
      import RealtimeWeb.ChannelCase
      alias Realtime.Test.Tokens

      @endpoint RealtimeWeb.Endpoint
    end
  end

  @doc "The socket a token's purpose belongs on."
  def socket_for(%{"purpose" => "widget_ws"}), do: RealtimeWeb.WidgetSocket
  def socket_for(_claims), do: RealtimeWeb.UserSocket

  @doc "Connects with `claims` signed as a valid token, on the socket for its purpose."
  defmacro connect_claims(claims, ip \\ quote(do: Tokens.random_ip())) do
    quote do
      claims = unquote(claims)

      Phoenix.ChannelTest.connect(
        RealtimeWeb.ChannelCase.socket_for(claims),
        %{},
        connect_info: %{
          peer_data: %{address: unquote(ip), port: 1234},
          x_headers: [],
          auth_token: Tokens.sign(claims)
        }
      )
    end
  end
end
