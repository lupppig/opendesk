defmodule Realtime.Fanout do
  @moduledoc """
  Turns NATS messages into channel broadcasts (`contracts/events.md`).

  Every gateway node subscribes to NATS on its own (no queue group), so every
  node receives every message exactly once and broadcasts it only to the
  sockets connected to it (`local_broadcast`). Each socket lives on one node,
  so each client receives each event once, in the order NATS delivered it.

  Fan-out of an envelope on `od.events.<wid>`:

    * `workspace:<wid>` always (agents see both visibilities);
    * every `team:<wid>:<tid>` listed in `topics`, for the envelope's own
      workspace: the event is about a private team's ticket
      (`contracts/teams.md`, "Visibility");
    * every `user:<uid>` listed in `topics` (the user's own notifications and
      assignments, which go only to users who can see the ticket);
    * every `contact:<wid>:<cid>` listed in `topics`, only when visibility is
      `public`, and only for the envelope's own workspace. A listed
      `contact:<wid>:<cid>:<bid>` names the chat's brand: the payload pushed to
      `contact:<wid>:<cid>` then carries `brand_id`, and
      `RealtimeWeb.ContactChannel` pushes it only to sockets of that brand.

  The pushed payload is the envelope minus `topics`. When `topics` names any
  `team:` topic, or `private` (an event scoped to the people whose `user:`
  topics it lists), `workspace:<wid>` gets only the stand-in
  `{id, seq, workspace_id, type: "event.hidden", visibility: "agents", data: {},
  occurred_at}`, which keeps `seq` contiguous for every agent without
  revealing the event; a malformed `team:` topic still hides the event from the
  workspace. Repeats of an envelope (the relay delivers at least once) are
  dropped by `Realtime.Fanout.Dedupe`.

  Live previews on `od.live.<wid>` (`contracts/realtime.md`, "Live events") are
  pushed as `"live"` `{type, data}` to `contact:<wid>:<contact_id>` and
  `ticket:<wid>:<ticket_id>`. A preview of a private team's ticket names the
  team (`team_id`); the ticket topic's copy keeps it so
  `RealtimeWeb.TicketChannel` pushes it only to sockets with that team. Its
  `brand_id` stays on the contact topic's copy for `RealtimeWeb.ContactChannel`. They
  carry no `seq` and are never deduplicated or replayed. Typing between an agent and
  a visitor is a live event too, published by the API once it has checked the ticket
  (`live/2`): an agent's goes only to the contact topic as `"typing"`, a visitor's
  only to the ticket topic as `"customer_typing"`.

  `od.control.disconnect` `{user_id, session_id?, at?}` closes the subject's sockets on
  this node and records the revocation (`Realtime.Revocations`), so a token issued
  before it cannot open a new one.
  """

  require Logger

  alias Realtime.Fanout.Dedupe
  alias Realtime.Ids
  alias Realtime.Metrics
  alias Realtime.Revocations

  @events_prefix "od.events."
  @live_prefix "od.live."
  @disconnect "od.control.disconnect"
  @live_types ~w(ai.reply.started ai.reply.delta ai.reply.tool ai.reply.ended)
  @typing_types ~w(agent.typing customer.typing)

  @doc "NATS subjects the gateway subscribes to."
  def subjects, do: [@events_prefix <> ">", @live_prefix <> ">", @disconnect]

  @doc """
  Handles one NATS message. `broadcast` is `(topic, event, payload) -> any`,
  `RealtimeWeb.Endpoint.local_broadcast/3` by default.
  """
  @spec handle(String.t(), iodata(), (String.t(), String.t(), map() -> any())) ::
          {:ok, [String.t()]} | {:error, atom()}
  def handle(subject, body, broadcast \\ &RealtimeWeb.Endpoint.local_broadcast/3)

  def handle(@disconnect, body, broadcast) do
    with {:ok, %{"user_id" => user_id} = msg} <- decode(body),
         true <- Ids.uuid?(user_id) do
      sub = Ids.normalize(user_id)
      Revocations.record(revoked_key(sub, msg["session_id"]), revoked_at(msg["at"]))
      topic = "user_socket:" <> sub
      broadcast.(topic, "disconnect", %{})
      Metrics.incr(:disconnects)
      {:ok, [topic]}
    else
      _ -> reject(:invalid_disconnect)
    end
  end

  def handle(@events_prefix <> wid, body, broadcast) do
    with true <- Ids.uuid?(wid),
         {:ok, envelope} <- decode(body),
         {:ok, routes} <- routes(Ids.normalize(wid), envelope),
         :ok <- Dedupe.check(Ids.normalize(wid), envelope["seq"]) do
      Enum.each(routes, fn {topic, payload} -> broadcast.(topic, "event", payload) end)
      Metrics.incr(:events)
      {:ok, Enum.map(routes, &elem(&1, 0))}
    else
      :duplicate ->
        Metrics.incr(:duplicates)
        {:error, :duplicate}

      {:error, reason} ->
        reject(reason)

      _ ->
        reject(:invalid_subject)
    end
  end

  def handle(@live_prefix <> wid, body, broadcast) do
    with true <- Ids.uuid?(wid),
         {:ok, event} <- decode(body),
         {:ok, routes} <- live(Ids.normalize(wid), event) do
      Enum.each(routes, fn {topic, name, payload} -> broadcast.(topic, name, payload) end)
      Metrics.incr(:live)
      {:ok, Enum.map(routes, &elem(&1, 0))}
    else
      {:error, reason} -> reject(reason)
      _ -> reject(:invalid_subject)
    end
  end

  def handle(_subject, _body, _broadcast), do: reject(:unknown_subject)

  # A session's revocation is keyed by the session; anything else revokes the whole subject.
  defp revoked_key(sub, session) do
    if Ids.uuid?(session), do: {:sid, Ids.normalize(session)}, else: {:sub, sub}
  end

  # The API's revocation time; without one (an older API), when this node received it.
  defp revoked_at(at) when is_integer(at) and at > 0, do: at
  defp revoked_at(_at), do: System.system_time(:millisecond)

  @doc """
  The `{topic, event, payload}` triples of a live event from workspace `wid`.

  A reply preview goes to both topics as `"live"`. Typing, which the API sends only
  after checking the ticket, goes to one topic in the shape clients know: an agent's
  (`agent.typing`) to the contact topic as `"typing"` `{ticket_id, user}`, a visitor's
  (`customer.typing`) to the ticket topic as `"customer_typing"` `{ticket_id, contact_id}`.
  A ticket topic's payload carries the event's `team_id` and a contact topic's its
  `brand_id`, for the channels to filter on and strip.
  """
  @spec live(String.t(), map()) :: {:ok, [{String.t(), String.t(), map()}]} | {:error, atom()}
  def live(
        wid,
        %{
          "workspace_id" => event_wid,
          "ticket_id" => tid,
          "contact_id" => cid,
          "type" => type,
          "data" => data
        } = event
      )
      when is_binary(event_wid) and is_binary(tid) and is_binary(cid) and is_map(data) do
    with :ok <- live_type(type),
         :ok <- same_workspace(event_wid, wid),
         :ok <- live_ids(tid, cid),
         {:ok, team} <- live_team(Map.get(event, "team_id")),
         {:ok, brand} <- live_team(Map.get(event, "brand_id")) do
      tid = Ids.normalize(tid)
      cid = Ids.normalize(cid)
      ticket = {"ticket:#{wid}:#{tid}", team}
      contact = {"contact:#{wid}:#{cid}", brand}
      live_routes(type, data, tid, cid, ticket, contact)
    end
  end

  def live(_wid, _event), do: {:error, :invalid_live}

  defp live_routes("agent.typing", data, tid, _cid, _ticket, contact) do
    with {:ok, user} <- typing_user(data) do
      {:ok, [scoped(contact, "typing", %{"ticket_id" => tid, "user" => user})]}
    end
  end

  defp live_routes("customer.typing", _data, tid, cid, ticket, _contact),
    do: {:ok, [scoped(ticket, "customer_typing", %{"ticket_id" => tid, "contact_id" => cid})]}

  defp live_routes(type, data, _tid, _cid, ticket, contact) do
    payload = %{"type" => type, "data" => data}
    {:ok, [scoped(ticket, "live", payload), scoped(contact, "live", payload)]}
  end

  defp live_type(type) when type in @live_types or type in @typing_types, do: :ok
  defp live_type(_type), do: {:error, :invalid_live}

  # The scope (team or brand) rides on the payload under its topic's key.
  defp scoped({"ticket:" <> _ = topic, team}, name, payload),
    do: {topic, name, put_scope(payload, "team_id", team)}

  defp scoped({"contact:" <> _ = topic, brand}, name, payload),
    do: {topic, name, put_scope(payload, "brand_id", brand)}

  defp put_scope(payload, _key, nil), do: payload
  defp put_scope(payload, key, value), do: Map.put(payload, key, value)

  defp typing_user(%{"user" => %{"id" => id} = user}) do
    name = user["name"]
    name = if is_binary(name) and byte_size(name) <= 200, do: name

    if Ids.uuid?(id),
      do: {:ok, %{"id" => Ids.normalize(id), "name" => name}},
      else: {:error, :invalid_live}
  end

  defp typing_user(_data), do: {:error, :invalid_live}

  defp same_workspace(event_wid, wid) do
    if Ids.uuid?(event_wid) and Ids.normalize(event_wid) == wid,
      do: :ok,
      else: {:error, :workspace_mismatch}
  end

  defp live_team(nil), do: {:ok, nil}

  defp live_team(team) do
    if Ids.uuid?(team), do: {:ok, Ids.normalize(team)}, else: {:error, :invalid_live}
  end

  defp live_ids(tid, cid) do
    if Ids.uuid?(tid) and Ids.uuid?(cid), do: :ok, else: {:error, :invalid_live}
  end

  @doc "The channel topics an envelope from workspace `wid` fans out to."
  @spec topics(String.t(), map()) :: {:ok, [String.t()]} | {:error, atom()}
  def topics(wid, envelope) do
    with {:ok, routes} <- routes(wid, envelope), do: {:ok, Enum.map(routes, &elem(&1, 0))}
  end

  @doc """
  The `{topic, payload}` pairs an envelope from workspace `wid` fans out to:
  the full envelope minus `topics` everywhere, except `workspace:<wid>`, which
  gets the `event.hidden` stand-in when the envelope names a `team:` topic or
  is scoped to people (`private`).
  """
  @spec routes(String.t(), map()) :: {:ok, [{String.t(), map()}]} | {:error, atom()}
  def routes(wid, %{"workspace_id" => envelope_wid, "visibility" => visibility} = envelope)
      when is_binary(envelope_wid) and visibility in ["agents", "public"] do
    listed = Map.get(envelope, "topics", [])

    cond do
      Ids.normalize(envelope_wid) != wid ->
        {:error, :workspace_mismatch}

      not is_list(listed) ->
        {:error, :invalid_envelope}

      true ->
        payload = Map.delete(envelope, "topics")

        workspace =
          if team_scoped?(listed) or private?(listed), do: hidden(envelope), else: payload

        extra =
          listed
          |> Enum.flat_map(&route(&1, wid, visibility))
          |> Enum.uniq()
          |> Enum.map(fn {topic, fields} -> {topic, Map.merge(payload, fields)} end)

        {:ok, [{"workspace:" <> wid, workspace} | extra]}
    end
  end

  def routes(_wid, _envelope), do: {:error, :invalid_envelope}

  @doc "The stand-in `workspace:<wid>` receives for an event of a private team."
  @spec hidden(map()) :: map()
  def hidden(envelope) do
    envelope
    |> Map.take(["id", "seq", "workspace_id", "occurred_at"])
    |> Map.merge(%{"type" => "event.hidden", "visibility" => "agents", "data" => %{}})
  end

  defp team_scoped?(listed), do: Enum.any?(listed, &match?("team:" <> _, &1))

  defp private?(listed), do: Enum.member?(listed, "private")

  defp route("team:" <> rest, wid, _visibility) do
    case String.split(rest, ":") do
      [^wid, tid] -> if Ids.uuid?(tid), do: [{"team:#{wid}:#{Ids.normalize(tid)}", %{}}], else: []
      _ -> []
    end
  end

  defp route("user:" <> uid, _wid, _visibility) do
    if Ids.uuid?(uid), do: [{"user:" <> Ids.normalize(uid), %{}}], else: []
  end

  defp route("contact:" <> rest, wid, "public") do
    case String.split(rest, ":") do
      [^wid, cid] ->
        if Ids.uuid?(cid), do: [{"contact:#{wid}:#{Ids.normalize(cid)}", %{}}], else: []

      [^wid, cid, bid] ->
        if Ids.uuid?(cid) and Ids.uuid?(bid),
          do: [{"contact:#{wid}:#{Ids.normalize(cid)}", %{"brand_id" => Ids.normalize(bid)}}],
          else: []

      _ ->
        []
    end
  end

  defp route(_topic, _wid, _visibility), do: []

  defp decode(body) do
    case Jason.decode(body) do
      {:ok, map} when is_map(map) -> {:ok, map}
      _ -> {:error, :invalid_json}
    end
  end

  # The message body is never logged: it carries customer content.
  defp reject(reason) do
    Metrics.incr({:nats_rejected, reason})
    Logger.warning("dropped NATS message", reason: reason)
    {:error, reason}
  end
end
