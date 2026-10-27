# Wiki

The **wiki** is the team's internal documentation: runbooks, policies, escalation steps,
onboarding, how-tos. Pages nest under each other like folders that hold their own text. Schema:
`migrations/000683_wiki.up.sql`. Owners: the API is backend-helpdesk (`internal/helpdesk/wiki`), the
pages are web (`web/src/pages/wiki`).

The wiki is for members only. It is never public and never reaches a customer: no help center,
no widget, no email agent, no knowledge source of the AI agent, no embeddings. The knowledge base
(`/v1/kb/*`) is what customers read. Ollie, the in-app assistant, reads the wiki for the member it
works for; AI clients connected to the MCP server read and write it with the `wiki:*` scopes. The
wiki is on every plan, with the limits below.

## Shapes

```ts
interface WikiNode {           // a page in the tree, without its body
  id: string
  parent_id: string | null     // the page it sits under; null at the top level
  title: string                // 0..200 characters (longer is 422), whitespace folded; "" shows as "Untitled"
  icon: string                 // one emoji (see "Icons"), or ""
  position: number             // order among its siblings: 0, 1, 2... with no gaps
  version: number              // starts at 1, +1 on every change of title, icon or body
  updated_at: string           // the last change of title, icon or body (moves do not count)
  created_by_id: string | null // who created it; null for an API key, or once the person deleted their account
}

interface WikiRef { id: string; title: string; icon: string }

interface WikiPage extends WikiNode {
  body_html: string            // rich text, see "Body"
  created_by: UserRef | null   // null for an API key, or once the person deleted their account
  updated_by: UserRef | null
  created_at: string
  breadcrumbs: WikiRef[]       // its ancestors, the top-level page first
  children: WikiRef[]          // its subpages, in order
}

interface WikiRevision {
  id: string
  title: string
  icon: string
  body_html?: string           // only when one revision is read
  author: UserRef | null
  created_at: string           // the first edit it holds
  updated_at: string           // the last edit it holds
}

interface WikiSearchHit {
  id: string
  parent_id: string | null
  title: string
  icon: string
  snippet: string              // plain text around the match, no markup
  path: WikiRef[]              // its ancestors, the top-level page first
  updated_at: string
}
```

## Access

| who | reads | creates, edits, moves, restores | deletes |
| --- | --- | --- | --- |
| viewer | yes | no | no |
| agent | yes | yes | a page they created, when they also created every page under it |
| admin, owner | yes | yes | any page |
| API key, AI client | `wiki:read` | `wiki:write` | `wiki:write` (an AI client's member: as their role allows) |

An agent deleting a page with someone else's page in it (the page or any subpage, including pages
moved under it) gets `403`, with a message saying an admin can delete it; so moving another team's
pages under one's own never makes them deletable. The tree carries `created_by_id` so the app offers
Delete only where it would pass, and says why it doesn't otherwise. An API key with `wiki:write`
deletes any page: whoever holds the key is trusted with the whole wiki.

Every page belongs to one workspace and is read and written with the caller's workspace. A
`parent_id` from another workspace is `422`; another workspace's page is `404`. The parent of a
page is enforced in the schema too (a composite key on `(workspace_id, parent_id)`). `kb:*`
scopes do not reach the wiki, nor `wiki:*` the knowledge base.

## Tree

- New pages go at the end of their parent's subpages unless `position` says where.
- `POST /v1/wiki/pages/{id}/move` `{parent_id, position}` moves a page with everything under it. A
  page never moves under itself or one of its subpages (`422 page_cycle`). Positions are numbered
  again from 0 among the new siblings and, when the parent changed, the old ones.
- Changes to the tree (create, move, delete) of one workspace run one at a time
  (`pg_advisory_xact_lock`), so counts, depths and cycle checks see each other.
- Deleting a page deletes its subpages and their history, for good (`ON DELETE CASCADE`). The app
  asks first and names how many subpages go with it.
