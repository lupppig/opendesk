# Spaces

A **Space** is a sidebar a person builds and names: sections of rows they choose, from any of the
workspaces they belong to. Someone keeps several Spaces ("Morning", "Support", "Mail") and picks one
in the workspace switcher, next to their workspaces. Schema: `migrations/000673_spaces.up.sql`. Owners: the API
is backend-core (`internal/core/spaces.go`), the sidebar is web.

Spaces belong to a user, not to a workspace: one list per user, the same in every workspace and on
every device. They are on every plan. Rows of Collab mail show only for workspaces with Collab
(`contracts/collab.md`); without it the editor does not offer them and the sidebar skips them.

Each workspace keeps its member preferences as before (`SidebarPrefs`, `contracts/collab.md`):
what is hidden inside its built-in sections, its favorites, its numbers, the mail page's tabs. A
Space decides which sections and rows show and in what order; a whole built-in section in a Space
still follows that workspace's preferences.

## Shapes

```ts
interface Space {
  id: string                 // uuid, made by the client
  name: string               // 1..40 characters, a name people choose (internal/displayname, Workspace)
  icon: string               // "" (the name's first letter) or one of SPACE_ICONS below
  color: SpaceColor          // default "grey"
  marks: boolean             // default true: another workspace's badge beside its rows
  sections: SpaceSection[]   // at most 20
}

type SpaceColor = "grey" | "blue" | "violet" | "teal" | "green" | "amber" | "red" | "pink"

interface SpaceSection {
  id: string                 // made by the client, 1..40 of [A-Za-z0-9_-], once per Space
  title: string              // 0..40 characters, checked like a name (a starter's titles show to the whole workspace); "" shows no header
  collapsed: boolean
  items: SpaceItem[]         // at most 60; at most 300 across the Space
}

interface SpaceItem {
  workspace_id: string       // a workspace the user is a member of; "" for an everywhere row
  kind: SpaceItemKind
  id: string                 // see below, 1..100 characters
  name?: string              // the person's own label for the row, 1..64 characters, a name people choose
  badge?: SidebarBadge       // the number beside the row (contracts/collab.md "badges"); left out: the row's default
}

type SpaceItemKind =
  | "section"        // a whole built-in section, kept up to date: id "favorites" | "inbox" | "mail" | "teams" | "views" | "workspace"
  | "ticket_folder"  // an inbox folder: id "unassigned" | "mine" | "open" | "pending" | "solved" | "spam" (web/src/lib/folders.ts)
  | "team"           // id: team uuid
  | "view"           // id: view uuid
  | "box"            // a Collab box across accounts: id "inbox" (All inboxes) | "shared" | "assigned"
  | "account"        // a Collab account's inbox: id: account uuid
  | "folder"         // a Collab folder or label: id: folder uuid
  | "page"           // a workspace page: id "contacts" | "knowledge" | "reports"
  | "everywhere"     // an Everywhere list across every workspace: id "mine" (assigned to me) | "mail" (all mail);
                     // workspace_id "" (the one kind of no workspace); never in a starter Space

// SPACE_ICONS: "sun", "moon", "star", "inbox", "mail", "headphones", "users", "briefcase", "rocket",
// "flame", "zap", "heart", "coffee", "flag", "bookmark", "layers", "target", "globe", "bell", "leaf",
// "home", "chart", "shield", "sparkles" (lucide names; the web app maps them to its icons)
```

An item names a thing by id; one that is gone or no longer visible to the person (a deleted team, a
private team they left, an account they no longer see) is kept and skipped by the app, like unknown
ids in `SidebarPrefs`.

### The starter Space

A workspace's admins can publish one **starter Space**: a `Space` whose items are all of that
workspace. Everyone in the workspace finds it in the switcher's New Space menu and starts a Space
from it. A copy is the person's own: later changes
to the starter do not change it.

## Endpoints

