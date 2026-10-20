---
title: Workspace and members
description: The workspace, its members, invitations, teams, audit logs and notifications.
---

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/workspaces` | Create a workspace; you become its owner |
| `GET`, `PATCH` | `/v1/workspace` | The current workspace (`OD-Workspace`) |
| `DELETE` | `/v1/workspace` | Owner with two-factor: delete it and all its data |
| `POST` | `/v1/workspace/transfer` | Owner with two-factor: transfer ownership |
| `POST` | `/v1/workspace/widget-secret/rotate` | Admin: new widget secret, shown once |
| `GET` | `/v1/members` | List members |
| `PATCH`, `DELETE` | `/v1/members/{user_id}` | Change a role, or remove a member |
| `PATCH` | `/v1/members/{user_id}/profile` | Change a profile: title, phone, pronouns and personal signature ([details](/api/resources/templates/#member-profiles)) |
| `GET`, `POST` | `/v1/invitations` | List or send invitations |
| `DELETE` | `/v1/invitations/{id}` | Revoke an invitation |
| `POST` | `/v1/invitations/lookup` | Public: what an invitation is for, from `{token}` |
| `POST` | `/v1/invitations/accept` | Accept an invitation (no workspace header) |
| `GET`, `POST` | `/v1/teams` | List or create teams |
| `GET`, `PATCH`, `DELETE` | `/v1/teams/{id}` | |
| `GET` | `/v1/audit-logs` | Admin: the audit log |
| `GET` | `/v1/notifications` | Your notifications |
| `POST` | `/v1/notifications/read` | Mark notifications read |

Pictures, availability, assignment, triage, business hours and the setup checklist are in [Pictures, assignment and setup](/api/resources/pictures-assignment/).

## Objects

```ts
type Workspace = {
  id: string; name: string; slug: string; widget_key: string; settings: WorkspaceSettings;
  avatar_url: string | null; avatar_source: "upload" | null; // the workspace logo
  images: { help_center_logo: string | null; help_center_favicon: string | null; widget_avatar: string | null; widget_launcher: string | null };
  created_at: string;
};
type WorkspaceSettings = {
  brand_color?: string;
  timezone?: string;
  help_center_enabled?: boolean;
  help_center_title?: string;
  csat_enabled?: boolean;
  auto_assign?: "none" | "round_robin" | "load_balanced";
  gravatar?: boolean;     // default false
  domain_logos?: boolean; // default true
};
type Member = {
  user: UserRef & { avatar_source: AvatarSource | null }; role: "owner" | "admin" | "agent" | "viewer";
  availability: "online" | "away" | "offline"; presence: "online" | "away" | "offline";
  max_open_tickets: number | null; open_tickets: number;
  profile: { title: string; phone: string; pronouns: string; signature_html: string }; // see Templates and email design
  last_seen_at: string | null; created_at: string;
};
type Invitation = { id: string; email: string; role: Role; expires_at: string; created_at: string; invited_by: UserRef | null };
type Team = { id: string; name: string; member_ids: string[]; created_at: string };
type AuditLog = {
  id: string;
  actor: { type: "user" | "api_key" | "system"; user: UserRef | null; api_key_id: string | null };
  action: string;
  entity_type: string;
  entity_id: string | null;
  data: Record<string, unknown>;
  ip: string;
  created_at: string;
};
```

## Update settings

```sh
curl -X PATCH https://api.opendesk.sh/v1/workspace \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "settings": { "csat_enabled": true, "auto_assign": "round_robin" } }'
```

## Invite a member

```sh
curl https://api.opendesk.sh/v1/invitations \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "email": "sam@example.com", "role": "agent" }'
```

Invitations can grant `admin`, `agent` or `viewer`.

## Delete the workspace

```sh
curl -X DELETE https://api.opendesk.sh/v1/workspace \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "confirm_name": "Example" }'
```

Requires the owner, with two-factor authentication and a recent sign-in. All workspace data is removed.

## Notifications

```ts
type Notification = {
  id: string;
  type: "assigned" | "new_ticket" | "mentioned" | "customer_replied" | "thread_replied" | "sla_breached" | "ai_handoff" | "ai_cap_reached"
    | "ai_balance_low" | "ai_balance_out" | "auto_recharge_failed" | "ai_key_failed";
  ticket: { id: string; number: number; subject: string } | null;
  data: Record<string, unknown>;
  read_at: string | null;
  created_at: string;
};
```

`new_ticket` (`{ channel, contact?: { id, name } }`) tells the team about a ticket nobody is assigned to; who gets it follows `new_ticket_notify` in [assignment](/api/resources/pictures-assignment/).

Owners and admins get the workspace notices, which have no ticket: `ai_cap_reached` (`{ cap_usd }`, the AI agent reached its monthly spending cap), `ai_balance_low` and `ai_balance_out` (`{ remaining_usd }`, in US dollars), `auto_recharge_failed` (`{ reason }`) and `ai_key_failed` (`{ provider, reason }`). See [Billing](/api/resources/billing/#auto-recharge).

Mark some or all read with `POST /v1/notifications/read` and `{ "ids": [...] }` or `{ "all": true }`. New notifications arrive live as `notification.created` on your `user:<uid>` topic.

## Events

`member.added`, `member.updated`, `member.removed`, `team.*`, `notification.created`.