- In the app, pages drag onto, above and below each other; **Move to…** in a page's menu (in the tree
  and on the page) picks the new parent from a searchable list, or the top level, and puts the page
  last among its new siblings. It is what touch screens use, where dragging is not available. Pages
  the move can't go under (the page itself, its subpages, or past the depth limit) are not offered.

## Body

`body_html` is sanitized with the rich-text allowlist the knowledge base uses (`hd.SanitizeRich`:
paragraphs, headings, lists, quotes, code, links, images from https or our attachments, mentions)
before it is stored. The app edits it with the same editor as articles. Images are uploaded as
attachments (`POST /v1/attachments`).

## Editing together

The app saves as people type. Every PATCH from the app sends the `version` it edited; when someone
else saved since, the API answers `409 page_changed` and changes nothing. The app then shows
"Someone else edited this page", keeps what was typed and offers to load their version or to keep
its own (which saves again with the new version). A PATCH without `version` (an API caller that
does not care) always applies. A PATCH that changes nothing keeps the version and publishes no
event.

Only a person who may edit ever saves, and loading a version (someone else's, a restored one,
theirs after a conflict) is never an edit: viewers and idle agents send nothing. A save answered
`403` or `404` stops for good (no retries): the page says it can't be edited or was deleted, and
offers the unsent text to copy. While any page holds unsent text, closing the tab from anywhere in
the app sends it and the browser asks first. A tab refetches a page when an event's `version` is
newer than the one it holds, so the same person's edits from another tab or through Ollie arrive
too.

## History

Every change of title, icon or body is kept as a revision:

- One person's edits less than 10 minutes apart, for at most an hour, are one revision: while they
  type, their latest revision moves along (`updated_at`). Someone else's edit, a pause, an API key's
  edit and a restore start a new one.
- At most 100 revisions per page are kept; the oldest go first.
- Restoring a revision puts its title, icon and body back as a new version and a new revision, so
  what it replaced stays in the history.

Moves are not revisions.

## Search

`GET /v1/wiki/search?q=` matches titles (weighted highest) and bodies, every word also as a
prefix ("refu" finds "refunds"), like the knowledge base's `prefixQuery`. `GET /v1/search` (the
command palette) returns the top 5 pages in `wiki_pages` to callers that may read the wiki.

## Icons

`icon` is one emoji as Unicode spells them (UTS #51): an `Extended_Pictographic` character with an
optional variation selector and skin tone, several joined by ZWJ (families, professions), a flag
(a pair of regional indicators, or a black flag with tag characters), or a keycap. Anything else
(letters, bidi controls, zero-width characters other than the joiner, combining marks, two emoji)
is `422`. Every emoji the app's picker offers passes (`TestPickerEmoji`).

## Events

`wiki_page.created`, `wiki_page.updated`, `wiki_page.moved`: `{ wiki_page: WikiNode, updated_by }`.
`wiki_page.deleted`: `{ wiki_page_id, wiki_page_ids }` (the page and every subpage removed with it).
Bodies are left out, as they are large and saved often: a client showing the page refetches it.
The web app patches the tree in place on `created`, `updated` and `deleted`, and refetches it on
`moved`. Audit: `wiki_page.created` `{title, parent_id}`, `wiki_page.deleted` `{title, subpages}`.

## Limits

| | |
| --- | --- |
| pages per workspace | 5,000 (`422 wiki_full`) |
| depth | 10 levels (`422 page_too_deep`, counting the subpages a move takes along) |
| body | 512 KB (`422 page_too_large`) |
| title | 200 characters (`422`; the app's title field stops at 200) |
| revisions per page | 100 |

## AI

- **Ollie** (`internal/ai/assistant`) has `search_wiki` and `get_wiki_page` read tools and links
  the pages it used (`Ref.kind: "wiki_page"`). The page context carries `wiki_page_id` on a wiki
  page. Writing goes through `api_change` and its approval, like other settings.
- **MCP** (`contracts/agent.md` "opendesk MCP server"): `search_wiki`, `get_wiki_page`
  (`wiki:read`), `create_wiki_page`, `update_wiki_page` (`wiki:write`; markdown bodies).
- Never the widget agent, the email agent, suggested replies or anything a customer sees.