| method | path | who | |
| --- | --- | --- | --- |
| GET | `/v1/me/spaces` | user (no `OD-Workspace` needed) | `{spaces: Space[], saved: boolean}`: the saved Spaces, or none (`saved: false`). Items of workspaces the user is no longer a member of are left out (a section left empty stays) |
| PUT | `/v1/me/spaces` | user | `{spaces: Space[]}` (at most 20, at most 64 KB) → the same as GET. Replaces the whole list. Every `workspace_id` must be a workspace the user is a member of and each limit above holds, else `422 validation_failed` (`422 invalid_name` for a refused name); space ids once each. |
| GET | `/v1/sidebar/starter` | member (signed in; not API keys) | `{space: Space \| null}`: the workspace's starter Space |
| PUT | `/v1/sidebar/starter` | admin | `Space` → `{space: Space}`. Every item's `workspace_id` is set to the workspace (never read from the body); the limits above hold. Audit-logged `sidebar.starter_updated` |
| DELETE | `/v1/sidebar/starter` | admin | → 204, also when there is none. Audit-logged `sidebar.starter_removed` when one is removed |

## Web app

- **The switcher.** The workspace switcher in the top bar lists the person's Spaces above their
  workspaces. It picks one of them: a workspace, whose sidebar is its own (its sections in its
  `SidebarPrefs` order, as before Spaces), or a Space, whose sidebar is that Space's rows from every
  workspace in it. The trigger shows what is picked, its icon and name. Alt+1..9 picks a Space, and the
  existing workspace keys (Mod+Alt+1..9) pick a workspace. What is picked is remembered per user in this
  browser (`od-space:<user id>`: the Space id, absent for a workspace).
- **Spaces in the switcher.** Each Space has its own menu (Edit, Duplicate, Move up, Move down, Delete
  with a confirm). New Space opens the editor blank, or from a workspace's own sidebar ("Copy of
  Margin's sidebar"), or from a workspace's starter Space ("From workspace admins").
- **Picking a Space never switches the workspace**, and picking a workspace leaves the Space. With a
  Space picked, the open page still belongs to one workspace, shown in the breadcrumb; opening a row of
  another workspace opens it in that workspace (`?ws=<wid>`, `web/src/lib/workspaceParam.ts`) and the
  Space stays picked.
- **Rows.** A row of the open workspace is the row the sidebar has always had (live counts, menus). A row
  of another workspace reads its teams, views and accounts with `OD-Workspace` set to it (under that
  workspace's own cache keys) and its numbers from `GET /v1/me/overview`, kept live by the one socket of
  an `all_workspaces` token (contracts/api.md "Several workspaces"). The open workspace's rows look exactly
  like its own sidebar; with `marks` on, a row of another workspace carries that workspace's small badge,
  "In <workspace>" on hover (a whole built-in section carries it beside its title; the rail shows none). A
  row's default number is its workspace's own choice for it (`SidebarPrefs.badges`, the row's then its
  section's), then the row's usual one. Rows of another workspace have no row menu and no waiting-chat
  chip: their menus would change that workspace's preferences. Empty sections do not show; a Space with
  nothing to show has the shared empty state with Edit Space. An `everywhere` row opens its Everywhere
  list (`/everywhere/mine`, `/everywhere/mail`).
- **Row menu** inside a Space: Number, Rename in this Space, Move to section, Also add to Space, Remove
  from the Space. Rows inside a whole built-in section keep that section's own menu.
- **The editor** (New Space, Edit) has the name, icon and colour; the Space's sections and rows, dragged
  into order, each row with its name and number; Add section; and "Add from" with a tab per workspace
  listing what that workspace offers: its whole sections, inbox folders, teams, views, Collab boxes,
  accounts and folders, and pages, plus an "Every workspace" tab with the Everywhere lists (not offered
  for a starter Space). Then the `marks` switch. Admins edit the starter Space with the same
  editor from Settings › General.
- Spaces are the active account's. Workspaces of another account signed in to in the same browser are not
  offered; switching accounts switches Spaces.
