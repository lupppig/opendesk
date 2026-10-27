// Setup snippets for AI clients connecting to opendesk's MCP server
// (contracts/agent.md "opendesk MCP server"; docs/api/mcp). Each client either
// signs in with OAuth (only the URL is needed) or sends an API key in a header.

export type ClientId = "claude" | "chatgpt" | "claudeCode" | "claudeDesktop" | "cursor" | "vscode" | "other";

export type ClientSetup = {
  id: ClientId;
  label: string;
  /** How it authenticates: OAuth sign-in or an API key header. */
  auth: "oauth" | "key";
  /** Copyable snippets, in order. */
  snippets: { label?: string; value: string; multiline?: boolean }[];
  /** An optional second way, with an API key, shown under a note. */
  keySnippets?: { label?: string; value: string; multiline?: boolean }[];
};

export const KEY_PLACEHOLDER = "od_live_...";

const json = (v: unknown) => JSON.stringify(v, null, 2);

/** The clients, in the order the page lists them. `key` fills the snippets when the user just made one. */
export function clientSetups(url: string, key?: string | null): ClientSetup[] {
  const k = key || KEY_PLACEHOLDER;
  return [
    { id: "claude", label: "Claude", auth: "oauth", snippets: [{ value: url }] },
    { id: "chatgpt", label: "ChatGPT", auth: "oauth", snippets: [{ value: url }] },
    {
      id: "claudeCode",
      label: "Claude Code",
      auth: "oauth",
      snippets: [{ value: `claude mcp add --transport http opendesk ${url}` }],
      keySnippets: [{ value: `claude mcp add --transport http opendesk ${url} \\\n  --header "Authorization: Bearer ${k}"`, multiline: true }],
    },
    {
      id: "claudeDesktop",
      label: "Claude Desktop",
      auth: "key",
      snippets: [
        {
          multiline: true,
          value: json({
            mcpServers: {
              opendesk: {
                command: "npx",
                args: ["-y", "mcp-remote", url, "--header", "Authorization:${OPENDESK_AUTH}"],
                env: { OPENDESK_AUTH: `Bearer ${k}` },
              },
            },
          }),
        },
      ],
    },
    {
      id: "cursor",
      label: "Cursor",
      auth: "key",
      snippets: [{ multiline: true, value: json({ mcpServers: { opendesk: { url, headers: { Authorization: `Bearer ${k}` } } } }) }],
    },
    {
      id: "vscode",
      label: "VS Code",
      auth: "key",
      snippets: [
        {
          multiline: true,
          value: json({
            inputs: [{ type: "promptString", id: "opendesk-key", description: "opendesk API key", password: true }],
            servers: { opendesk: { type: "http", url, headers: { Authorization: "Bearer ${input:opendesk-key}" } } },
          }),
        },
      ],
    },
    { id: "other", label: "Other", auth: "oauth", snippets: [{ value: url }, { value: `Authorization: Bearer ${k}` }] },
  ];
}

/** Read scopes for an assistant that only reads; write adds what replying and drafting need. */
export const READ_SCOPES = ["tickets:read", "contacts:read", "kb:read", "wiki:read"];
export const WRITE_SCOPES = [...READ_SCOPES, "tickets:write", "contacts:write", "kb:write", "wiki:write"];
