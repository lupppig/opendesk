defmodule RealtimeWeb.TicketChannel do
  @moduledoc """
  `ticket:<wid>:<ticket_id>`: Presence of agents on a ticket, typing, and the
  widget agent's live reply previews (`"live"`, from `Realtime.Fanout`).
  Joined by `ws` tokens with `kind=user` in the same workspace.

  Join params:

    * `{watch: true}` watches the ticket (the inbox list): the joiner receives
      Presence but is not tracked, and its presence and typing pushes are ignored.
    * `{state: "viewing" | "replying" | "idle"}` is the initial Presence state
      (default `"viewing"`).
    * `{team_id}` is the ticket's private team. It must be in the token's
      `teams` claim (else the join is forbidden), and it scopes the joiner's
      Presence meta and typing to that team.

  A private team's ticket reaches only its team: Presence metas and typing of
  joiners that named the team, and live previews the API marks with the team,
  carry `team_id`, and `handle_out` drops them for sockets whose `teams` claim
  lacks it (and strips `team_id` from what it pushes). A socket that omits
  `team_id` therefore joins, but receives nothing scoped to a private team.

  Client pushes:

    * `"presence"` `{state: "viewing" | "replying" | "idle"}` updates the meta.
    * `"typing"` `{contact_id?}` sends `"typing"` `{user_id, name}` to the other
      agents here and, with a `contact_id`, `"typing"`
      `{ticket_id, user: {id, name}}` to `contact:<wid>:<contact_id>` (not from a
      viewer, who cannot reply).
  """

  use Phoenix.Channel, log_join: false, log_handle_in: false

  intercept ["live", "typing", "presence_diff"]

  alias Realtime.Ids
  alias RealtimeWeb.Channels.Guard
  alias RealtimeWeb.Endpoint
  alias RealtimeWeb.Presence

  @states ~w(viewing replying idle)

  @impl true
  def join("ticket:" <> rest, payload, %{assigns: %{kind: :user, wid: wid}} = socket) do
    with [^wid, ticket_id] <- String.split(rest, ":"),
         true <- Ids.uuid?(ticket_id) and ticket_id == Ids.normalize(ticket_id),
         {:ok, team_id} <- join_team(payload, socket) do
      if Guard.join_allowed?(socket) do
        send(self(), :after_join)

        socket =
          socket
          |> assign(:ticket_id, ticket_id)
          |> assign(:team_id, team_id)
          |> assign(:watch, watch?(payload))
          |> assign(:initial_state, initial_state(payload))

        {:ok, Guard.ok_reply(), socket}
      else
        {:error, %{reason: "rate_limited"}}
      end
    else
      _ -> Guard.forbidden(:ticket)
    end
  end

  def join(_topic, _payload, _socket), do: Guard.forbidden(:ticket)

  @impl true
  def handle_info(:after_join, socket) do
    unless socket.assigns.watch do
      {:ok, _ref} =
        Presence.track(socket, socket.assigns.sub, meta(socket, socket.assigns.initial_state))
    end

    push(socket, "presence_state", visible_presences(Presence.list(socket), socket))
    {:noreply, socket}
  end

  @impl true
  def handle_out("presence_diff", %{joins: joins, leaves: leaves}, socket) do
    joins = visible_presences(joins, socket)
    leaves = visible_presences(leaves, socket)

    if joins != %{} or leaves != %{},
      do: push(socket, "presence_diff", %{joins: joins, leaves: leaves})

    {:noreply, socket}
  end

  def handle_out(event, payload, socket) when event in ["live", "typing"] do
    {team_id, payload} = pop_team(payload)
    if visible?(team_id, socket), do: push(socket, event, payload)
    {:noreply, socket}
  end

  # No team_id: the ticket is not in a private team. A team_id must be one of the
  # token's private teams.
  defp join_team(payload, socket) when is_map(payload) do
    case Map.get(payload, "team_id") do
      nil ->
        {:ok, nil}

      team_id ->
        if Ids.uuid?(team_id) and Ids.normalize(team_id) in Map.get(socket.assigns, :teams, []),
          do: {:ok, Ids.normalize(team_id)},
          else: :error
    end
  end

  defp join_team(_payload, _socket), do: {:ok, nil}

  defp visible?(nil, _socket), do: true
  defp visible?(team_id, socket), do: team_id in Map.get(socket.assigns, :teams, [])

  defp pop_team(payload) do
    case Map.pop(payload, :team_id) do
      {nil, payload} -> Map.pop(payload, "team_id")
      found -> found
    end
  end

  # Keeps the metas the socket may see, without their team_id.
  defp visible_presences(presences, socket) do
    for {key, %{metas: metas} = entry} <- presences,
        metas =
          for(meta <- metas, visible?(meta[:team_id], socket), do: Map.delete(meta, :team_id)),
        metas != [],
        into: %{},
        do: {key, %{entry | metas: metas}}
  end

  defp watch?(%{"watch" => true}), do: true
  defp watch?(_), do: false

  defp initial_state(%{"state" => state}) when state in @states, do: state
  defp initial_state(_), do: "viewing"

  @impl true
  def handle_in(event, payload, socket) do
    case Guard.throttle_push(socket) do
      {:ok, socket} -> handle_push(event, payload, socket)
      {:drop, socket} -> {:noreply, socket}
      reply -> reply
    end
  end

  # A watcher is not in Presence and does not type here.
  defp handle_push(event, _payload, %{assigns: %{watch: true}} = socket)
       when event in ["presence", "typing"] do
    {:noreply, socket}
  end

  defp handle_push("presence", %{"state" => state}, socket) when state in @states do
    {:ok, _ref} =
      Presence.update(socket, socket.assigns.sub, fn meta -> Map.put(meta, :state, state) end)

    {:reply, :ok, socket}
  end

  defp handle_push("typing", payload, socket) when is_map(payload) do
    case Map.get(payload, "contact_id") do
      nil ->
        broadcast_from!(socket, "typing", typing(socket))
        {:noreply, socket}

      contact_id ->
        if Ids.uuid?(contact_id) do
          broadcast_from!(socket, "typing", typing(socket))
          typing_to_contact(socket, contact_id)
          {:noreply, socket}
        else
          Guard.invalid(socket)
        end
    end
  end

  defp handle_push(_event, _payload, socket), do: Guard.invalid(socket)

  # Only someone who can reply shows the customer they are typing: never a viewer.
  defp typing_to_contact(%{assigns: %{role: "viewer"}}, _contact_id), do: :ok

  defp typing_to_contact(socket, contact_id) do
    %{sub: sub, name: name, wid: wid, ticket_id: ticket_id} = socket.assigns

    Endpoint.broadcast!("contact:#{wid}:#{Ids.normalize(contact_id)}", "typing", %{
      ticket_id: ticket_id,
      user: %{id: sub, name: name}
    })
  end

  defp typing(socket),
    do: %{user_id: socket.assigns.sub, name: socket.assigns.name, team_id: socket.assigns.team_id}

  defp meta(socket, state) do
    %{
      user_id: socket.assigns.sub,
      name: socket.assigns.name,
      avatar: socket.assigns.avatar,
      state: state,
      online_at: DateTime.utc_now() |> DateTime.to_iso8601(),
      team_id: socket.assigns.team_id
    }
  end
end
