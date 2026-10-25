defmodule RealtimeWeb.PresenceTypingTest do
  use RealtimeWeb.ChannelCase, async: true

  alias Phoenix.Socket.Broadcast
  alias RealtimeWeb.{ContactChannel, Endpoint, Presence, TicketChannel}

  setup do
    agent = Tokens.ws_claims()
    contact = Tokens.widget_claims(%{"wid" => agent["wid"]})
    {:ok, a} = connect_claims(agent)
    {:ok, k} = connect_claims(contact)
    wid = agent["wid"]
    tid = Tokens.uuid()
    topic = "ticket:#{wid}:#{tid}"
    {:ok, _, ticket} = subscribe_and_join(a, TicketChannel, topic)

    %{
      wid: wid,
      tid: tid,
      topic: topic,
      uid: agent["sub"],
      cid: contact["sub"],
      ticket: ticket,
      contact_socket: k
    }
  end

  describe "presence" do
    test "joining tracks the user as viewing and pushes presence_state", c do
      uid = c.uid
      assert_push "presence_state", %{^uid => %{metas: [meta]}}

      assert %{
               user_id: ^uid,
               name: "Anna",
               avatar: "https://example.test/a.png",
               state: "viewing",
               online_at: online_at
             } = meta

      assert {:ok, _, _} = DateTime.from_iso8601(online_at)
      assert %{^uid => _} = Presence.list(c.topic)
    end

    test "a second agent sees the first in presence_state and a diff on join", c do
      uid = c.uid
      assert_push "presence_state", _
      other = Tokens.ws_claims(%{"wid" => c.wid, "name" => "Ben"})
      {:ok, o} = connect_claims(other)
      {:ok, _, _} = subscribe_and_join(o, TicketChannel, c.topic)

      assert_push "presence_state", state
      assert Map.has_key?(state, uid)
      assert Map.has_key?(state, other["sub"])
      osub = other["sub"]

      assert_receive %Broadcast{
        event: "presence_diff",
        payload: %{joins: %{^osub => %{metas: [%{name: "Ben", state: "viewing"}]}}}
      }
    end

    test "presence pushes update the state", c do
      for state <- ~w(replying idle viewing) do
        ref = push(c.ticket, "presence", %{"state" => state})
        assert_reply ref, :ok
        assert %{metas: [%{state: ^state}]} = Presence.list(c.topic)[c.uid]
      end
    end

    test "a watcher gets presence but is not tracked", c do
      uid = c.uid
      assert_push "presence_state", _
      other = Tokens.ws_claims(%{"wid" => c.wid, "name" => "Ben"})
      {:ok, o} = connect_claims(other)
      {:ok, _, w} = subscribe_and_join(o, TicketChannel, c.topic, %{"watch" => true})

      assert_push "presence_state", state
      assert Map.has_key?(state, uid)
      refute Map.has_key?(Presence.list(c.topic), other["sub"])

      push(w, "presence", %{"state" => "replying"})
      push(w, "typing", %{})
      refute_receive %Broadcast{event: "typing"}, 100
      refute Map.has_key?(Presence.list(c.topic), other["sub"])
    end

    test "the join state sets the initial presence state", c do
      assert_push "presence_state", _
      other = Tokens.ws_claims(%{"wid" => c.wid, "name" => "Ben"})
      {:ok, o} = connect_claims(other)
      {:ok, _, _} = subscribe_and_join(o, TicketChannel, c.topic, %{"state" => "replying"})
      assert_push "presence_state", _
      assert %{metas: [%{state: "replying"}]} = Presence.list(c.topic)[other["sub"]]
    end

    test "invalid presence states are refused", c do
      for payload <- [%{"state" => "away"}, %{"state" => 1}, %{}, %{"status" => "idle"}] do
        ref = push(c.ticket, "presence", payload)
        assert_reply ref, :error, %{reason: "invalid"}
      end

      assert %{metas: [%{state: "viewing"}]} = Presence.list(c.topic)[c.uid]
    end
  end

  describe "typing on a ticket" do
    test "goes to the other agents on the ticket", c do
      push(c.ticket, "typing", %{})
      uid = c.uid
      assert_broadcast "typing", %{user_id: ^uid, name: "Anna"}
    end

    test "a contact_id forwards nothing to the contact topic", c do
      contact_topic = "contact:#{c.wid}:#{c.cid}"
      {:ok, _, _} = subscribe_and_join(c.contact_socket, ContactChannel, contact_topic)
      :ok = Endpoint.subscribe(contact_topic)
      uid = c.uid

      for payload <- [
            %{"contact_id" => c.cid},
            %{"contact_id" => "../../x"},
            %{"contact_id" => 5}
          ] do
        ref = push(c.ticket, "typing", payload)
        assert_broadcast "typing", %{user_id: ^uid}
        refute_reply ref, :error, %{reason: "invalid"}, 20
      end

      refute_receive %Broadcast{topic: ^contact_topic, event: "typing"}, 100
      refute_receive %Phoenix.Socket.Message{topic: ^contact_topic, event: "typing"}, 10
    end

    test "a viewer's typing never reaches the customer", c do
      viewer = Tokens.ws_claims(%{"wid" => c.wid, "role" => "viewer", "name" => "Vic"})
      {:ok, v} = connect_claims(viewer)
      {:ok, _, vt} = subscribe_and_join(v, TicketChannel, c.topic)
      :ok = Endpoint.subscribe("contact:#{c.wid}:#{c.cid}")
      push(vt, "typing", %{"contact_id" => c.cid})
      refute_receive %Broadcast{topic: "contact:" <> _, event: "typing"}, 100
    end
  end

  describe "typing from the widget" do
    test "is accepted and sends nothing, whatever the ticket_id", c do
      {:ok, _, contact} =
        subscribe_and_join(c.contact_socket, ContactChannel, "contact:#{c.wid}:#{c.cid}")

      :ok = Endpoint.subscribe(c.topic)

      for payload <- [%{"ticket_id" => c.tid}, %{"ticket_id" => "nope"}, %{}, %{"ticket_id" => 5}] do
        ref = push(contact, "typing", payload)
        refute_reply ref, :error, %{reason: "invalid"}, 20
      end

      refute_receive %Broadcast{event: "customer_typing"}, 100
      refute_receive %Phoenix.Socket.Message{event: "customer_typing"}, 10
      assert Process.alive?(contact.channel_pid)
    end
  end
end
