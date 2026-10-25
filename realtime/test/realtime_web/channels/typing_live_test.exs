defmodule RealtimeWeb.TypingLiveTest do
  @moduledoc """
  Typing between agents and visitors arrives from the API as live events and is
  delivered in the shapes clients know: to one topic, filtered by team or brand.
  """
  use RealtimeWeb.ChannelCase, async: true

  alias Phoenix.Socket.Message
  alias Realtime.Fanout
  alias RealtimeWeb.{ContactChannel, TicketChannel}

  setup do
    wid = Tokens.uuid()
    tid = Tokens.uuid()
    cid = Tokens.uuid()
    team = Tokens.uuid()
    brand = Tokens.uuid()
    agent = Tokens.uuid()

    %{
      wid: wid,
      tid: tid,
      cid: cid,
      team: team,
      brand: brand,
      agent: agent,
      ticket: "ticket:#{wid}:#{tid}",
      contact: "contact:#{wid}:#{cid}"
    }
  end

  # Joins in its own process (its own transport) and forwards what the socket is pushed.
  defp join_as(claims, channel, topic, params) do
    parent = self()
    {:ok, socket} = connect_claims(claims)

    pid =
      spawn_link(fn ->
        {:ok, _, _} = join(%{socket | transport_pid: self()}, channel, topic, params)
        send(parent, {:joined, self()})
        forward(parent)
      end)

    assert_receive {:joined, ^pid}
    pid
  end

  defp forward(parent) do
    receive do
      %Message{event: event} = msg when event not in ["presence_state", "presence_diff"] ->
        send(parent, {self(), msg})

      _ ->
        :ok
    end

    forward(parent)
  end

  defp agent_socket(c, overrides \\ %{}) do
    claims = Tokens.ws_claims(Map.merge(%{"wid" => c.wid}, overrides))
    join_as(claims, TicketChannel, c.ticket, %{"watch" => true})
  end

  defp member_socket(c) do
    claims = Tokens.ws_claims(%{"wid" => c.wid, "teams" => [c.team]})
    join_as(claims, TicketChannel, c.ticket, %{"team_id" => c.team, "watch" => true})
  end

  defp visitor_socket(c, brand) do
    claims = Tokens.widget_claims(%{"wid" => c.wid, "sub" => c.cid})
    claims = if brand, do: Map.put(claims, "bid", brand), else: claims
    join_as(claims, ContactChannel, c.contact, %{})
  end

  defp publish(c, type, extra) do
    ev =
      Map.merge(
        %{
          "workspace_id" => c.wid,
          "ticket_id" => c.tid,
          "contact_id" => c.cid,
          "type" => type,
          "data" => %{}
        },
        extra
      )

    Fanout.handle("od.live." <> c.wid, Jason.encode!(ev))
  end

  defp agent_typing(c, extra \\ %{}) do
    publish(
      c,
      "agent.typing",
      Map.merge(%{"data" => %{"user" => %{"id" => c.agent, "name" => "Anna"}}}, extra)
    )
  end

  describe "agent.typing" do
    test "reaches the visitor as typing {ticket_id, user}, and no agent", c do
      visitor = visitor_socket(c, nil)
      agent = agent_socket(c)

      assert {:ok, [contact]} = agent_typing(c)
      assert contact == c.contact

      expected = %{"ticket_id" => c.tid, "user" => %{"id" => c.agent, "name" => "Anna"}}
      assert_receive {^visitor, %Message{event: "typing", payload: ^expected}}
      refute_receive {^agent, %Message{}}, 100
    end

    test "reaches only widget sockets of the chat's brand, without the brand", c do
      mine = visitor_socket(c, c.brand)
      other = visitor_socket(c, Tokens.uuid())

      assert {:ok, _} = agent_typing(c, %{"brand_id" => String.upcase(c.brand)})
      assert_receive {^mine, %Message{event: "typing", payload: payload}}
      refute Map.has_key?(payload, "brand_id")
      refute_receive {^other, %Message{}}, 100
    end

    test "without a well-formed user is dropped", c do
      for data <- [%{}, %{"user" => %{"name" => "Anna"}}, %{"user" => %{"id" => "x"}}] do
        assert publish(c, "agent.typing", %{"data" => data}) == {:error, :invalid_live}
      end
    end
  end

  describe "customer.typing" do
    test "reaches agents as customer_typing {ticket_id, contact_id}, and not the visitor", c do
      agent = agent_socket(c)
      visitor = visitor_socket(c, nil)

      assert {:ok, [ticket]} = publish(c, "customer.typing", %{})
      assert ticket == c.ticket

      expected = %{"ticket_id" => c.tid, "contact_id" => c.cid}
      assert_receive {^agent, %Message{event: "customer_typing", payload: ^expected}}
      refute_receive {^visitor, %Message{}}, 100
    end

    test "of a private team's chat reaches only the team, without the team", c do
      member = member_socket(c)
      outsider = agent_socket(c)

      assert {:ok, _} = publish(c, "customer.typing", %{"team_id" => c.team})
      expected = %{"ticket_id" => c.tid, "contact_id" => c.cid}
      assert_receive {^member, %Message{event: "customer_typing", payload: ^expected}}
      refute_receive {^outsider, %Message{}}, 100
    end
  end

  test "typing for another workspace or with malformed ids is dropped", c do
    assert Fanout.handle("od.live." <> Tokens.uuid(), Jason.encode!(%{"workspace_id" => c.wid})) ==
             {:error, :invalid_live}

    assert publish(c, "customer.typing", %{"workspace_id" => Tokens.uuid()}) ==
             {:error, :workspace_mismatch}

    assert publish(c, "customer.typing", %{"ticket_id" => "x"}) == {:error, :invalid_live}
    assert publish(c, "customer.typing", %{"team_id" => "x"}) == {:error, :invalid_live}
    assert publish(c, "agent.typing", %{"contact_id" => "x"}) == {:error, :invalid_live}
    assert publish(c, "someone.typing", %{}) == {:error, :invalid_live}
  end
end
