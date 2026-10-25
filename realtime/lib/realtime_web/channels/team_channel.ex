defmodule RealtimeWeb.TeamChannel do
  @moduledoc """
  `team:<wid>:<tid>`: every event about a private team's tickets, both
  visibilities (`contracts/teams.md`, "Visibility"). Those events reach
  `workspace:<wid>` only as an `event.hidden` stand-in, so this topic is the one
  place their content is pushed.

  Joined by `ws` tokens (users and API keys) that cover `wid` and list `tid`
  among their private teams there (the token's `teams` for its own workspace,
  the `workspaces` claim's for a person's others): the API puts there only the
  private teams the caller may see in that workspace, and the claim is fixed
  for the socket's life. A change to someone's teams disconnects their sockets
  so they reconnect with a fresh token.
  """

  use Phoenix.Channel, log_join: false, log_handle_in: false

  alias Realtime.Auth
  alias RealtimeWeb.Channels.Guard

  @impl true
  def join("team:" <> rest, _payload, %{assigns: %{kind: kind} = assigns} = socket)
      when kind in [:user, :api_key] do
    with [wid, tid] <- String.split(rest, ":"),
         %{teams: teams} <- Auth.workspace(assigns, wid),
         true <- tid in teams do
      if Guard.join_allowed?(socket),
        do: {:ok, Guard.ok_reply(), socket},
        else: {:error, %{reason: "rate_limited"}}
    else
      _ -> Guard.forbidden(:team)
    end
  end

  def join(_topic, _payload, _socket), do: Guard.forbidden(:team)

  @impl true
  def handle_in(_event, _payload, socket) do
    case Guard.throttle_push(socket) do
      {:ok, socket} -> Guard.invalid(socket)
      {:drop, socket} -> {:noreply, socket}
      reply -> reply
    end
  end
end
