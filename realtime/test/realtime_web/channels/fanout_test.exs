defmodule RealtimeWeb.FanoutTest do
  use RealtimeWeb.ChannelCase, async: true

  alias Phoenix.Socket.Message
  alias Realtime.Fanout
  alias RealtimeWeb.{ContactChannel, TeamChannel, UserChannel, WorkspaceChannel}

  setup do
    agent = Tokens.ws_claims()
    other = Tokens.ws_claims(%{"wid" => agent["wid"]})
    contact = Tokens.widget_claims(%{"wid" => agent["wid"]})
    {:ok, a} = connect_claims(agent)
    {:ok, o} = connect_claims(other)
    {:ok, k} = connect_claims(contact)
    wid = agent["wid"]

    {:ok, _, _} = subscribe_and_join(a, WorkspaceChannel, "workspace:#{wid}")
    {:ok, _, _} = subscribe_and_join(a, UserChannel, "user:#{agent["sub"]}")
    {:ok, _, _} = subscribe_and_join(o, UserChannel, "user:#{other["sub"]}")
    {:ok, _, _} = subscribe_and_join(k, ContactChannel, "contact:#{wid}:#{contact["sub"]}")

    %{wid: wid, uid: agent["sub"], other: other["sub"], cid: contact["sub"]}
  end

  defp envelope(c, visibility, topics) do
    %{
      "id" => Tokens.uuid(),
      "seq" => System.unique_integer([:positive]),
      "workspace_id" => c.wid,
      "type" => "message.created",
      "visibility" => visibility,
      "topics" => topics,
      "data" => %{"ticket_id" => Tokens.uuid(), "message" => %{"body" => "hi"}},
      "occurred_at" => "2026-10-11T09:00:00Z"
    }
  end

  defp publish(c, env), do: Fanout.handle("od.events." <> c.wid, Jason.encode!(env))

  test "a public event reaches the workspace, the listed user and the listed contact", c do
    ws = "workspace:#{c.wid}"
    user = "user:#{c.uid}"
    contact = "contact:#{c.wid}:#{c.cid}"
    env = envelope(c, "public", [ws, user, contact])

    assert {:ok, topics} = publish(c, env)
    assert Enum.sort(topics) == Enum.sort([ws, user, contact])

    expected = Map.delete(env, "topics")
    assert_receive %Message{topic: ^ws, event: "event", payload: ^expected}
    assert_receive %Message{topic: ^user, event: "event", payload: ^expected}
    assert_receive %Message{topic: ^contact, event: "event", payload: ^expected}
    refute_receive %Message{}, 50
  end

  test "an agents event never reaches a contact topic, even when listed", c do
    env = envelope(c, "agents", ["workspace:#{c.wid}", "contact:#{c.wid}:#{c.cid}"])
    assert {:ok, ["workspace:" <> _]} = publish(c, env)

    assert_push "event", %{"visibility" => "agents"}
    refute_receive %Message{topic: "contact:" <> _}, 50
  end

  test "an event without user topics reaches no user channel", c do
    assert {:ok, [_]} = publish(c, envelope(c, "agents", ["workspace:#{c.wid}"]))
    ws = "workspace:#{c.wid}"
    assert_receive %Message{topic: ^ws}
    refute_receive %Message{topic: "user:" <> _}, 50
  end

  test "only the listed user receives a user event", c do
    assert {:ok, _} = publish(c, envelope(c, "agents", ["user:#{c.other}"]))
    other = "user:#{c.other}"
    uid = "user:#{c.uid}"
    assert_receive %Message{topic: ^other, event: "event"}
    refute_receive %Message{topic: ^uid}, 50
  end

  test "contact topics of another workspace and malformed topics are ignored", c do
    env =
      envelope(c, "public", [
        "contact:#{Tokens.uuid()}:#{c.cid}",
        "contact:#{c.wid}:nope",
        "user:nope",
        "workspace:#{Tokens.uuid()}",
        42
      ])

    assert publish(c, env) == {:ok, ["workspace:#{c.wid}"]}
  end

  test "envelopes that do not match their subject or are malformed are dropped", c do
    env = envelope(c, "agents", [])

    assert Fanout.handle("od.events." <> Tokens.uuid(), Jason.encode!(env)) ==
             {:error, :workspace_mismatch}

    assert Fanout.handle("od.events.not-a-uuid", Jason.encode!(env)) ==
             {:error, :invalid_subject}

    assert publish(c, %{env | "visibility" => "secret"}) == {:error, :invalid_envelope}
    assert publish(c, %{env | "topics" => "x"}) == {:error, :invalid_envelope}
    assert publish(c, Map.delete(env, "workspace_id")) == {:error, :invalid_envelope}
    assert Fanout.handle("od.events." <> c.wid, "{not json") == {:error, :invalid_json}
    assert Fanout.handle("od.events." <> c.wid, "[1]") == {:error, :invalid_json}
    assert Fanout.handle("od.other", "{}") == {:error, :unknown_subject}
    refute_receive %Message{}, 50
  end

  test "a repeated or older seq for the workspace is dropped", c do
    env = envelope(c, "agents", [])
    assert {:ok, _} = publish(c, env)
    assert publish(c, env) == {:error, :duplicate}
    assert publish(c, %{env | "seq" => env["seq"] - 1}) == {:error, :duplicate}
    assert {:ok, _} = publish(c, %{env | "seq" => env["seq"] + 1})

    seqs =
      for _ <- 1..2 do
        assert_receive %Message{event: "event", payload: %{"seq" => seq}}
        seq
      end

    assert seqs == [env["seq"], env["seq"] + 1]
    refute_receive %Message{}, 50
  end

  describe "events of a private team" do
    setup c do
      team = Tokens.uuid()
      member = Tokens.ws_claims(%{"wid" => c.wid, "teams" => [team]})
      {:ok, m} = connect_claims(member)
      {:ok, _, _} = subscribe_and_join(m, TeamChannel, "team:#{c.wid}:#{team}")
      %{team: team}
    end

    test "go in full to the team, as a stand-in to the workspace", c do
      ws = "workspace:#{c.wid}"
      team = "team:#{c.wid}:#{c.team}"
      env = envelope(c, "agents", [team])

      assert {:ok, topics} = publish(c, env)
      assert Enum.sort(topics) == Enum.sort([ws, team])

      full = Map.delete(env, "topics")
      assert_receive %Message{topic: ^team, event: "event", payload: ^full}

      stand_in = %{
        "id" => env["id"],
        "seq" => env["seq"],
        "workspace_id" => c.wid,
        "type" => "event.hidden",
        "visibility" => "agents",
        "data" => %{},
        "occurred_at" => env["occurred_at"]
      }

      assert_receive %Message{topic: ^ws, event: "event", payload: ^stand_in}
      refute_receive %Message{}, 50
    end

    test "still reach the listed user and, when public, the contact in full", c do
      ws = "workspace:#{c.wid}"
      user = "user:#{c.uid}"
      contact = "contact:#{c.wid}:#{c.cid}"
      env = envelope(c, "public", ["team:#{c.wid}:#{c.team}", user, contact])

      assert {:ok, _} = publish(c, env)
      full = Map.delete(env, "topics")
      assert_receive %Message{topic: ^user, payload: ^full}
      assert_receive %Message{topic: ^contact, payload: ^full}
      assert_receive %Message{topic: ^ws, payload: %{"type" => "event.hidden", "data" => %{}}}
    end

    test "a team topic of another workspace or a malformed one reaches no team but still hides the event",
         c do
      ws = "workspace:#{c.wid}"

      for listed <- ["team:#{Tokens.uuid()}:#{c.team}", "team:#{c.wid}:nope", "team:#{c.wid}"] do
        env = envelope(c, "agents", [listed])
        assert publish(c, env) == {:ok, [ws]}, listed
        assert_receive %Message{topic: ^ws, payload: %{"type" => "event.hidden"} = payload}
        refute Map.has_key?(payload, "topics")
        refute_receive %Message{topic: "team:" <> _}, 20
      end
    end

    test "an uppercase team id is normalized", c do
      team = "team:#{c.wid}:#{c.team}"
      env = envelope(c, "agents", ["team:#{c.wid}:#{String.upcase(c.team)}"])
      assert {:ok, _} = publish(c, env)
      assert_receive %Message{topic: ^team, payload: %{"type" => "message.created"}}
    end
  end

  describe "events scoped to people" do
    test "go in full to the listed users, as a stand-in to the workspace", c do
      ws = "workspace:#{c.wid}"
      user = "user:#{c.uid}"
      env = envelope(c, "agents", ["private", user])

      assert {:ok, topics} = publish(c, env)
      assert Enum.sort(topics) == Enum.sort([ws, user])

      full = Map.delete(env, "topics")
      assert_receive %Message{topic: ^user, event: "event", payload: ^full}

      assert_receive %Message{
        topic: ^ws,
        event: "event",
        payload: %{"type" => "event.hidden", "data" => %{}} = stand_in
      }

      refute Map.has_key?(stand_in, "topics")
      assert stand_in["seq"] == env["seq"]
      refute_receive %Message{}, 50
    end

    test "with nobody listed reach nobody in full", c do
      ws = "workspace:#{c.wid}"
      env = envelope(c, "agents", ["private"])

      assert publish(c, env) == {:ok, [ws]}
      assert_receive %Message{topic: ^ws, payload: %{"type" => "event.hidden"}}
      refute_receive %Message{}, 50
    end
  end

  test "the broadcast function is injectable", c do
    test = self()
    env = envelope(c, "public", ["contact:#{c.wid}:#{c.cid}"])

    assert {:ok, _} =
             Fanout.handle("od.events." <> c.wid, Jason.encode!(env), fn topic, event, payload ->
               send(test, {:sent, topic, event, payload})
             end)

    assert_receive {:sent, "workspace:" <> _, "event", payload}
    refute Map.has_key?(payload, "topics")
    assert_receive {:sent, "contact:" <> _, "event", _}
    # Nothing went through the PubSub.
    refute_receive %Message{}, 50
  end
end
