// From name formats (internal/fromname): a From name written with variables that name who
// sends the mail, like "{agent} at {mailbox}". One vocabulary across the product: a helpdesk
// mailbox takes {agent}, {agent_first}, {team}, {brand} and {mailbox} (contracts/teams.md
// "What the customer sees"); a Collab account or address {agent}, {agent_first}, {mailbox}
// (the account's name) and {workspace} (contracts/collab.md "Addresses"). The server fills
// and checks them again; this is the live preview.

export type FromVar = "agent" | "agent_first" | "team" | "brand" | "mailbox" | "workspace";

export const HELPDESK_FROM_VARS: readonly FromVar[] = ["agent", "agent_first", "team", "brand", "mailbox"];
export const COLLAB_FROM_VARS: readonly FromVar[] = ["agent", "agent_first", "mailbox", "workspace"];

export const FROM_MAX = 80;

export type FromNames = { agent: string; team?: string; brand?: string; mailbox: string; workspace?: string };

const SEP = /^[\s\-–—|·:;,/]+|[\s\-–—|·:;,/]+$/g;

/**
 * The From name for a format. Without a sender (auto-replies, automations, CSAT) the agent
 * variable and the words up to the next variable are dropped, a format left with no
 * variable falls back to {mailbox}, and separators left at either end are trimmed.
 */
export function fillFromName(format: string, v: FromNames, vars: readonly FromVar[] = HELPDESK_FROM_VARS): string {
  const re = new RegExp(`(\\{(?:${vars.join("|")})\\})`);
  const parts = format.split(re).filter((p) => p !== "");
  const isVar = (p: string) => re.test(p) && p.startsWith("{");
  let kept = parts;
  if (!v.agent.trim()) {
    kept = [];
    for (let i = 0; i < parts.length; i++) {
      if (parts[i] === "{agent}" || parts[i] === "{agent_first}") {
        if (i + 1 < parts.length && !isVar(parts[i + 1])) i++;
        continue;
      }
      kept.push(parts[i]);
    }
    if (!kept.some(isVar)) return v.mailbox;
  }
  const first = v.agent.trim().split(/\s+/)[0] ?? "";
  const value: Record<FromVar, string> = {
    agent: v.agent,
    agent_first: first,
    team: v.team || v.brand || "",
    brand: v.brand ?? "",
    mailbox: v.mailbox,
    workspace: v.workspace ?? "",
  };
  const out = kept
    .map((p) => (isVar(p) ? value[p.slice(1, -1) as FromVar] : p))
    .join("")
    .replace(/\s+/g, " ")
    .replace(SEP, "");
  return out || v.mailbox;
}

/** Whether a name uses any of vars. */
export function hasFromVars(name: string, vars: readonly FromVar[] = COLLAB_FROM_VARS): boolean {
  return (name.match(/\{[a-z_]+\}/g) ?? []).some((x) => vars.includes(x.slice(1, -1) as FromVar));
}

/** What is wrong with a format, as the server would refuse it. */
export function fromNameProblem(format: string, vars: readonly FromVar[] = HELPDESK_FROM_VARS): "long" | "chars" | "variable" | null {
  if (format.trim().replace(/\s+/g, " ").length > FROM_MAX) return "long";
  if (/[<>"]/.test(format)) return "chars";
  const rest = format.replace(/\{([a-z_]+)\}/g, (m, k: string) => (vars.includes(k as FromVar) ? "" : m));
  if (/[{}]/.test(rest)) return "variable";
  return null;
}
