// Contacts, companies, tags, saved replies, views, SLA, automations, the
// knowledge base, mailboxes and the public help center / CSAT pages.

import type {
  Action,
  Automation,
  Brand,
  Company,
  Condition,
  Contact,
  KbArticle,
  KbCollection,
  Mailbox,
  MailboxConfig,
  Priority,
  SavedReply,
  SlaPolicy,
  TicketFilters,
  View,
} from "@/lib/api/types";
import { db, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, get, invalid, notFound, paginate, patch, post, required, respond, s, strArr, type Ctx } from "../router";
import { applyLabels, canCreateLabels, fieldFiltersFromQuery, fieldsText, matchFields, mergeFields } from "../labels";
import { addActivity, publishTicketUpdated, sortTickets } from "../tickets";
import { isRecord, nowIso, randomToken, slugify, uuid } from "../util";
import { aiOff } from "./ai";
import { defaultBrand, isAdmin, teamOut } from "../teams";
import { checkLimit } from "./billing";
import { authRefusedCause, connectOptions, detect, imapOff, mockApiSyncOn, mockImapOn, mockOAuthEnabled, mockSystemSendOn, receivingOf, refusal, sendingOf, syncReceiving, systemSendOff } from "./mailhost";
import { assign, crud } from "./crud";
import { checkBotId } from "./bots";

function contactOut(ws: WsData, c: Contact): Contact {
  c.ticket_count = ws.tickets.filter((t) => t.contact?.id === c.id && !ws.deletedTickets.has(t.id)).length;
  return c;
}

function companyOut(ws: WsData, c: Company): Company {
  c.contact_count = ws.contacts.filter((x) => x.company?.id === c.id).length;
  return c;
}

function setCompany(ws: WsData, c: Contact, companyId: unknown) {
  if (companyId === null || companyId === "") c.company = null;
  else if (typeof companyId === "string") {
    const co = ws.companies.find((x) => x.id === companyId);
    if (!co) throw invalid("Unknown company");
    c.company = { id: co.id, name: co.name, domain: co.domain, avatar_url: co.avatar_url };
  }
}

/** tags (replace), add_tags and remove_tags on a contact or company write. */
function writeLabels(ctx: Ctx, scope: "contacts" | "companies", row: Contact | Company): boolean {
  const replace = strArr(ctx.body.tags);
  const add = strArr(ctx.body.add_tags);
  const remove = strArr(ctx.body.remove_tags);
  if (!replace && !add?.length && !remove?.length) return false;
  const r = applyLabels(ctx.ws, scope, row.tags, { replace, add, remove }, { create: canCreateLabels(ctx.ws) });
  row.tags = r.next;
  return r.added.length + r.removed.length > 0;
}

function writeFields(ctx: Ctx, entity: "contact" | "company", row: Contact | Company): boolean {
  if (!isRecord(ctx.body.fields)) return false;
  const r = mergeFields(ctx.ws, entity, row.fields, ctx.body.fields);
  row.fields = r.next;
  return r.changes.length > 0;
}

function recordFilter<R extends Contact | Company>(ctx: Ctx, entity: "contact" | "company", list: R[]): R[] {
  const tag = ctx.query.get("tag")?.trim().toLowerCase();
  const ff = fieldFiltersFromQuery(ctx.ws, entity, ctx.query);
  return list.filter((r) => (!tag || r.tags.some((x) => x.name.toLowerCase() === tag)) && matchFields(ctx.ws, entity, r.fields, ff));
}

function syncContactRefs(ws: WsData, c: Contact) {
  for (const t of ws.tickets) if (t.contact?.id === c.id) t.contact = { id: c.id, email: c.email, name: c.name, avatar_url: c.avatar_url };
}

