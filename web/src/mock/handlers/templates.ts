// Templates and email design (contracts/templates.md): the variable catalog, each brand's
// layout and satisfaction email, previews and test sends, a ticket's variables and members'
// profiles. The catalog and the defaults are generated from internal/templates
// (templateCatalog.json), so the demo offers what the server does. Filling is a small
// stand-in for the server's engine: variables with filters, if/else blocks and the slots.

import type { EmailLayoutOptions, EmailTemplate, Mailbox, TemplateCatalog, TemplatePreview, TemplateProblem, Ticket } from "@/lib/api/types";
import { db, me, type WsData } from "../db";
import { profileOf } from "../profiles";
import { publish } from "../gateway";
import { forbidden, get, invalid, notFound, patch, post, put, del, respond } from "../router";
import { isAdmin } from "../teams";
import { isRecord, nowIso } from "../util";
import catalog from "../templateCatalog.json";
import { memberOut } from "./workspace";

type Kind = "layout" | "csat";
type Stored = { subject: string; body_html: string; options: EmailLayoutOptions; updated_at: string };

const defaults = catalog as unknown as TemplateCatalog & {
  default_layout: EmailLayoutOptions;
  default_custom_layout: string;
  default_csat_subject: string;
  default_csat_html: string;
};

const SAMPLE_CONTENT =
  `<p>Hi {{ contact.first_name | default: "there" }},</p>` +
  `<p>Thanks for getting in touch. This is how your emails look to customers: your layout, signature and footer around each message.</p>` +
  `<p>Let us know if there is anything else we can do.</p>`;

const FONTS: Record<string, string> = {
  system: `-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif`,
  sans: `Helvetica,Arial,sans-serif`,
  serif: `Georgia,'Times New Roman',Times,serif`,
};

// Per workspace: saved templates by brand and kind.
const saved = new WeakMap<WsData, Record<string, Partial<Record<Kind, Stored>>>>();

function savedOf(ws: WsData) {
  let s = saved.get(ws);
  if (!s) saved.set(ws, (s = {}));
  return s;
}

function templateOut(ws: WsData, brandId: string, kind: Kind): EmailTemplate {
  const row = savedOf(ws)[brandId]?.[kind];
  const base = { brand_id: brandId, kind, options: { ...defaults.default_layout } };
  if (!row)
    return kind === "layout"
      ? { ...base, subject: "", body_html: defaults.default_custom_layout, is_default: true, updated_at: null }
      : { ...base, subject: defaults.default_csat_subject, body_html: defaults.default_csat_html, is_default: true, updated_at: null };
  return { ...base, subject: row.subject, body_html: row.body_html, options: { ...defaults.default_layout, ...row.options }, is_default: false, updated_at: row.updated_at };
}

function brandOf(ws: WsData, id: string | undefined) {
  const b = id ? ws.brands.find((x) => x.id === id) : (ws.brands.find((x) => x.is_default) ?? ws.brands[0]);
  if (!b) throw notFound("Brand");
  return b;
}

function requireAdmin(ws: WsData) {
  if (!isAdmin(ws, db.meId)) throw forbidden("Only admins can change this");
}

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
const known = new Set(defaults.variables.map((v) => v.key));
const SLOTS = new Set(["content", "signature", "history", "survey"]);

