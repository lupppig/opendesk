defmodule RealtimeWeb.ContactChannel do
  @moduledoc """
  `contact:<wid>:<contact_id>`: the chat widget's channel. Joined only by a
  `widget_ws` token for that contact in that workspace. Receives public events,
  agents' `"typing"` `{ticket_id, user}` and the widget agent's live reply
  previews (`"live"`), all from `Realtime.Fanout`.

  The visitor's typing goes through the API (`POST /v1/widget/conversations/{id}/typing`),
  which checks the conversation is theirs. A `"typing"` push here is accepted and
  does nothing, for widgets from before that route.

  `"event"`, `"live"` and `"typing"` pushes that name a chat's brand (`brand_id`)
  reach only sockets whose token is for that brand (its `bid`); `brand_id` is
  stripped from what is pushed. A visitor's chats of another brand never
  reach this widget.
  """

  use Phoenix.Channel, log_join: false, log_handle_in: false

  intercept ["event", "live", "typing"]

  alias RealtimeWeb.Channels.Guard

  @impl true
  def join(
        "contact:" <> rest,
        _payload,
        %{assigns: %{kind: :contact, wid: wid, sub: sub}} = socket
      ) do
    case String.split(rest, ":") do
      [^wid, ^sub] ->
        if Guard.join_allowed?(socket),
          do: {:ok, Guard.ok_reply(), socket},
          else: {:error, %{reason: "rate_limited"}}

      _ ->
        Guard.forbidden(:contact)
    end
  end

  def join(_topic, _payload, _socket), do: Guard.forbidden(:contact)

  @impl true
  def handle_out(event, %{"brand_id" => brand} = payload, socket) do
    case Map.get(socket.assigns, :brand) do
      mine when mine in [nil, brand] ->
        push(socket, event, Map.delete(payload, "brand_id"))
        {:noreply, socket}

      _other ->
        {:noreply, socket}
    end
  end

  def handle_out(event, payload, socket) do
    push(socket, event, payload)
    {:noreply, socket}
  end

  @impl true
  def handle_in(event, payload, socket) do
    case Guard.throttle_push(socket) do
      {:ok, socket} -> handle_push(event, payload, socket)
      {:drop, socket} -> {:noreply, socket}
      reply -> reply
    end
  end

  defp handle_push("typing", _payload, socket), do: {:noreply, socket}

  defp handle_push(_event, _payload, socket), do: Guard.invalid(socket)
end
