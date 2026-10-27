---
title: Wiki
description: Read and write the team's internal wiki pages, their history and search.
---

The wiki holds your team's internal pages: runbooks, policies, escalation steps, how-tos. It is for members only and never shown to customers. Pages nest under each other.

Scopes: `wiki:read` to read, `wiki:write` to change.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/wiki/pages` | Every page without its body, to build the tree |
| `POST` | `/v1/wiki/pages` | Create a page: `parent_id`, `title`, `icon`, `body_html`, `position` (all optional) |
| `GET`, `PATCH`, `DELETE` | `/v1/wiki/pages/{id}` | Read a page with its body, change its title, icon or body, or delete it with its subpages |
| `POST` | `/v1/wiki/pages/{id}/move` | Put it under another page, or at the top level, at a position |
| `GET` | `/v1/wiki/pages/{id}/revisions` | Its history, newest first |
| `GET` | `/v1/wiki/pages/{id}/revisions/{revisionId}` | One revision with its body |
| `POST` | `/v1/wiki/pages/{id}/revisions/{revisionId}/restore` | Put a revision back as a new version |
| `GET` | `/v1/wiki/search?q=` | Pages matching the words, best first, with a passage around the match |

Viewers read. Agents and up create, edit and move pages. Admins delete any page; agents a page they created, when they also created every page under it (otherwise `403`, and an admin can delete it). API keys with `wiki:write` delete any page. `GET /v1/search` also returns the top wiki pages, in `wiki_pages`.

## Objects

```ts
type WikiNode = {
  id: string;
  parent_id: string | null;
  title: string;          // "" until named; up to 200 characters (longer is 422)
  icon: string;           // one emoji, or ""
  position: number;       // among its siblings, from 0
  version: number;        // goes up on every change of title, icon or body
  updated_at: string;
  created_by_id: string | null;
};

type WikiPage = WikiNode & {
  body_html: string;
  created_by: UserRef | null;
  updated_by: UserRef | null;
  created_at: string;
  breadcrumbs: { id: string; title: string; icon: string }[]; // top-level page first
  children: { id: string; title: string; icon: string }[];
};
```

## Edit without overwriting a teammate

Send the `version` you started from. If someone saved the page since, the API answers `409` with code `page_changed` and changes nothing:

```sh
curl -X PATCH https://api.opendesk.sh/v1/wiki/pages/0192f0c4-... \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{"body_html": "<p>Refunds over 100 euros go to the team lead.</p>", "version": 4}'
```

Leave `version` out to always apply the change.

## Move a page

```sh
curl -X POST https://api.opendesk.sh/v1/wiki/pages/0192f0c4-.../move \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{"parent_id": "0192f0c4-...", "position": 0}'
```

`parent_id: null` moves it to the top level. A page can't move under itself or one of its subpages (`422 page_cycle`).

## Limits

- 5,000 pages per workspace (`422 wiki_full`).
- 10 levels deep (`422 page_too_deep`).
- 512 KB of text and formatting per page (`422 page_too_large`).
- 100 revisions per page. One person's edits a few minutes apart share a revision.

Events: `wiki_page.created`, `wiki_page.updated`, `wiki_page.moved` and `wiki_page.deleted`. They carry the page without its body; fetch the page for its text.