export function registerResources() {
  // Contacts.
  get("/v1/contacts", (ctx) => {
    const q = (ctx.query.get("q") ?? "").toLowerCase().trim();
    const company = ctx.query.get("company_id");
    let list = ctx.ws.contacts;
    if (company) list = list.filter((c) => c.company?.id === company);
    list = recordFilter(ctx, "contact", list);
    if (q) list = list.filter((c) => [c.name, c.email ?? "", c.company?.name ?? "", c.phone, fieldsText(c.fields)].join(" ").toLowerCase().includes(q));
    list = list.slice().sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
    const page = paginate(ctx, list);
    return { ...page, data: page.data.map((c) => contactOut(ctx.ws, c)) };
  });
  get("/v1/contacts/:id", (ctx) => contactOut(ctx.ws, findContact(ctx)));
  post("/v1/contacts", (ctx) => {
    const email = s(ctx, "email")?.trim().toLowerCase() || null;
    const name = s(ctx, "name")?.trim() || email?.split("@")[0] || "";
    if (!name) throw invalid("Name or email is required");
    if (email && ctx.ws.contacts.some((c) => c.email === email)) throw new HttpError(409, "conflict", "A contact with this email already exists");
    const c: Contact = { id: uuid(), email, name: name.slice(0, 80), phone: s(ctx, "phone") ?? "", avatar_url: null, avatar_source: null, company: null, external_id: s(ctx, "external_id") ?? null, notes: s(ctx, "notes") ?? "", custom: isRecord(ctx.body.custom) ? ctx.body.custom : {}, tags: [], fields: {}, blocked: false, ticket_count: 0, created_at: nowIso(), updated_at: nowIso() };
    setCompany(ctx.ws, c, ctx.body.company_id);
    writeLabels(ctx, "contacts", c);
    writeFields(ctx, "contact", c);
    ctx.ws.contacts.unshift(c);
    publish(ctx.ws, "contact.created", { contact: c });
    return respond(201, c);
  });
  patch("/v1/contacts/:id", (ctx) => {
    const c = findContact(ctx);
    if ("name" in ctx.body && !s(ctx, "name")?.trim()) throw invalid("Name can't be empty");
    if (s(ctx, "email")) {
      const email = s(ctx, "email")!.trim().toLowerCase();
      if (ctx.ws.contacts.some((x) => x.email === email && x.id !== c.id)) throw new HttpError(409, "conflict", "Another contact already uses this email");
      c.email = email;
    }
    writeLabels(ctx, "contacts", c);
    writeFields(ctx, "contact", c);
    assign(c, ctx.body, ["name", "phone", "notes", "external_id", "blocked", "custom"]);
    if ("company_id" in ctx.body) setCompany(ctx.ws, c, ctx.body.company_id);
    c.updated_at = nowIso();
    syncContactRefs(ctx.ws, c);
    publish(ctx.ws, "contact.updated", { contact: contactOut(ctx.ws, c) });
    return c;
  });
  del("/v1/contacts/:id", (ctx) => {
    const c = findContact(ctx);
    ctx.ws.contacts = ctx.ws.contacts.filter((x) => x !== c);
    publish(ctx.ws, "contact.deleted", { contact_id: c.id });
    return respond(204);
  });
  get("/v1/contacts/:id/tickets", (ctx) => {
    const c = findContact(ctx);
    const list = ctx.ws.tickets.filter((t) => t.contact?.id === c.id && !ctx.ws.deletedTickets.has(t.id));
    return paginate(ctx, sortTickets(list, "last_message_at", "desc"));
  });

  // Companies.
  get("/v1/companies", (ctx) => {
    const q = (ctx.query.get("q") ?? "").toLowerCase().trim();
    let list = ctx.ws.companies;
    list = recordFilter(ctx, "company", list);
    if (q) list = list.filter((c) => [c.name, c.domain, fieldsText(c.fields)].join(" ").toLowerCase().includes(q));
    list = list.slice().sort((a, b) => a.name.localeCompare(b.name));
    const page = paginate(ctx, list);
    return { ...page, data: page.data.map((c) => companyOut(ctx.ws, c)) };
  });
  get("/v1/companies/:id", (ctx) => companyOut(ctx.ws, findCompany(ctx)));
  post("/v1/companies", (ctx) => {
    const name = required(ctx, "name").slice(0, 80);
    const c: Company = { id: uuid(), name, domain: (s(ctx, "domain") ?? "").toLowerCase(), avatar_url: null, avatar_source: null, notes: s(ctx, "notes") ?? "", custom: isRecord(ctx.body.custom) ? ctx.body.custom : {}, tags: [], fields: {}, contact_count: 0, created_at: nowIso(), updated_at: nowIso() };
    writeLabels(ctx, "companies", c);
    writeFields(ctx, "company", c);
    ctx.ws.companies.push(c);
    publish(ctx.ws, "company.created", { company: c });
    return respond(201, c);
  });
  patch("/v1/companies/:id", (ctx) => {
    const c = findCompany(ctx);
    if ("name" in ctx.body && !s(ctx, "name")?.trim()) throw invalid("Name can't be empty");
    writeLabels(ctx, "companies", c);
    writeFields(ctx, "company", c);
    assign(c, ctx.body, ["name", "domain", "notes", "custom"]);
    c.updated_at = nowIso();
    for (const ct of ctx.ws.contacts) if (ct.company?.id === c.id) ct.company = { id: c.id, name: c.name, domain: c.domain, avatar_url: c.avatar_url };
    publish(ctx.ws, "company.updated", { company: companyOut(ctx.ws, c) });
    return c;
  });
  del("/v1/companies/:id", (ctx) => {
    const c = findCompany(ctx);
    ctx.ws.companies = ctx.ws.companies.filter((x) => x !== c);
    for (const ct of ctx.ws.contacts) if (ct.company?.id === c.id) ct.company = null;
    publish(ctx.ws, "company.deleted", { company_id: c.id });
    return respond(204);
  });

  crud<SavedReply>({
    path: "/v1/saved-replies",
    resource: "saved_reply",
    label: "saved reply",
    list: (ws) => ws.savedReplies,
    sort: (a, b) => a.title.localeCompare(b.title),
    // A private team's replies are listed only to its members.
    filter: (ctx, rows) => rows.filter((r) => canSeeTeamScoped(ctx.ws, r.team_id)),
    create: (ctx, id) => ({ id, title: required(ctx, "title").slice(0, 100), shortcut: slugify(s(ctx, "shortcut") ?? "").slice(0, 30), body_html: required(ctx, "body_html"), team_id: teamRef(ctx, ctx.body.team_id), created_at: nowIso(), updated_at: nowIso() }),
    update: (ctx, row) => {
      assign(row, ctx.body, ["title", "body_html"]);
      if ("team_id" in ctx.body) row.team_id = teamRef(ctx, ctx.body.team_id);
      if (s(ctx, "shortcut") !== undefined) row.shortcut = slugify(s(ctx, "shortcut")!).slice(0, 30);
      row.updated_at = nowIso();
    },
  });

  crud<View>({
    path: "/v1/views",
    resource: "view",
    list: (ws) => ws.views,
    sort: (a, b) => a.position - b.position,
    // A team view is listed to that team's members (and admins, who manage them).
    filter: (ctx, rows) => rows.filter((v) => !v.team_id || isAdmin(ctx.ws, db.meId) || !!teamOfId(ctx.ws, v.team_id, db.meId)),
    create: (ctx, id) => {
      const team_id = teamRef(ctx, ctx.body.team_id);
      return { id, name: required(ctx, "name").slice(0, 60), shared: team_id ? false : ctx.body.shared === true, team_id, filters: isRecord(ctx.body.filters) ? (ctx.body.filters as TicketFilters) : {}, position: typeof ctx.body.position === "number" ? ctx.body.position : ctx.ws.views.length };
    },
    update: (ctx, row) => {
      assign(row, ctx.body, ["name", "shared", "position"]);
      if ("team_id" in ctx.body) row.team_id = teamRef(ctx, ctx.body.team_id);
      if (row.team_id) row.shared = false;
      if (isRecord(ctx.body.filters)) row.filters = ctx.body.filters as TicketFilters;
    },
  });

  crud<SlaPolicy>({
    path: "/v1/sla-policies",
    resource: "sla_policy",
    label: "SLA policy",
    list: (ws) => ws.sla,
    sort: (a, b) => a.position - b.position,
    create: (ctx, id) => ({
      id,
      name: required(ctx, "name").slice(0, 60),
      priority: (s(ctx, "priority") as Priority | undefined) ?? null,
      first_response_minutes: positive(ctx.body.first_response_minutes, "first_response_minutes"),
      resolution_minutes: positive(ctx.body.resolution_minutes, "resolution_minutes"),
      business_hours: ctx.body.business_hours === true,
      enabled: ctx.body.enabled !== false,
      position: typeof ctx.body.position === "number" ? ctx.body.position : ctx.ws.sla.length,
      team_id: teamRef(ctx, ctx.body.team_id),
    }),
    update: (ctx, row) => {
      assign(row, ctx.body, ["name", "enabled", "position", "business_hours"]);
      if ("team_id" in ctx.body) row.team_id = teamRef(ctx, ctx.body.team_id);
      if ("priority" in ctx.body) row.priority = (s(ctx, "priority") as Priority | undefined) ?? null;
      if ("first_response_minutes" in ctx.body) row.first_response_minutes = positive(ctx.body.first_response_minutes, "first_response_minutes");
      if ("resolution_minutes" in ctx.body) row.resolution_minutes = positive(ctx.body.resolution_minutes, "resolution_minutes");
    },
  });

  crud<Automation>({
    path: "/v1/automations",
    resource: "automation",
    list: (ws) => ws.automations,
    sort: (a, b) => a.position - b.position,
    create: (ctx, id) => ({
      id,
      name: required(ctx, "name").slice(0, 80),
      trigger: (s(ctx, "trigger") as Automation["trigger"] | undefined) ?? "ticket_created",
      conditions: conditions(ctx.body.conditions),
      actions: actions(ctx.body.actions, true),
      enabled: ctx.body.enabled !== false,
      position: ctx.ws.automations.length,
      run_count: 0,
    }),
    update: (ctx, row) => {
      assign(row, ctx.body, ["name", "trigger", "enabled", "position"]);
      if ("conditions" in ctx.body) row.conditions = conditions(ctx.body.conditions);
      if ("actions" in ctx.body) row.actions = actions(ctx.body.actions, true);
    },
  });

  // Knowledge base.
  crud<KbCollection>({
    path: "/v1/kb/collections",
    resource: "kb_collection",
    label: "collection",
    list: (ws) => ws.collections,
    // ?brand_id=: that brand's own collections, or "none" for those of every brand.
    filter: (ctx, rows) => {
      const b = brandListFilter(ctx);
      return b === undefined ? rows : rows.filter((c) => (c.brand_id ?? null) === b);
    },
    sort: (a, b) => a.position - b.position,
    create: (ctx, id) => {
      const name = required(ctx, "name").slice(0, 80);
      return {
        id,
        name,
        slug: uniqueSlug(ctx.ws.collections, slugify(s(ctx, "slug") || name)),
        description: s(ctx, "description") ?? "",
        position: ctx.ws.collections.length,
        article_count: 0,
        brand_id: kbBrandRef(ctx, ctx.body.brand_id),
      };
    },
    update: (ctx, row) => {
      assign(row, ctx.body, ["name", "description", "position"]);
      if (s(ctx, "slug")) row.slug = uniqueSlug(ctx.ws.collections.filter((c) => c !== row), slugify(s(ctx, "slug")!));
      if ("brand_id" in ctx.body) row.brand_id = kbBrandRef(ctx, ctx.body.brand_id);
    },
    beforeDelete: (ctx, row) => {
      for (const a of ctx.ws.articles) if (a.collection_id === row.id) a.collection_id = null;
    },
  });

  get("/v1/kb/articles", (ctx) => {
    const col = ctx.query.get("collection_id");
    const status = ctx.query.get("status");
    const q = (ctx.query.get("q") ?? "").toLowerCase().trim();
    let list = ctx.ws.articles;
    if (col) list = list.filter((a) => a.collection_id === col);
    if (status) list = list.filter((a) => a.status === status);
    const brand = brandListFilter(ctx);
    if (brand !== undefined) list = list.filter((a) => articleBrand(ctx.ws, a) === brand);
    const forBrand = ctx.query.get("for_brand_id");
    if (forBrand) {
      if (!ctx.ws.brands.some((b) => b.id === forBrand)) throw invalid("for_brand_id must be a brand of the workspace");
      list = list.filter((a) => shownBy(ctx.ws, a, forBrand));
    }
    if (q) list = list.filter((a) => (a.title + " " + a.excerpt + " " + a.body_html).toLowerCase().includes(q));
    return paginate(ctx, list.slice().sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at)), 100);
  });
  get("/v1/kb/articles/:id", (ctx) => findArticle(ctx));
  post("/v1/kb/articles", (ctx) => {
    const title = required(ctx, "title").slice(0, 160);
    const status = s(ctx, "status") === "published" ? "published" : "draft";
    const a: KbArticle = {
      id: uuid(),
      brand_id: kbBrandRef(ctx, ctx.body.brand_id),
      collection_id: s(ctx, "collection_id") ?? null,
      title,
      slug: uniqueSlug(ctx.ws.articles, slugify(s(ctx, "slug") || title)),
      excerpt: s(ctx, "excerpt") ?? "",
      body_html: s(ctx, "body_html") ?? "",
      status,
      author: userRef(db.meId),
      views: 0,
      published_at: status === "published" ? nowIso() : null,
      updated_at: nowIso(),
    };
    ctx.ws.articles.unshift(a);
    recountCollections(ctx.ws);
    publish(ctx.ws, "kb_article.created", { kb_article: a });
    return respond(201, a);
  });
  patch("/v1/kb/articles/:id", (ctx) => {
    const a = findArticle(ctx);
    if ("title" in ctx.body && !s(ctx, "title")?.trim()) throw invalid("Title can't be empty");
    assign(a, ctx.body, ["title", "excerpt", "body_html"]);
    if ("collection_id" in ctx.body) a.collection_id = s(ctx, "collection_id") || null;
    if ("brand_id" in ctx.body) a.brand_id = kbBrandRef(ctx, ctx.body.brand_id);
    if (s(ctx, "slug")) a.slug = uniqueSlug(ctx.ws.articles.filter((x) => x !== a), slugify(s(ctx, "slug")!));
    const status = s(ctx, "status");
    if (status === "published" || status === "draft") {
      if (status === "published" && a.status !== "published") a.published_at = nowIso();
      if (status === "draft") a.published_at = null;
      a.status = status;
    }
    a.updated_at = nowIso();
    recountCollections(ctx.ws);
    publish(ctx.ws, "kb_article.updated", { kb_article: a });
    return a;
  });
  del("/v1/kb/articles/:id", (ctx) => {
    const a = findArticle(ctx);
    ctx.ws.articles = ctx.ws.articles.filter((x) => x !== a);
    recountCollections(ctx.ws);
    publish(ctx.ws, "kb_article.deleted", { kb_article_id: a.id });
    return respond(204);
  });

  // Mailboxes.
  get("/v1/mailboxes", (ctx) => {
    syncReceiving(ctx.ws.mailboxes);
    return paginate(ctx, ctx.ws.mailboxes, 100);
  });
  get("/v1/mailboxes/connect-options", () => connectOptions("in.opendesk.sh"));
  get("/v1/mailboxes/detect", (ctx) => {
    const email = (ctx.query.get("email") ?? "").trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) throw invalid("Enter a valid email address");
    return detect(email);
  });
  get("/v1/mailboxes/:id", (ctx) => {
    const m = findMailbox(ctx);
    m.receiving = receivingOf(m);
    m.sending = sendingOf(m);
    return m;
  });
  post("/v1/mailboxes", (ctx) => {
    checkLimit(ctx.ws, "mailbox");
    const email = required(ctx, "email").toLowerCase();
    if (ctx.ws.mailboxes.some((m) => m.email === email || m.aliases.includes(email))) throw new HttpError(409, "conflict", "A mailbox with this address already exists");
    const inbound = (s(ctx, "inbound_kind") ?? "forwarding") as Mailbox["inbound_kind"];
    const outbound = (s(ctx, "outbound_kind") ?? "system") as Mailbox["outbound_kind"];
    if (inbound !== "forwarding" && !mockImapOn()) throw imapOff();
    if (outbound === "system" && !mockSystemSendOn()) throw systemSendOff();
    if ((outbound === "google" || outbound === "microsoft") && inbound !== outbound)
      throw invalid(`Sending with ${outbound === "google" ? "Google" : "Microsoft"} needs its sign-in: connect it from the mailbox.`);
    const config = isRecord(ctx.body.config) ? (ctx.body.config as MailboxConfig) : {};
    if (inbound === "imap" && (!config.imap_host || !config.imap_username)) throw invalid("IMAP host and username are required");
    if (outbound === "smtp" && (!config.smtp_host || !config.smtp_username)) throw invalid("SMTP host and username are required");
    const host = config.mail_host || detect(email).host || "other";
    if (ctx.body.verify === true && (inbound === "imap" || outbound === "smtp")) {
      const failed = refusal(host, (inbound === "imap" ? s(ctx, "password") : s(ctx, "smtp_password")) ?? "", inbound !== "imap");
      if (failed)
        throw new HttpError(422, "mailbox_connection_failed", `${failed.cause.title}. Nothing was saved.`, { ...failed });
    }
    const m = mailbox(ctx.ws, { name: required(ctx, "name"), email, display_name: s(ctx, "display_name") ?? "", inbound_kind: inbound, outbound_kind: outbound, config: { ...config, mail_host: host }, team_id: teamRef(ctx, ctx.body.team_id) });
    mailboxSecrets.set(m.id, { password: s(ctx, "password") ?? "", smtp: s(ctx, "smtp_password") ?? "" });
    writeAddressing(ctx, m);
    if ((s(ctx, "password") ?? "") === "wrong" || (s(ctx, "smtp_password") ?? "") === "wrong") {
      m.status = "error";
      m.last_error = "IMAP login failed: [AUTHENTICATIONFAILED] Invalid credentials";
    }
    publish(ctx.ws, "mailbox.created", { mailbox: m });
    return respond(201, m);
  });
  patch("/v1/mailboxes/:id", (ctx) => {
    const m = findMailbox(ctx);
    if (ctx.body.outbound_kind === "system" && m.outbound_kind !== "system" && !mockSystemSendOn()) throw systemSendOff();
    patchReceiving(ctx, m);
    assign(m, ctx.body, ["name", "display_name", "signature_html", "auto_reply_enabled", "auto_reply_subject", "auto_reply_html", "status"]);
    const prevTeam = m.team_id;
    if ("team_id" in ctx.body) m.team_id = teamRef(ctx, ctx.body.team_id);
    writeAddressing(ctx, m);
    // Teams list their addresses.
    if (prevTeam !== m.team_id) for (const t of ctx.ws.teams) if (t.id === prevTeam || t.id === m.team_id) publish(ctx.ws, "team.updated", { team: teamOut(ctx.ws, t) });
    patchConnection(ctx, m);
    patchMailboxAI(ctx, m);
    patchFormSenders(ctx, m);
    if ("bot_id" in ctx.body) m.bot_id = checkBotId(ctx.ws, ctx.body.bot_id);
    if (ctx.body.password || ctx.body.smtp_password) {
      m.status = "active";
      m.last_error = "";
    }
    m.receiving = receivingOf(m);
    m.sending = sendingOf(m);
    publish(ctx.ws, "mailbox.updated", { mailbox: m });
    return m;
  });
  del("/v1/mailboxes/:id", (ctx) => {
    const m = findMailbox(ctx);
    ctx.ws.mailboxes = ctx.ws.mailboxes.filter((x) => x !== m);
    publish(ctx.ws, "mailbox.deleted", { mailbox_id: m.id });
    return respond(204);
  });
  post("/v1/mailboxes/:id/test", (ctx) => {
    const m = findMailbox(ctx);
    // Forwarding in, SMTP out: only the SMTP server signs in, with the stored password
    // (a mailbox in error is refused as a wrong password is).
    const sec = mailboxSecrets.get(m.id);
    if (m.inbound_kind === "forwarding" && m.outbound_kind === "smtp" && (sec || m.status === "error")) {
      const failed = refusal(m.config.mail_host || detect(m.email).host || "other", m.status === "error" ? "wrong" : (sec?.smtp || sec?.password) ?? "", true);
      if (failed) return respond(200, failed);
    } else if (m.status === "error")
      return respond(200, {
        inbound: { ok: false, error: "The server refused the sign-in.", detail: m.last_error || "[AUTHENTICATIONFAILED] Invalid credentials" },
        outbound: m.outbound_kind === "smtp" ? { ok: false, error: "The server refused the sign-in.", detail: "535 5.7.8 Authentication failed" } : { ok: true, error: "" },
        cause: authRefusedCause,
      });
    m.last_synced_at = nowIso();
    publish(ctx.ws, "mailbox.updated", { mailbox: m });
    return respond(200, { inbound: { ok: true, error: "" }, outbound: { ok: true, error: "" } });
  });
  // Google or Microsoft sign-in: to send only (receive "forwarding", the default), to sync
  // over IMAP as well (receive "sync", where the instance receives over IMAP), or to read the
  // inbox directly (receive "api", where the workspace has Collab).
  post("/v1/mailboxes/oauth/:provider/start", (ctx) => {
    const provider = ctx.params.provider;
    if (provider !== "google" && provider !== "microsoft") throw notFound("Provider");
    const receive = s(ctx, "receive") ?? "forwarding";
    if (receive !== "forwarding" && receive !== "sync" && receive !== "api") throw invalid("receive must be forwarding, sync or api");
    if (receive === "sync" && !mockImapOn()) throw imapOff();
    if (receive === "api" && !mockApiSyncOn())
      throw new HttpError(422, "api_sync_unavailable", "Reading the inbox directly needs Collab on this workspace's plan. Forward its mail to opendesk instead.");
    const existingId = s(ctx, "mailbox_id");
    let m = existingId ? ctx.ws.mailboxes.find((x) => x.id === existingId) : undefined;
    if (existingId && !m) throw notFound("Mailbox");
    const connected = m?.inbound_kind === provider || m?.outbound_kind === provider;
    if (!mockOAuthEnabled(provider) && !connected) {
      const name = provider === "google" ? "Google" : "Microsoft";
      throw new HttpError(403, "mailbox_oauth_disabled", `Connecting a mailbox with ${name} sign-in is not available on this instance. Forward its mail into opendesk and send through opendesk or SMTP.`);
    }
    const email = s(ctx, "email")?.toLowerCase() || `support@${provider === "google" ? "margin.app" : "margin.onmicrosoft.com"}`;
    // A new mailbox receives as asked; a reconnect keeps how it receives, moving to
    // forwarding where the instance no longer receives over IMAP.
    const wasDirect = m?.config.oauth_grant === "api" && m.inbound_kind !== "forwarding";
    const direct = receive === "api" || (wasDirect && mockApiSyncOn());
    const inbound = direct || (m ? m.inbound_kind !== "forwarding" && !wasDirect && mockImapOn() : receive === "sync") ? provider : "forwarding";
    const grant = direct ? "api" : inbound === provider ? "sync" : "send";
    if (!m) {
      if (ctx.ws.mailboxes.some((x) => x.email === email || x.aliases.includes(email))) throw new HttpError(409, "conflict", "A mailbox with this address already exists");
      // Reading directly names the sender as the account does.
      m = mailbox(ctx.ws, { name: s(ctx, "name") || "Support", email, display_name: direct ? "Margin Support" : "", inbound_kind: inbound, outbound_kind: provider, config: { oauth_email: email, oauth_grant: grant, ...(direct ? { oauth_client: "collab" as const } : {}) }, team_id: teamRef(ctx, ctx.body.team_id) });
      writeAddressing(ctx, m);
      m.receiving = receivingOf(m);
    m.sending = sendingOf(m);
      publish(ctx.ws, "mailbox.created", { mailbox: m });
    } else {
      if (m.inbound_kind !== "forwarding" && inbound === "forwarding") stopImap(m);
      m.status = "active";
      m.last_error = "";
      m.inbound_kind = inbound;
      m.outbound_kind = provider;
      m.config = { ...m.config, oauth_email: m.config.oauth_email || m.email, oauth_grant: grant, oauth_client: direct ? "collab" : undefined };
      for (const k of SMTP_KEYS) delete m.config[k];
      mailboxSecrets.delete(m.id);
      m.receiving = receivingOf(m);
    m.sending = sendingOf(m);
      publish(ctx.ws, "mailbox.updated", { mailbox: m });
    }
    return respond(200, { url: `/settings/mailboxes?connected=${m.id}` });
  });

  // Public help center: one brand's, with its own collections and articles and those of every brand.
  const publicArticles = (ws: WsData, brand: Brand | undefined) =>
    ws.articles.filter((a) => a.status === "published" && (!brand || shownBy(ws, a, brand.id)));
  get("/v1/public/help/:slug", (ctx) => {
    const { ws, brand } = publicBrand(ctx);
    const published = publicArticles(ws, brand);
    return {
      title: (brand?.is_default ? ws.workspace.settings.help_center_title : brand?.help_center.title) || brand?.help_center.title || `${brand?.name ?? ws.workspace.name} Help`,
      collections: ws.collections
        .filter((c) => !brand || !c.brand_id || c.brand_id === brand.id)
        .map((c) => ({ ...c, article_count: published.filter((a) => a.collection_id === c.id).length }))
        .filter((c) => c.article_count > 0)
        .sort((a, b) => a.position - b.position),
      featured: published.slice().sort((a, b) => b.views - a.views).slice(0, 6),
      locale: brand?.locale || ws.workspace.settings.locale,
      // The brand's help center logo, else its logo, else the workspace's (contracts/api.md).
      logo_url: brand?.images.help_center_logo || brand?.images.logo || ws.workspace.avatar_url || null,
      favicon_url: brand?.images.help_center_favicon || null,
    };
  });
  get("/v1/public/help/:slug/collections/:cslug", (ctx) => {
    const { ws, brand } = publicBrand(ctx);
    const col = ws.collections.find((c) => c.slug === ctx.params.cslug && (!brand || !c.brand_id || c.brand_id === brand.id));
    if (!col) throw notFound("Collection");
    const articles = publicArticles(ws, brand).filter((a) => a.collection_id === col.id);
    return { collection: { ...col, article_count: articles.length }, articles };
  });
  get("/v1/public/help/:slug/articles/:aslug", (ctx) => {
    const { ws, brand } = publicBrand(ctx);
    const a = publicArticles(ws, brand).find((x) => x.slug === ctx.params.aslug);
    if (!a) throw notFound("Article");
    a.views++;
    return a;
  });
  get("/v1/public/help/:slug/search", (ctx) => {
    const { ws, brand } = publicBrand(ctx);
    const q = (ctx.query.get("q") ?? "").toLowerCase().trim();
    if (!q) return [];
    return publicArticles(ws, brand)
      .filter((a) => (a.title + " " + a.excerpt + " " + a.body_html).toLowerCase().includes(q))
      .slice(0, 20);
  });

  // Public CSAT.
  const findCsat = (token: string) => {
    for (const ws of db.workspaces) {
      const c = ws.csat[token];
      if (c) return { ws, c, t: ws.tickets.find((x) => x.id === c.ticket_id)! };
    }
    throw notFound("Survey");
  };
  get("/v1/public/csat/:token", (ctx) => {
    const { ws, c, t } = findCsat(ctx.params.token);
    return { score: c.score, comment: c.comment ?? "", workspace_name: ws.workspace.name, brand_color: ws.workspace.settings.brand_color ?? "", ticket: { number: t.number, subject: t.subject }, locale: ws.workspace.settings.locale ?? "" };
  });
  post("/v1/public/csat/:token", (ctx) => {
    const { ws, c, t } = findCsat(ctx.params.token);
    const score = ctx.body.score;
    if (typeof score !== "number" || score < 1 || score > 5) throw invalid("score must be between 1 and 5");
    c.score = score;
    c.comment = (s(ctx, "comment") ?? "").slice(0, 2000);
    t.csat = { score, comment: c.comment };
    addActivity(ws, t, "csat_received", { score, comment: c.comment }, null);
    publishTicketUpdated(ws, t, ["csat"]);
    return respond(204);
  });
}