/** The values a template sees: the ticket (or sample data), its brand and team, and the person writing. */
function valuesFor(
  ws: WsData,
  opts: { ticket?: Ticket; brandId?: string; teamId?: string; mailboxId?: string; alias?: string },
): { text: Record<string, string>; html: Record<string, string> } {
  const t = opts.ticket;
  const brand = brandOf(ws, opts.brandId ?? t?.brand_id);
  const team = ws.teams.find((x) => x.id === (opts.teamId ?? t?.team_id));
  const contact = t?.contact ? ws.contacts.find((c) => c.id === t.contact!.id) : undefined;
  const user = me();
  const prof = profileOf(ws, user.id);
  const [first, ...rest] = (contact?.name ?? "Jane Cooper").split(" ");
  const [aFirst, ...aRest] = user.name.split(" ");
  const mailbox =
    ws.mailboxes.find((m) => m.id === (opts.mailboxId ?? t?.mailbox_id)) ?? ws.mailboxes.find((m) => m.brand_id === brand.id) ?? ws.mailboxes[0];
  // Mail to an alias reads the alias's own name (contracts/teams.md "Aliases").
  const alias = mailboxAlias(mailbox, opts.alias ?? t?.received_at);
  const text: Record<string, string> = {
    "contact.name": contact?.name ?? "Jane Cooper",
    "contact.first_name": first,
    "contact.last_name": rest.join(" "),
    "contact.email": contact?.email ?? "jane@example.com",
    "contact.phone": contact?.phone ?? "",
    "company.name": contact?.company?.name ?? "Acme Inc.",
    "ticket.number": String(t?.number ?? 1042),
    "ticket.subject": t?.subject ?? "Can't sign in",
    "ticket.status": t ? t.status.replace(/^\w/, (c) => c.toUpperCase()) : "Open",
    "ticket.priority": t ? t.priority.replace(/^\w/, (c) => c.toUpperCase()) : "Normal",
    "ticket.created_date": new Date(t?.created_at ?? Date.now()).toLocaleDateString("en-GB", { day: "numeric", month: "long", year: "numeric" }),
    "agent.name": user.name,
    "agent.first_name": aFirst,
    "agent.last_name": aRest.join(" "),
    "agent.title": prof.title,
    "agent.phone": prof.phone,
    "agent.pronouns": prof.pronouns,
    "agent.avatar_url": user.avatar_url ?? "",
    "team.name": team?.name ?? brand.name,
    "team.description": team?.description ?? "",
    "brand.name": brand.name,
    "brand.color": brand.color,
    "brand.logo_url": brand.images.logo ?? "",
    "brand.help_center_url": `https://help.example.com/${brand.slug}`,
    "mailbox.name": (alias && mailbox?.alias_settings?.[alias]?.display_name) || mailbox?.display_name || mailbox?.name || brand.name,
    "mailbox.email": alias ?? mailbox?.email ?? "support@example.com",
    "workspace.name": ws.workspace.name,
    "date.today": new Date().toLocaleDateString("en-GB", { day: "numeric", month: "long", year: "numeric" }),
  };
  const html: Record<string, string> = { "agent.signature": fill(prof.signature_html, { text, html: {} }) };
  return { text, html };
}

/** addr when it is one of the mailbox's aliases. */
function mailboxAlias(m: Mailbox | undefined, addr: string | undefined): string | null {
  const a = addr?.trim().toLowerCase();
  return m && a && m.aliases.includes(a) ? a : null;
}

/** The signature an email carries: the alias's, else the team's, else the address's, else the author's own. */
function signatureFor(ws: WsData, t: Ticket, mailboxId: string | undefined, vals: { text: Record<string, string>; html: Record<string, string> }) {
  const m = ws.mailboxes.find((x) => x.id === (mailboxId ?? t.mailbox_id));
  const alias = mailboxAlias(m, t.received_at);
  const team = ws.teams.find((x) => x.id === t.team_id);
  const src = [alias ? m?.alias_settings?.[alias]?.signature_html : "", team?.signature_html, m?.signature_html].find((x) => x?.trim());
  return src ? fill(src, vals) : vals.html["agent.signature"];
}

