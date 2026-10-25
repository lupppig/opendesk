defmodule RealtimeWeb.WorkspacesTest do
  @moduledoc """
  A person's token covers their other workspaces too (the `workspaces` claim): one
  socket joins each of them with the role and private teams it has there, and
  nothing of a workspace it does not list.
  """
  use RealtimeWeb.ChannelCase, async: true

  alias Phoenix.Socket.Broadcast
  alias RealtimeWeb.{Endpoint, TeamChannel, TicketChannel, WorkspaceChannel}

  @forbidden {:error, %{reason: "forbidden"}}

  setup do
    [a, b, c] = [Tokens.uuid(), Tokens.uuid(), Tokens.uuid()]
    [team_a, team_b, hidden_b] = [Tokens.uuid(), Tokens.uuid(), Tokens.uuid()]

    claims =
      Tokens.ws_claims(%{
        "wid" => a,
        "role" => "agent",
        "teams" => [team_a],
        "workspaces" => %{b => %{"role" => "viewer", "teams" => [team_b]}}
      })

    {:ok, socket} = connect_claims(claims)

    %{
      socket: socket,
      uid: claims["sub"],
      a: a,
      b: b,
      c: c,
      team_a: team_a,
      team_b: team_b,
      hidden_b: hidden_b
    }
  end

  test "joins every workspace the token lists", c do
    assert {:ok, _, _} = subscribe_and_join(c.socket, WorkspaceChannel, "workspace:#{c.a}")
    assert {:ok, _, _} = subscribe_and_join(c.socket, WorkspaceChannel, "workspace:#{c.b}")
  end

  test "refuses a workspace the token does not list", c do
    assert subscribe_and_join(c.socket, WorkspaceChannel, "workspace:#{c.c}") == @forbidden

    assert subscribe_and_join(c.socket, WorkspaceChannel, "workspace:#{String.upcase(c.b)}") ==
             @forbidden

    # Another person's token naming B is still not this socket's.
    {:ok, other} = connect_claims(Tokens.ws_claims(%{"wid" => c.c}))
    assert subscribe_and_join(other, WorkspaceChannel, "workspace:#{c.b}") == @forbidden
  end

  test "joins a private team only in the workspace that lists it", c do
    assert {:ok, _, _} = subscribe_and_join(c.socket, TeamChannel, "team:#{c.b}:#{c.team_b}")
    assert {:ok, _, _} = subscribe_and_join(c.socket, TeamChannel, "team:#{c.a}:#{c.team_a}")

    for topic <- [
          "team:#{c.b}:#{c.hidden_b}",
          "team:#{c.b}:#{c.team_a}",
          "team:#{c.a}:#{c.team_b}",
          "team:#{c.c}:#{c.team_b}"
        ] do
      assert subscribe_and_join(c.socket, TeamChannel, topic) == @forbidden, topic
    end
  end

  test "joins a ticket of another listed workspace with that workspace's teams", c do
    tid = Tokens.uuid()

    assert {:ok, _, ch} =
             subscribe_and_join(c.socket, TicketChannel, "ticket:#{c.b}:#{tid}", %{
               "team_id" => c.team_b
             })

    assert ch.assigns.wid == c.b
    assert ch.assigns.role == "viewer"
    assert ch.assigns.teams == [c.team_b]

    for {topic, params} <- [
          {"ticket:#{c.b}:#{tid}", %{"team_id" => c.team_a}},
          {"ticket:#{c.b}:#{tid}", %{"team_id" => c.hidden_b}},
          {"ticket:#{c.c}:#{tid}", %{}}
        ] do
      assert subscribe_and_join(c.socket, TicketChannel, topic, params) == @forbidden, topic
    end
  end

  test "acts with its role in the topic's workspace, and no push reaches a visitor from either",
       c do
    {cid, tid} = {Tokens.uuid(), Tokens.uuid()}
    {:ok, _, in_b} = subscribe_and_join(c.socket, TicketChannel, "ticket:#{c.b}:#{tid}")
    assert_push "presence_state", _
    {:ok, _, in_a} = subscribe_and_join(c.socket, TicketChannel, "ticket:#{c.a}:#{tid}")
    assert_push "presence_state", _
    assert {in_a.assigns.role, in_a.assigns.teams} == {"agent", [c.team_a]}
    assert {in_b.assigns.role, in_b.assigns.teams} == {"viewer", [c.team_b]}

    # The visitor sees an agent typing through the API, which checks the ticket.
    :ok = Endpoint.subscribe("contact:#{c.a}:#{cid}")
    :ok = Endpoint.subscribe("contact:#{c.b}:#{cid}")
    push(in_a, "typing", %{"contact_id" => cid})
    push(in_b, "typing", %{"contact_id" => cid})
    refute_receive %Broadcast{topic: "contact:" <> _}, 100
  end

  test "a private team's presence in B stays with B's team", c do
    tid = Tokens.uuid()

    {:ok, _, _} =
      subscribe_and_join(c.socket, TicketChannel, "ticket:#{c.b}:#{tid}", %{
        "team_id" => c.team_b
      })

    assert_push "presence_state", _

    # Someone in B without the team joins the same ticket: they never see the meta.
    {:ok, outsider} = connect_claims(Tokens.ws_claims(%{"wid" => c.b}))
    {:ok, _, _} = subscribe_and_join(outsider, TicketChannel, "ticket:#{c.b}:#{tid}")
    assert_push "presence_state", state
    refute Map.has_key?(state, c.uid)
  end
end
