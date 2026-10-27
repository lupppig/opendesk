---
title: MCP server
description: Connect Claude, ChatGPT, Cursor, VS Code or any MCP client to opendesk with a sign-in or an API key, and the tools it offers.
---

opendesk is an [MCP](https://modelcontextprotocol.io) server. Connect an AI client such as Claude, ChatGPT, Cursor or VS Code and it can search and read tickets, reply, add notes, update and create tickets, look up contacts, search the knowledge base, draft articles and read reports.

There are two ways to connect:

- **Sign in with OAuth.** Clients with connectors (the Claude apps, ChatGPT) only need the server URL. You sign in to opendesk, choose a workspace and approve. The client acts as you, with your role.
- **An API key.** Clients that send a header (Claude Code, Cursor, VS Code, scripts) use a key with the scopes you choose.

**Settings → AI clients** in opendesk shows the server URL, copyable setup for each client, the tools offered and the clients you have connected.

| Where | Endpoint |
| --- | --- |
| opendesk Cloud | `https://api.opendesk.sh/mcp` |
| Self-hosted | `https://<your-domain>/mcp` |

The server speaks MCP over Streamable HTTP. Each request stands alone, so there is no session to keep alive.

## Connect with OAuth

Add opendesk as a custom connector with the URL `https://api.opendesk.sh/mcp` (self-hosted: `https://<your-domain>/mcp`). No key, no bridge.

- **Claude** (claude.ai and the desktop app): **Settings → Connectors → Add custom connector**, paste the URL, then **Connect**.
- **ChatGPT**: add a connector (developer mode) with the same URL and OAuth authentication.

The client opens opendesk in your browser. Sign in if you are not, pick the workspace, check what the app asks for and approve. You return to the client, connected.

What it can do:

- The app gets the access you approve, never more than your role allows: viewers read only; agents and up can also reply, update tickets and contacts, and draft articles. If your role changes, the connection follows at once.
- It never gets settings writes, and it can't reach your account (password, sessions, two-factor).
- Everything it does shows up in opendesk as done by you.

Disconnect it any time in **Settings → AI clients**. Admins see every member's connections and can disconnect any of them. Removing a member from the workspace, or changing or resetting your password, disconnects their clients too.

For client developers: opendesk follows the MCP authorization spec. `401` responses from `/mcp` carry `WWW-Authenticate: Bearer resource_metadata="https://api.opendesk.sh/.well-known/oauth-protected-resource/mcp"`. The authorization server metadata is at `/.well-known/oauth-authorization-server` and supports dynamic client registration (`/oauth/register`; redirect URIs must be `https`, or `http` on `127.0.0.1`, `localhost` or `[::1]`), the authorization code flow with PKCE (`S256` only), the `resource` parameter, refresh token rotation and revocation (`/oauth/revoke`). Access tokens last an hour; refresh tokens rotate on every use, and a refresh token used twice ends the connection.

## Create an API key

1. In opendesk, open **Settings → API keys** (admins only).
2. Create a key and give it only the scopes you want the AI client to have. The client sees only the tools those scopes allow (see [Tools](#tools)).
3. Copy the secret (`od_live_...`). It is shown once.

Every tool runs through the public API as that key, so it gets exactly the key's scopes, the same validation, and the same events, activity entries and audit log entries as any other API call. Changes show up in opendesk as made by the API.

Treat the key like a password. Do not commit it to a repository; prefer a prompt or an environment variable, as in the examples below. Delete the key in **Settings → API keys** to cut off access at once.

## Connect a client

The examples use the Cloud endpoint. On a self-hosted install, replace `https://api.opendesk.sh/mcp` with `https://<your-domain>/mcp`.

### Claude Code

```sh
claude mcp add --transport http opendesk https://api.opendesk.sh/mcp \
  --header "Authorization: Bearer od_live_..."
```

Add `--scope project` to share the server with your team through `.mcp.json`. In that case keep the key out of the file and reference an environment variable instead:

```json
{
  "mcpServers": {
    "opendesk": {
      "type": "http",
      "url": "https://api.opendesk.sh/mcp",
      "headers": { "Authorization": "Bearer ${OPENDESK_API_KEY}" }
    }
  }
}
```

### Claude Desktop

The simplest way is a connector with OAuth (see [Connect with OAuth](#connect-with-oauth)). To use an API key instead, connect through the `mcp-remote` bridge (it needs Node.js). Open **Settings → Developer → Edit config** and add:

```json
{
  "mcpServers": {
    "opendesk": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://api.opendesk.sh/mcp", "--header", "Authorization:${OPENDESK_AUTH}"],
      "env": { "OPENDESK_AUTH": "Bearer od_live_..." }
    }
  }
}
```

Restart Claude Desktop. The opendesk tools appear in the tools menu.

### Cursor

Add the server to `~/.cursor/mcp.json` (all projects) or `.cursor/mcp.json` (one project):

```json
{
  "mcpServers": {
    "opendesk": {
      "url": "https://api.opendesk.sh/mcp",
      "headers": { "Authorization": "Bearer od_live_..." }
    }
  }
}
```

Then check **Cursor Settings → MCP** that opendesk is enabled and lists its tools.

### VS Code

Add `.vscode/mcp.json` to your workspace. VS Code asks for the key the first time and stores it securely:

```json
{
  "inputs": [
    { "type": "promptString", "id": "opendesk-key", "description": "opendesk API key", "password": true }
  ],
  "servers": {
    "opendesk": {
      "type": "http",
      "url": "https://api.opendesk.sh/mcp",
      "headers": { "Authorization": "Bearer ${input:opendesk-key}" }
    }
  }
}
```

Start the server from the file or with **MCP: List Servers**, then use the tools in Copilot chat's agent mode.

### Other clients

Any MCP client that supports Streamable HTTP works: with OAuth when it supports the MCP authorization spec, otherwise with `Authorization: Bearer <API key>` on every request.

## Tools

A tool is offered only when the key, or the OAuth connection together with your role, has its scopes.

| Tool | Scope | What it does | Changes data |
| --- | --- | --- | --- |
| `search_tickets` | `tickets:read` | Finds tickets by text, status, priority, severity, assignee, team, label, channel, contact or creation date. At most 50 per call. | No |
| `get_ticket` | `tickets:read` | Reads a ticket with its thread as plain text, internal notes included. | No |
| `reply_to_ticket` | `tickets:write` | Sends a public reply (markdown). The customer receives it. Can set the status after sending. | Yes |
| `draft_reply` | `tickets:write` | Stages a reply (markdown) on the ticket without sending it. Every teammate who opens the ticket finds it in their composer, to edit and send as their own reply. Staging again replaces it; a reply going out clears it. | Yes |
| `add_note` | `tickets:write` | Adds an internal note the customer never sees. | Yes |
| `update_ticket` | `tickets:write` | Changes status, priority, severity, assignee, team or labels. | Yes |
| `create_ticket` | `tickets:write` | Creates a ticket for a contact email. With a mailbox, the first reply is emailed to the contact. | Yes |
| `search_contacts` | `contacts:read` | Finds contacts by name, email, phone or field value. | No |
| `get_contact` | `contacts:read` | Reads a contact by id or email, with their latest tickets when the key also has `tickets:read`. | No |
| `search_knowledge` | `kb:read` | Searches knowledge base articles and knowledge sources; the top matches come with their full text. | No |
| `create_kb_draft` | `kb:write` | Creates an unpublished draft article for the team to review. | Yes |
| `search_wiki` | `wiki:read` | Searches the team's internal wiki; each match comes with where it sits and a passage. | No |
| `get_wiki_page` | `wiki:read` | Reads a wiki page as plain text, with its subpages and its version. | No |
| `create_wiki_page` | `wiki:write` | Adds a wiki page (markdown), at the top level or under another page. | Yes |
| `update_wiki_page` | `wiki:write` | Renames a wiki page or replaces its text (markdown). With the version it read, a teammate's newer edit is never overwritten. | Yes |
| `get_report` | `settings:read` and `tickets:read` | Reads the overview report for a date range. | No |

Tickets are referenced by number (`#123`) or id. Assigning by email or moving to a team by name looks the teammate or team up, which needs `settings:read`; with user and team ids it does not.

Tools carry MCP annotations, so clients can tell read-only tools from ones that change data and ask you before running them. Results are compact JSON text.

A suggested starting point: `tickets:read`, `contacts:read` and `kb:read` for an assistant that only reads, plus `tickets:write` when you want it to reply and update tickets.

## Limits and security

- Requests without a valid key or token get `401` with `WWW-Authenticate: Bearer` and the `resource_metadata` URL.
- Requests from a browser must come from an origin in `CORS_ORIGINS`; others get `403`. Desktop and command line clients send no origin and are not affected.
- Each key, and each OAuth connection, can make 300 requests a minute to `/mcp`. Over that, the server answers `429`; wait and retry.
- Request bodies, keys, codes and tokens are never logged. Codes and refresh tokens are stored hashed.

## Self-hosting

There is nothing to switch on. The MCP endpoint is part of the `opendesk` service, and the bundled Caddy configuration already sends `/mcp` to it, so it is live at `https://<your-domain>/mcp` as soon as your install is, OAuth included. If you run your own reverse proxy, route `/mcp`, `/oauth/*` and `/.well-known/oauth-*` to the `opendesk` service, like the API.