function applyFilters(value: string, filters: string[]): string {
  let v = value;
  for (const f of filters) {
    const [name, arg] = f.split(":").map((x) => x.trim());
    const a = arg?.replace(/^["']|["']$/g, "") ?? "";
    if (name === "default" && !v) v = a;
    else if (name === "upcase") v = v.toUpperCase();
    else if (name === "downcase") v = v.toLowerCase();
    else if (name === "capitalize") v = v.charAt(0).toUpperCase() + v.slice(1);
    else if (name === "append") v += a;
    else if (name === "prepend") v = a + v;
  }
  return v;
}

/** Fills a template: if/else on a variable, then each {{ variable | filters }}; slots and HTML variables go in unescaped. */
function fill(src: string, vals: { text: Record<string, string>; html: Record<string, string> }): string {
  const truthy = (k: string) => !!(vals.text[k] || vals.html[k]);
  let out = src.replace(/\{%-?\s*if\s+([\w.]+)\s*-?%\}([\s\S]*?)(?:\{%-?\s*else\s*-?%\}([\s\S]*?))?\{%-?\s*endif\s*-?%\}/g, (_, k: string, yes: string, no = "") =>
    truthy(k) ? yes : no,
  );
  out = out.replace(/\{%[\s\S]*?%\}/g, "");
  return out.replace(/\{\{\s*([\w.]+)\s*((?:\|[^}]*)?)\}\}/g, (whole, key: string, rest: string) => {
    const filters = rest ? rest.split("|").slice(1) : [];
    if (key in vals.html) return applyFilters(vals.html[key], filters);
    if (key in vals.text) return esc(applyFilters(vals.text[key], filters));
    return known.has(key) || key.includes(".fields.") ? esc(applyFilters("", filters)) : whole;
  });
}

/** The unknown variables and slots a template uses, as the server reports them. */
function problems(src: string, kind: string): TemplateProblem[] {
  const out: TemplateProblem[] = [];
  for (const m of src.matchAll(/\{\{\s*([\w.]+)[^}]*\}\}/g)) {
    const key = m[1];
    if (known.has(key) || key.includes(".fields.")) continue;
    if (SLOTS.has(key)) {
      const slot = defaults.slots.find((s) => s.key === key);
      if (kind === "layout" || kind === "csat" ? true : !slot) continue;
    }
    out.push({ message: `Unknown variable "${key}".`, token: m[0] });
  }
  return out;
}

function surveyButtons(accent: string): string {
  const labels = ["", "Very poor", "Poor", "Okay", "Good", "Great"];
  let h = `<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;margin:4px 0 16px"><tr>`;
  for (let n = 1; n <= 5; n++)
    h += `<td style="padding:0 6px 0 0"><a href="#" title="${labels[n]}" style="display:inline-block;width:44px;height:44px;line-height:42px;border:2px solid ${accent};border-radius:8px;color:#0A0A0A;text-decoration:none;font-weight:600;font-size:16px;text-align:center">${n}</a></td>`;
  return h + `</tr></table>`;
}

/** The email around a message, as internal/templates RenderEmail builds it. */
function renderEmail(o: EmailLayoutOptions, custom: string, parts: { content: string; signature: string; history: string; footer: string }, brand: { name: string; color: string; logo: string }) {
  const accent = o.accent || brand.color || "#0A0A0A";
  const font = FONTS[o.font] ?? FONTS.system;
  const sig = parts.signature.trim() ? `<div style="margin-top:20px;color:#404040">${parts.signature}</div>` : "";
  let body: string;
  if (o.mode === "custom" && custom.trim()) {
    body = custom.replace(/\{\{\s*content\s*\}\}/g, parts.content).replace(/\{\{\s*signature\s*\}\}/g, parts.signature).replace(/\{\{\s*history\s*\}\}/g, parts.history);
  } else if (o.style === "branded") {
    const head = o.logo
      ? brand.logo
        ? `<img src="${esc(brand.logo)}" alt="${esc(brand.name)}" height="30" style="display:block;height:30px;width:auto;max-width:220px;border:0">`
        : `<span style="font-size:15px;font-weight:600;color:#171717">${esc(brand.name)}</span>`
      : "";
    body =
      `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#F4F4F5"><tr><td align="center" style="padding:28px 12px">` +
      `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:600px;background:#FFFFFF;border:1px solid #E5E5E5;border-top:3px solid ${accent};border-radius:10px;font-family:${font}">` +
      (head ? `<tr><td style="padding:22px 32px 4px">${head}</td></tr>` : "") +
      `<tr><td style="padding:18px 32px 26px;font-size:14px;line-height:1.6;color:#171717">${parts.content}${sig}` +
      (parts.history ? `<div style="margin-top:24px;border-top:1px solid #EFEFEF;padding-top:16px">${parts.history}</div>` : "") +
      `</td></tr></table>` +
      (parts.footer ? `<div style="max-width:600px;padding:16px 32px 0;font-size:12px;line-height:1.5;color:#8A8A8A;font-family:${font}">${parts.footer}</div>` : "") +
      `</td></tr></table>`;
  } else {
    body =
      `<div style="max-width:640px;padding:16px 20px;font-family:${font};font-size:14px;line-height:1.6;color:#171717">` +
      (o.logo && brand.logo ? `<div style="margin:0 0 18px"><img src="${esc(brand.logo)}" alt="${esc(brand.name)}" height="28" style="display:block;height:28px;width:auto;max-width:220px;border:0"></div>` : "") +
      parts.content +
      sig +
      (parts.footer ? `<div style="margin-top:28px;padding-top:12px;border-top:1px solid #EBEBEB;font-size:12px;line-height:1.5;color:#8A8A8A">${parts.footer}</div>` : "") +
      (parts.history ? `<div style="margin-top:24px">${parts.history}</div>` : "") +
      `</div>`;
  }
  return (
    `<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0}p{margin:0 0 12px}a{color:${accent}}blockquote{margin:0 0 12px;padding-left:12px;border-left:2px solid #D4D4D4;color:#525252}</style></head>` +
    `<body style="margin:0;padding:0">${body}</body></html>`
  );
}

