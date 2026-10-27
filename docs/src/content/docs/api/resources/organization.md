---
title: Views, labels and saved replies
description: The resources that organize the inbox.
---

| Method | Path | Description |
| --- | --- | --- |
| `GET`, `POST` | `/v1/tags` | List or create labels ([details](/api/resources/labels-fields/)) |
| `GET`, `PATCH`, `DELETE` | `/v1/tags/{id}` | |
| `GET`, `POST` | `/v1/views` | List or create views |
| `GET`, `PATCH`, `DELETE` | `/v1/views/{id}` | |
| `GET`, `POST` | `/v1/saved-replies` | List or create saved replies |
| `GET`, `PATCH`, `DELETE` | `/v1/saved-replies/{id}` | |

## Objects

```ts
type View = {
  id: string;
  name: string;
  shared: boolean;
  position: number;
  filters: {
    status?: TicketStatus[];
    priority?: Priority[];
    assignee?: "me" | "none" | string;
    team_id?: string;
    mailbox_id?: string;
    contact_not?: string; // comma-separated contact ids: leaves out their tickets
    company_not?: string; // comma-separated company ids: leaves out their contacts' tickets
    tag?: string;
    channel?: Channel;
    q?: string;
    fields?: { key: string; op: FieldOp; value: string }[]; // custom field filters
  };
};

type SavedReply = { id: string; title: string; shortcut: string; body_html: string; created_at: string; updated_at: string };
```

## Create a view

```sh
curl https://api.opendesk.sh/v1/views \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "name": "Urgent, unassigned", "shared": true, "filters": { "status": ["open"], "priority": ["urgent"], "assignee": "none" } }'
```

List tickets in a view with `GET /v1/tickets?view_id=<id>`.

## Create a saved reply

```sh
curl https://api.opendesk.sh/v1/saved-replies \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "title": "Password reset", "shortcut": "reset", "body_html": "<p>You can reset your password from the sign-in page.</p>" }'
```

`body_html` takes [template variables](/guides/email-templates/variables/). An unknown variable or a tag that does not parse is `422 validation_failed`. To insert a saved reply filled for a ticket, use [`POST /v1/tickets/{id}/render`](/api/resources/templates/#ticket-values).

## Events

`view.*` and `saved_reply.*` (`created`, `updated`, `deleted`). Label events are listed under [Labels and custom fields](/api/resources/labels-fields/#events).

Labels, label groups and custom fields have [their own page](/api/resources/labels-fields/).