function positive(v: unknown, name: string): number {
  if (typeof v !== "number" || !Number.isFinite(v) || v <= 0) throw invalid(`${name} must be a positive number of minutes`);
  return Math.round(v);
}

function conditions(v: unknown): Condition[] {
  if (!Array.isArray(v)) return [];
  return v.filter(isRecord).map((c) => {
    if (typeof c.field !== "string" || typeof c.op !== "string" || typeof c.value !== "string") throw invalid("Each condition needs a field, operator and value");
    return { field: c.field, op: c.op, value: c.value } as Condition;
  });
}

function actions(v: unknown, requireOne: boolean): Action[] {
  const list = Array.isArray(v) ? v.filter(isRecord) : [];
  if (requireOne && !list.length) throw invalid("Add at least one action");
  return list.map((a) => {
    if (typeof a.type !== "string" || typeof a.value !== "string") throw invalid("Each action needs a type and value");
    return { type: a.type, value: a.value } as Action;
  });
}

function uniqueSlug(rows: { slug: string }[], base: string): string {
  const b = base || "untitled";
  let slug = b;
  for (let i = 2; rows.some((r) => r.slug === slug); i++) slug = `${b}-${i}`;
  return slug;
}

function recountCollections(ws: WsData) {
  for (const c of ws.collections) c.article_count = ws.articles.filter((a) => a.collection_id === c.id).length;
}