function historyHTML(o: EmailLayoutOptions, author: string): string {
  if (o.history === "none") return "";
  const when = new Date(Date.now() - 3600_000).toLocaleString("en-GB", { weekday: "short", day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" });
  const header = (o.history_header || "On {{ message.date }}, {{ message.author }} wrote:").replace(/\{\{\s*message\.date\s*\}\}/g, when).replace(/\{\{\s*message\.author\s*\}\}/g, author);
  return (
    `<div><div style="margin:0 0 6px;font-size:12px;color:#737373">${esc(header)}</div>` +
    `<blockquote style="margin:0 0 16px;padding:0 0 0 12px;border-left:2px solid #D4D4D4;color:#525252"><p>Hi, I can't sign in to my account since this morning. Could you take a look?</p></blockquote></div>`
  );
}

const textOf = (h: string) =>
  h
    .replace(/<(br|\/p|\/div|\/tr|\/li|\/h\d)\s*\/?>/gi, "\n")
    .replace(/<[^>]+>/g, "")
    .replace(/&nbsp;/g, " ")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, "&")
    .replace(/\n{3,}/g, "\n\n")
    .trim();

/** A reply as it goes out (layout, signature, history), the copy a sent email keeps. */
export function renderSentReply(ws: WsData, ticketId: string, html: string, mailboxId: string | null): string {
  return preview(ws, { kind: "reply", body_html: html, ticket_id: ticketId, mailbox_id: mailboxId ?? undefined }).html;
}

function preview(ws: WsData, b: Record<string, unknown>): TemplatePreview {
  const kind = String(b.kind ?? "");
  const ticket = typeof b.ticket_id === "string" ? ws.tickets.find((x) => x.id === b.ticket_id) : undefined;
  const mailboxId = typeof b.mailbox_id === "string" && b.mailbox_id ? b.mailbox_id : undefined;
  const alias = mailboxAlias(ws.mailboxes.find((m) => m.id === mailboxId), typeof b.alias === "string" ? b.alias : undefined) ?? undefined;
  const aliasBrand = alias ? ws.mailboxes.find((m) => m.id === mailboxId)?.alias_settings?.[alias]?.brand_id : undefined;
  const brand = brandOf(ws, typeof b.brand_id === "string" ? b.brand_id : (aliasBrand ?? ticket?.brand_id));
  const vals = valuesFor(ws, { ticket, brandId: brand.id, teamId: typeof b.team_id === "string" ? b.team_id : undefined, mailboxId, alias });
  const layout = templateOut(ws, brand.id, "layout");
  const csat = templateOut(ws, brand.id, "csat");
  let o = layout.options;
  let custom = layout.body_html;
  const body = typeof b.body_html === "string" ? b.body_html : "";
  const subjectIn = typeof b.subject === "string" ? b.subject : "";
  const number = vals.text["ticket.number"];
  let subject = `Re: ${vals.text["ticket.subject"]} [#${number}]`;
  let content = fill(SAMPLE_CONTENT, vals);
  let signature = vals.html["agent.signature"];
  let probs: TemplateProblem[];
  let history = historyHTML(o, vals.text["contact.name"]);
  switch (kind) {
    case "reply":
      probs = problems(body, kind);
      content = fill(body, vals);
      // On a ticket, the signature its reply would carry, as the server picks it.
      if (ticket) signature = signatureFor(ws, ticket, mailboxId, vals);
      break;
    case "signature":
    case "personal_signature":
      probs = problems(body, kind);
      signature = fill(body, vals);
      break;
    case "auto_reply":
      probs = [...problems(body, kind), ...problems(subjectIn, "subject")];
      content = fill(body, vals);
      signature = "";
      history = "";
      subject = subjectIn.trim() ? `${fill(subjectIn, vals)}${/\[#\d+\]/.test(subjectIn) ? "" : ` [#${number}]`}` : subject;
      break;
    case "layout":
      if (isRecord(b.options)) o = { ...o, ...(b.options as Partial<EmailLayoutOptions>) };
      if (body.trim()) custom = body;
      probs = o.mode === "custom" ? problems(custom, "layout") : [];
      history = historyHTML(o, vals.text["contact.name"]);
      break;
    case "csat": {
      const src = body.trim() ? body : csat.body_html;
      const subj = subjectIn || csat.subject;
      probs = [...problems(src, "csat"), ...problems(subj, "subject")];
      vals.html.survey = surveyButtons(o.accent || brand.color);
      content = fill(src, vals);
      subject = `${fill(subj, vals)} [#${number}]`;
      history = "";
      break;
    }
    default:
      throw invalid("kind must be reply, signature, personal_signature, auto_reply, layout or csat");
  }
  const footer = o.mode !== "custom" && o.footer_html.trim() ? fill(o.footer_html, vals) : "";
  const html = renderEmail(o, fill(custom, vals), { content, signature, history, footer }, { name: brand.name, color: brand.color, logo: brand.images.logo ?? "" });
  return { subject: textOf(subject), html, text: [textOf(content), signature ? `-- \n${textOf(signature)}` : "", textOf(footer)].filter(Boolean).join("\n\n") + "\n", problems: probs };
}

/** The groups a kind reads when it takes only some (a Collab signature: the sender, the address, the workspace and the date). */
const KIND_GROUPS = (defaults as TemplateCatalog).kind_groups ?? {};
const groupOf = new Map(defaults.variables.map((v) => [v.key, v.group]));

/**
 * A Collab signature filled for the person writing (contracts/collab.md "Addresses"):
 * agent.* is you, mailbox.* the address and its From name, and nothing of a ticket.
 */
export function fillForSender(ws: WsData, src: string, mailbox: { name: string; email: string }): string {
  const vals = valuesFor(ws, {});
  vals.text["mailbox.name"] = mailbox.name;
  vals.text["mailbox.email"] = mailbox.email;
  vals.html["agent.signature"] = fill(profileOf(ws, me().id).signature_html, { text: vals.text, html: {} });
  return fill(src, vals);
}

/** What is wrong with a Collab signature: unknown variables, and ones it has nothing to read for. */
export function collabSignatureProblems(src: string): TemplateProblem[] {
  const out = problems(src, "signature");
  const groups = KIND_GROUPS.collab_signature ?? [];
  for (const m of src.matchAll(/\{\{\s*([\w.]+)[^}]*\}\}/g)) {
    const g = groupOf.get(m[1]) ?? (m[1].includes(".fields.") ? "contact" : undefined);
    if (g && !groups.includes(g)) out.push({ message: `{{ ${m[1]} }} has nothing to read here: mail from an account has no ticket, customer, team or brand.`, token: m[0] });
  }
  return out;
}

export function registerTemplates() {
  get("/v1/templates/variables", (ctx) => {
    const fields = ctx.ws.customFields
      .filter((f) => !f.archived)
      .map((f) => ({ key: `${f.entity}.fields.${f.key}`, group: f.entity, label: f.name, description: f.description || "A custom field.", example: "", html: false, list: f.type === "multi_select" }));
    return { variables: [...defaults.variables, ...fields], slots: defaults.slots, filters: defaults.filters, tags: defaults.tags, kind_groups: KIND_GROUPS };
  });

  post("/v1/templates/preview", (ctx) => preview(ctx.ws, ctx.body));
  post("/v1/templates/test", (ctx) => {
    preview(ctx.ws, ctx.body);
    return respond(202, { to: me().email });
  });

  get("/v1/email-templates/:brand_id", (ctx) => {
    const b = brandOf(ctx.ws, ctx.params.brand_id);
    return { layout: templateOut(ctx.ws, b.id, "layout"), csat: templateOut(ctx.ws, b.id, "csat") };
  });

  put("/v1/email-templates/:brand_id/:kind", (ctx) => {
    requireAdmin(ctx.ws);
    const b = brandOf(ctx.ws, ctx.params.brand_id);
    const kind = ctx.params.kind as Kind;
    if (kind !== "layout" && kind !== "csat") throw notFound("Template");
    const cur = templateOut(ctx.ws, b.id, kind);
    const next: Stored = {
      subject: typeof ctx.body.subject === "string" ? ctx.body.subject : cur.subject,
      body_html: typeof ctx.body.body_html === "string" ? ctx.body.body_html : cur.body_html,
      options: isRecord(ctx.body.options) ? { ...cur.options, ...(ctx.body.options as Partial<EmailLayoutOptions>) } : cur.options,
      updated_at: nowIso(),
    };
    const probs = kind === "csat" ? [...problems(next.body_html, "csat"), ...problems(next.subject, "subject")] : next.options.mode === "custom" ? problems(next.body_html, "layout") : [];
    if (probs.length) throw invalid(probs.map((p) => p.message).join(" "));
    (savedOf(ctx.ws)[b.id] ??= {})[kind] = next;
    publish(ctx.ws, "email_template.updated", { brand_id: b.id, kind });
    return templateOut(ctx.ws, b.id, kind);
  });

  del("/v1/email-templates/:brand_id/:kind", (ctx) => {
    requireAdmin(ctx.ws);
    const b = brandOf(ctx.ws, ctx.params.brand_id);
    const kind = ctx.params.kind as Kind;
    delete savedOf(ctx.ws)[b.id]?.[kind];
    publish(ctx.ws, "email_template.updated", { brand_id: b.id, kind });
    return respond(204);
  });

  get("/v1/tickets/:id/variables", (ctx) => {
    const t = ctx.ws.tickets.find((x) => x.id === ctx.params.id || String(x.number) === ctx.params.id);
    if (!t) throw notFound("Ticket");
    const mailboxId = ctx.query.get("mailbox_id") || undefined;
    const vals = valuesFor(ctx.ws, { ticket: t, mailboxId });
    return { values: vals.text, signature_html: signatureFor(ctx.ws, t, mailboxId, vals) };
  });

  post("/v1/tickets/:id/render", (ctx) => {
    const t = ctx.ws.tickets.find((x) => x.id === ctx.params.id || String(x.number) === ctx.params.id);
    if (!t) throw notFound("Ticket");
    const vals = valuesFor(ctx.ws, { ticket: t });
    // For a side conversation: the brand, team and sender, never the customer's or the ticket's details.
    if (ctx.body.thread === true) for (const k of Object.keys(vals.text)) if (/^(contact|company|ticket)\./.test(k)) delete vals.text[k];
    return { body_html: fill(typeof ctx.body.body_html === "string" ? ctx.body.body_html : "", vals) };
  });

  patch("/v1/members/:user_id/profile", (ctx) => {
    const uid = ctx.params.user_id === "me" ? db.meId : ctx.params.user_id;
    if (!ctx.ws.members.some((m) => m.user_id === uid)) throw notFound("Member");
    if (uid !== db.meId && !isAdmin(ctx.ws, db.meId)) throw forbidden("Only admins can change someone else's profile");
    const p = profileOf(ctx.ws, uid);
    for (const k of ["title", "phone", "pronouns", "signature_html"] as const) if (typeof ctx.body[k] === "string") p[k] = ctx.body[k] as string;
    const member = memberOut(ctx.ws, uid);
    publish(ctx.ws, "member.updated", { member }, { topics: [`user:${uid}`] });
    return member;
  });
}