/** Ollie's email answers on a mailbox (contracts/agent.md "Email"): only while AI is on. */
function patchMailboxAI(ctx: Ctx, m: Mailbox) {
  const b = ctx.body;
  if (b.ai_answers_mode !== undefined && b.ai_answers_mode !== "draft" && b.ai_answers_mode !== "send") throw invalid("ai_answers_mode must be draft or send");
  if (b.ai_answers === true && !m.ai_answers && (aiOff() || !ctx.ws.ai.enabled)) {
    throw new HttpError(422, "ai_off", "Turn on AI in Settings > AI before Ollie answers emails.");
  }
  if (typeof b.ai_answers === "boolean") m.ai_answers = b.ai_answers;
  if (b.ai_answers_mode !== undefined) m.ai_answers_mode = b.ai_answers_mode;
  if (typeof b.ai_answers_outside_hours === "boolean") m.ai_answers_outside_hours = b.ai_answers_outside_hours;
}

const FORM_DOMAIN = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;
const PUBLIC_MAIL = new Set(["gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "msn.com", "yahoo.com", "ymail.com", "icloud.com", "me.com", "mac.com", "aol.com", "proton.me", "protonmail.com", "pm.me", "gmx.com", "gmx.net", "mail.com", "yandex.com", "yandex.ru", "zoho.com", "fastmail.com"]);

/** trusted_form_senders: exact addresses or "@domain", at most 50 (contracts/api.md "Inbound email"). */
function patchFormSenders(ctx: Ctx, m: Mailbox) {
  if (!("trusted_form_senders" in ctx.body)) return;
  const list = strArr(ctx.body.trusted_form_senders);
  if (!list) throw invalid("trusted_form_senders must be a list");
  const clean = [...new Set(list.map((a) => a.trim().toLowerCase()))];
  if (clean.length > 50) throw invalid("A mailbox can trust at most 50 form senders.");
  for (const e of clean) {
    if (e.startsWith("@")) {
      const d = e.slice(1);
      if (!FORM_DOMAIN.test(d)) throw invalid("Enter form senders as an email address or @domain.");
      if (PUBLIC_MAIL.has(d)) throw invalid(`@${d} is a public email provider: enter the form's exact address instead.`);
    } else if (!ALIAS.test(e)) throw invalid("Enter form senders as an email address or @domain.");
    if (e === m.email || m.aliases.includes(e)) throw invalid("A form sender cannot be the mailbox's own address or one of its aliases.");
  }
  m.trusted_form_senders = clean;
}

/** Mailbox passwords, write-only as in the API: the mock checks sign-ins against them. */
const mailboxSecrets = new Map<string, { password: string; smtp: string }>();

/** A seeded mailbox has working passwords stored (16 letters, which every host's check accepts). */
const SEEDED_PASSWORD = "seededapppasswrd";
function secretsOf(m: Mailbox): { password: string; smtp: string } {
  return mailboxSecrets.get(m.id) ?? { password: m.inbound_kind === "imap" ? SEEDED_PASSWORD : "", smtp: m.outbound_kind === "smtp" ? SEEDED_PASSWORD : "" };
}

const IMAP_KEYS = ["imap_host", "imap_port", "imap_username", "imap_tls"] as const;
const SMTP_KEYS = ["smtp_host", "smtp_port", "smtp_username", "smtp_tls"] as const;
const TIDY_KEYS = ["provider_mark_read", "provider_archive_on_solve", "provider_label"] as const;

/**
 * How a mailbox connects, on a PATCH: its IMAP and SMTP settings, passwords and the way
 * replies are sent, as the API checks them. With verify, a changed server is signed in
 * to first and a refusal stores nothing.
 */
function patchConnection(ctx: Ctx, m: Mailbox) {
  const b = ctx.body;
  const cfg = isRecord(b.config) ? (b.config as MailboxConfig) : {};
  const outbound = (s(ctx, "outbound_kind") ?? m.outbound_kind) as Mailbox["outbound_kind"];
  if (outbound !== m.outbound_kind && (outbound === "google" || outbound === "microsoft") && m.inbound_kind !== outbound)
    throw invalid(`Sending through ${outbound === "google" ? "Google" : "Microsoft"} needs a mailbox connected with it.`);
  const next: MailboxConfig = { ...m.config };
  const take = (keys: readonly (keyof MailboxConfig)[]) => {
    for (const k of keys) if (cfg[k] !== undefined) Object.assign(next, { [k]: cfg[k] });
  };
  if (m.inbound_kind === "imap") take(IMAP_KEYS);
  // Tidy-up in Gmail or Outlook is only for a mailbox that reads its inbox directly.
  const direct = m.config.oauth_grant === "api" && (m.inbound_kind === "google" || m.inbound_kind === "microsoft");
  if (TIDY_KEYS.some((k) => cfg[k] === true) && !direct) throw invalid("Gmail and Outlook options are only for a mailbox that reads its inbox through Google or Microsoft.");
  if (direct) for (const k of TIDY_KEYS) if (typeof cfg[k] === "boolean") next[k] = cfg[k] || undefined;
  if (outbound === "smtp") take(SMTP_KEYS);
  else for (const k of SMTP_KEYS) delete next[k];
  const prev = secretsOf(m);
  const sec = { password: (s(ctx, "password") ?? prev.password).trim(), smtp: outbound === "smtp" ? (s(ctx, "smtp_password") ?? prev.smtp).trim() : "" };
  if (outbound !== "smtp" && m.inbound_kind === "forwarding") sec.password = "";
  if (outbound === "smtp") {
    const host = (next.smtp_host ?? "").trim().toLowerCase();
    if (!host) throw invalid("Enter the SMTP server host.");
    if (/^(localhost|127\.|10\.|192\.168\.|169\.254\.)/.test(host)) throw invalid("The SMTP server must be a public address.");
    if (next.smtp_tls === "none") throw invalid("The SMTP connection must use TLS.");
    if (!sec.smtp && !sec.password) throw invalid("Enter the SMTP password.");
    next.smtp_host = host;
    next.smtp_port ||= next.smtp_tls === "tls" ? 465 : 587;
    next.smtp_username = (next.smtp_username ?? "").trim() || m.email;
  }
  const smtpChanged = outbound === "smtp" && (m.outbound_kind !== "smtp" || SMTP_KEYS.some((k) => next[k] !== m.config[k]) || sec.smtp !== prev.smtp);
  const imapChanged = m.inbound_kind === "imap" && (IMAP_KEYS.some((k) => next[k] !== m.config[k]) || sec.password !== prev.password);
  if (b.verify === true && (smtpChanged || imapChanged)) {
    const host = m.config.mail_host || detect(m.email).host || "other";
    const failed = refusal(host, imapChanged ? sec.password : sec.smtp || sec.password, !imapChanged);
    if (failed) throw new HttpError(422, "mailbox_connection_failed", `${failed.cause.title}. Nothing was saved.`, { ...failed });
  }
  m.outbound_kind = outbound;
  m.config = next;
  mailboxSecrets.set(m.id, sec);
}

/** A mailbox that stops receiving over IMAP (or directly): its IMAP settings, password and Gmail or Outlook options go, and so does a sign-in error. */
function stopImap(m: Mailbox) {
  for (const k of [...IMAP_KEYS, ...TIDY_KEYS]) delete m.config[k];
  mailboxSecrets.set(m.id, { ...secretsOf(m), password: "" });
  if (m.status === "error") {
    m.status = "active";
    m.last_error = "";
  }
  m.last_synced_at = null;
}

/**
 * How mail arrives, on a PATCH: an IMAP-receiving mailbox may switch to forwarding, which
 * keeps how its replies are sent. Receiving over IMAP is refused where the instance has it off.
 */
function patchReceiving(ctx: Ctx, m: Mailbox) {
  const kind = s(ctx, "inbound_kind");
  if (kind === undefined || kind === m.inbound_kind) return;
  if (kind === "forwarding") {
    stopImap(m);
    m.inbound_kind = "forwarding";
    return;
  }
  if (kind !== "imap" && kind !== "google" && kind !== "microsoft") throw invalid("inbound_kind must be forwarding, imap, google or microsoft");
  if (!mockImapOn()) throw imapOff();
  throw invalid(kind === "imap" ? "Add the mailbox again to receive over IMAP." : "Reconnect the mailbox to sync it.");
}

function mailbox(ws: WsData, init: Pick<Mailbox, "name" | "email" | "display_name" | "inbound_kind" | "outbound_kind" | "config" | "team_id">): Mailbox {
  const m: Mailbox = {
    ...init,
    brand_id: defaultBrand(ws)?.id ?? "",
    aliases: [],
    alias_teams: {},
    trusted_form_senders: [],
    from_name_format: "{agent} at {mailbox}",
    private: !!init.team_id && !!ws.teams.find((t) => t.id === init.team_id)?.private,
    id: uuid(),
    name: init.name.slice(0, 60),
    forwarding_address: `${ws.workspace.slug}-${slugify(init.name) || "inbox"}-${randomToken(4).toLowerCase().replace(/[-_]/g, "x")}@in.opendesk.sh`,
    signature_html: "",
    auto_reply_enabled: false,
    auto_reply_subject: "",
    auto_reply_html: "",
    ai_answers: false,
    ai_answers_mode: "draft",
    ai_answers_outside_hours: false,
    status: "active",
    last_error: "",
    last_synced_at: init.inbound_kind === "forwarding" ? null : nowIso(),
    receiving: receivingOf(init),
    sending: sendingOf(init),
    created_at: nowIso(),
  };
  ws.mailboxes.push(m);
  return m;
}

/** The workspace and brand of a help center slug (a brand's slug; the default brand's is the workspace slug). */
function publicBrand(ctx: Ctx): { ws: WsData; brand: Brand | undefined } {
  for (const ws of db.workspaces) {
    const brand = ws.brands.find((b) => b.slug === ctx.params.slug);
    if (brand) {
      if (!brand.help_center.enabled) throw notFound("Help center");
      return { ws, brand };
    }
  }
  const ws = db.workspaces.find((w) => w.workspace.slug === ctx.params.slug);
  if (!ws || !ws.workspace.settings.help_center_enabled) throw notFound("Help center");
  return { ws, brand: defaultBrand(ws) };
}

/** A KB brand_id from the body: a brand of the workspace, or null (""/null) for every brand. */
function kbBrandRef(ctx: Ctx, v: unknown): string | null {
  if (v === null || v === undefined || v === "") return null;
  if (typeof v !== "string" || !ctx.ws.brands.some((b) => b.id === v)) throw invalid("brand_id must be a brand of the workspace");
  return v;
}

/** The ?brand_id= filter of the KB lists: a brand id, null for "none" (every brand), undefined when absent. */
function brandListFilter(ctx: Ctx): string | null | undefined {
  const raw = (ctx.query.get("brand_id") ?? "").trim();
  if (!raw) return undefined;
  if (raw === "none") return null;
  if (!ctx.ws.brands.some((b) => b.id === raw)) throw invalid("brand_id must be a brand of the workspace or none");
  return raw;
}

/** An article's brand: its collection's when that has one, else its own; null = every brand. */
export function articleBrand(ws: WsData, a: KbArticle): string | null {
  const col = a.collection_id ? ws.collections.find((c) => c.id === a.collection_id) : undefined;
  return col?.brand_id ?? a.brand_id ?? null;
}

/** Whether a brand's help center shows the article: its own, or one of every brand. */
function shownBy(ws: WsData, a: KbArticle, brandId: string): boolean {
  const b = articleBrand(ws, a);
  return b === null || b === brandId;
}

/** A team id from the body that must name a team of this workspace; "" and null clear it. */
function teamRef(ctx: Ctx, v: unknown): string | null {
  if (v === null || v === undefined || v === "") return null;
  if (typeof v !== "string" || !ctx.ws.teams.some((t) => t.id === v)) throw invalid("Unknown team");
  return v;
}

function teamOfId(ws: WsData, id: string, userId: string) {
  const t = ws.teams.find((x) => x.id === id);
  return t && t.members.some((m) => m.user_id === userId) ? t : null;
}

/** Rows scoped to a private team are for its members only. */
function canSeeTeamScoped(ws: WsData, teamId: string | null | undefined): boolean {
  if (!teamId) return true;
  const t = ws.teams.find((x) => x.id === teamId);
  return !t?.private || !!teamOfId(ws, teamId, db.meId);
}

const ALIAS = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** brand_id, aliases and from_name_format on a mailbox write (contracts/teams.md). */
function writeAddressing(ctx: Ctx, m: Mailbox) {
  const b = ctx.body;
  if (typeof b.brand_id === "string" && b.brand_id) {
    if (!ctx.ws.brands.some((x) => x.id === b.brand_id)) throw invalid("Unknown brand");
    m.brand_id = b.brand_id;
  }
  if ("aliases" in b) {
    const list = strArr(b.aliases);
    if (!list) throw invalid("aliases must be a list of addresses");
    const clean = [...new Set(list.map((a) => a.trim().toLowerCase()).filter(Boolean))];
    if (clean.length > 20) throw invalid("Add at most 20 aliases");
    for (const a of clean) {
      if (!ALIAS.test(a)) throw invalid(`${a} is not an email address`);
      if (a === m.email) throw invalid("An alias can't be the mailbox's own address");
      if (ctx.ws.mailboxes.some((x) => x !== m && (x.email === a || x.aliases.includes(a)))) throw new HttpError(409, "conflict", `${a} already delivers into another mailbox`);
    }
    m.aliases = clean;
    // A removed alias takes its team with it.
    m.alias_teams = Object.fromEntries(Object.entries(m.alias_teams ?? {}).filter(([a]) => clean.includes(a)));
  }
  if ("alias_teams" in b) {
    const raw = b.alias_teams;
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw invalid("alias_teams must map aliases to team ids");
    const next: Record<string, string> = {};
    for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
      const a = k.trim().toLowerCase();
      if (!m.aliases.includes(a)) throw invalid(`alias_teams: ${a} is not an alias of this mailbox`);
      if (typeof v !== "string" || !ctx.ws.teams.some((x) => x.id === v)) throw invalid("alias_teams names a team that does not exist in this workspace");
      next[a] = v;
    }
    m.alias_teams = next;
  }
  if ("from_name_format" in b) {
    const f = typeof b.from_name_format === "string" ? b.from_name_format.replace(/\s+/g, " ").trim() : "";
    if (f.length > 80) throw invalid("Keep the from name under 80 characters");
    if (/[<>"]/.test(f)) throw invalid("The from name can't contain < > or quotes");
    const unknown = (f.match(/\{[^}]*\}/g) ?? []).find((v) => !["{agent}", "{agent_first}", "{team}", "{brand}", "{mailbox}"].includes(v));
    if (unknown) throw invalid(`${unknown} is not a variable. Use {agent}, {agent_first}, {team}, {brand} or {mailbox}`);
    m.from_name_format = f || "{agent} at {mailbox}";
  }
  m.private = !!m.team_id && !!ctx.ws.teams.find((t) => t.id === m.team_id)?.private;
  if (m.private && Object.keys(m.alias_teams ?? {}).length && ("alias_teams" in b || "team_id" in b)) {
    throw invalid("A private team's address delivers all its aliases to that team.");
  }
}

function findContact(ctx: Ctx): Contact {
  const c = ctx.ws.contacts.find((x) => x.id === ctx.params.id);
  if (!c) throw notFound("Contact");
  return c;
}
function findCompany(ctx: Ctx): Company {
  const c = ctx.ws.companies.find((x) => x.id === ctx.params.id);
  if (!c) throw notFound("Company");
  return c;
}
function findArticle(ctx: Ctx): KbArticle {
  const a = ctx.ws.articles.find((x) => x.id === ctx.params.id);
  if (!a) throw notFound("Article");
  return a;
}
function findMailbox(ctx: Ctx): Mailbox {
  const m = ctx.ws.mailboxes.find((x) => x.id === ctx.params.id);
  if (!m) throw notFound("Mailbox");
  return m;
}
