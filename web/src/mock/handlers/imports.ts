// Mock history imports (contracts/imports.md): a check that answers a made-up account
// matched against the workspace's members and teams, CSV uploads read in the browser, and
// a run that ticks forward with import.updated events, writes a few tickets, waits out a
// rate limit once and finishes. Credentials containing "bad" are refused; with "expire"
// the source stops accepting them a third of the way in.

import type {
  CsvMapping,
  CsvUpload,
  Import,
  ImportCheck,
  ImportCheckBody,
  ImportCreateBody,
  ImportOptions,
  ImportSample,
  ImportSource,
} from "@/lib/api/types";
import { contactRef, db, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, forbidden, get, invalid, notFound, post, respond, type Ctx } from "../router";
import { ensureTags, newTicket } from "../tickets";
import { nowIso, pick, uuid } from "../util";

const names: Record<ImportSource, string> = { zendesk: "Zendesk", helpscout: "Help Scout", freshdesk: "Freshdesk", intercom: "Intercom", front: "Front", csv: "CSV" };
const required: Record<Exclude<ImportSource, "csv">, string[]> = {
  zendesk: ["subdomain", "email", "token"],
  helpscout: ["app_id", "app_secret"],
  freshdesk: ["domain", "api_key"],
  intercom: ["token"],
  front: ["token"],
};

type Upload = CsvUpload & { header: string[]; data: string[][] };
type State = { imports: Import[]; uploads: Upload[]; timers: Map<string, ReturnType<typeof setInterval>>; expiring: Set<string> };
const states = new Map<string, State>();
function stateOf(ws: WsData): State {
  let s = states.get(ws.workspace.id);
  if (!s) {
    s = { imports: [], uploads: [], timers: new Map(), expiring: new Set() };
    states.set(ws.workspace.id, s);
  }
  return s;
}

function admin(ws: WsData) {
  const r = ws.members.find((m) => m.user_id === db.meId)?.role;
  if (r !== "owner" && r !== "admin") throw forbidden("Only admins can import history.");
}

function account(src: ImportSource, creds: Record<string, string>) {
  const sub = (creds.subdomain || creds.domain || "acme").replace(/\..*$/, "").trim() || "acme";
  const nice = sub.charAt(0).toUpperCase() + sub.slice(1);
  switch (src) {
    case "zendesk":
      return { name: `${nice} Support`, url: `https://${sub}.zendesk.com` };
    case "freshdesk":
      return { name: nice, url: `https://${sub}.freshdesk.com` };
    case "helpscout":
      return { name: "Acme Inc.", url: "https://secure.helpscout.net" };
    case "intercom":
      return { name: "Acme", url: "https://app.intercom.com" };
    case "front":
      return { name: "Acme", url: "https://app.frontapp.com" };
    default:
      return { name: "tickets.csv", url: "" };
  }
}

function checkCreds(src: ImportSource, creds: Record<string, string> = {}) {
  if (src === "csv") return;
  for (const f of required[src]) {
    if (!creds[f]?.trim()) {
      throw new HttpError(422, "validation_failed", `Enter the ${f.replace("_", " ")}.`, { errors: [{ location: `body.credentials.${f}`, message: "Required" }] });
    }
  }
  if (Object.values(creds).some((v) => /bad/i.test(v))) {
    throw new HttpError(422, "import_auth_failed", `${names[src]} refused these credentials. Check them and try again.`);
  }
  if (src === "zendesk" && /notfound/i.test(creds.subdomain ?? "")) {
    throw new HttpError(422, "import_not_found", "No Zendesk account answers at that address. Check it and try again.");
  }
}

const subjects = [
  "Can't log in after the update",
  "Refund for a double charge",
  "How do I export my data?",
  "Invoice address is wrong",
  "Feature request: dark mode",
  "The app crashes on start",
  "Change the owner of our account",
  "Shipping to Canada",
];
const firstLines = [
  "Hi there, since yesterday I can't sign in. It says my password is wrong but I reset it twice.",
  "I was charged twice this month. Could you refund one of them?",
  "Is there a way to get all of our records out as a spreadsheet?",
  "Our invoices still show the old address. We moved in March.",
];

function samples(ws: WsData, src: ImportSource): ImportSample[] {
  const agent = ws.members[0] ? userRef(ws.members[0].user_id) : null;
  const at = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString();
  const prefix = src === "helpscout" || src === "intercom" ? "" : "#";
  return [0, 1, 2].map((i) => ({
    ref: `${prefix}${18_420 - i * 37}`,
    subject: subjects[i]!,
    status: (["solved", "closed", "open"] as const)[i]!,
    created_at: at(40 + i * 9),
    requester: { name: ["Mia Chen", "Jonas Berg", "Priya Patel"][i]!, email: ["mia@northwind.io", "jonas@fjord.no", "priya@lumen.dev"][i]! },
    message_count: 3 + i,
    tags: [["login", "vip"], ["billing"], []][i]!,
    messages: [
      { author: ["Mia Chen", "Jonas Berg", "Priya Patel"][i]!, agent: false, note: false, text: firstLines[i]!, created_at: at(40 + i * 9), attachments: i === 0 ? 1 : 0 },
      { author: agent?.name ?? "Agent", agent: true, note: true, text: "Checked the logs: the session cookie was blocked by their proxy.", created_at: at(40 + i * 9 - 0.1), attachments: 0 },
      { author: agent?.name ?? "Agent", agent: true, note: false, text: "Thanks for writing in. I've fixed this on our side, could you try again?", created_at: at(40 + i * 9 - 0.2), attachments: 0 },
    ],
  }));
}

function check(ws: WsData, b: ImportCheckBody): ImportCheck {
  if (b.source === "csv") {
    const up = stateOf(ws).uploads.find((u) => u.upload_id === b.upload_id);
    if (!up) throw new HttpError(404, "not_found", "The uploaded file is gone. Upload it again.");
    const m = b.mapping ?? up.suggested;
    if (!m.body && !m.subject) throw invalid("Choose the column with the message, or at least the subject.");
    const tickets = m.ticket_id ? new Set(up.data.map((r) => r[up.header.indexOf(m.ticket_id!)])).size : up.data.length;
    return { account: { name: up.filename, url: "" }, totals: { tickets, articles: null }, agents: [], groups: [], samples: preview(up, m), has_articles: false };
  }
  checkCreds(b.source, b.credentials);
  const members = ws.members.map((m) => userRef(m.user_id)!).filter(Boolean);
  const agents = [
    ...members.slice(0, 4).map((u, i) => ({ id: `a${i}`, name: u.name, email: u.email, user_id: u.id })),
    { id: "a8", name: "Sam Rivera", email: "sam@oldvendor.example", user_id: null },
    { id: "a9", name: "Lee Park", email: "lee.park@example.com", user_id: null },
  ];
  const groups = [
    ...ws.teams.slice(0, 2).map((t, i) => ({ id: `g${i}`, name: t.name, team_id: t.id })),
    { id: "g8", name: "Tier 2", team_id: null },
  ];
  const hasArticles = b.source !== "front" && (b.source !== "freshdesk" || !/nokb/i.test(b.credentials?.domain ?? ""));
  return {
    account: account(b.source, b.credentials ?? {}),
    totals: { tickets: b.source === "intercom" ? null : 1_284, articles: hasArticles ? 46 : null },
    agents,
    groups,
    samples: samples(ws, b.source),
    has_articles: hasArticles,
  };
}

// ── CSV ──

function parseCsv(text: string): string[][] {
  const t = text.replace(/^\uFEFF/, "");
  const first = t.split(/\r?\n/, 1)[0] ?? "";
  const sep = [",", ";", "\t"].sort((a, b) => first.split(b).length - first.split(a).length)[0]!;
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < t.length; i++) {
    const c = t[i]!;
    if (quoted) {
      if (c === '"' && t[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (c === '"') quoted = false;
      else cell += c;
    } else if (c === '"') quoted = true;
    else if (c === sep) {
      row.push(cell);
      cell = "";
    } else if (c === "\n" || c === "\r") {
      if (c === "\r" && t[i + 1] === "\n") i++;
      row.push(cell);
      if (row.some((x) => x.trim())) rows.push(row);
      row = [];
      cell = "";
    } else cell += c;
  }
  row.push(cell);
  if (row.some((x) => x.trim())) rows.push(row);
  return rows;
}

const aliases: [keyof CsvMapping, string[]][] = [
  ["ticket_id", ["ticket id", "ticket", "conversation id", "id", "number"]],
  ["subject", ["subject", "title", "summary"]],
  ["body", ["body", "message", "description", "content", "text", "comment"]],
  ["from_email", ["from email", "email", "from", "requester email", "customer email"]],
  ["from_name", ["from name", "name", "requester name", "customer name", "requester", "customer"]],
  ["author", ["author type", "sender type", "direction", "role", "type"]],
  ["status", ["status", "state"]],
  ["priority", ["priority", "urgency"]],
  ["tags", ["tags", "labels", "tag"]],
  ["created_at", ["created at", "created", "date", "timestamp"]],
  ["assignee_email", ["assignee email", "assignee", "agent email", "assigned to"]],
  ["team", ["team", "group", "department"]],
  ["company", ["company", "organization", "organisation"]],
  ["message_id", ["message id", "rfc message id", "email message id"]],
  ["in_reply_to", ["in reply to", "parent message id"]],
  ["references", ["references"]],
];
const norm = (h: string) => h.toLowerCase().replace(/[\s_\-.]+/g, " ").trim();

function suggest(header: string[]): CsvMapping {
  const out: CsvMapping = {};
  const used = new Set<string>();
  for (const [field, list] of aliases) {
    const hit = list.map((a) => header.find((h) => norm(h) === a && !used.has(h))).find(Boolean);
    if (hit) {
      out[field] = hit;
      used.add(hit);
    }
  }
  return out;
}

function preview(up: Upload, m: CsvMapping): ImportSample[] {
  const col = (r: string[], f: keyof CsvMapping) => (m[f] ? (r[up.header.indexOf(m[f]!)] ?? "").trim() : "");
  const groups = new Map<string, string[][]>();
  up.data.forEach((r, i) => {
    const k = col(r, "ticket_id") || `row-${i}`;
    groups.set(k, [...(groups.get(k) ?? []), r]);
  });
  return [...groups.entries()].slice(0, 3).map(([k, rows]) => {
    const r0 = rows[0]!;
    const status = col(r0, "status").toLowerCase();
    return {
      ref: k.startsWith("row-") ? "" : k,
      subject: col(r0, "subject") || col(r0, "body").slice(0, 80) || "(no subject)",
      status: (["open", "pending", "on_hold", "solved", "closed"].includes(status) ? status : "closed") as ImportSample["status"],
      created_at: Date.parse(col(r0, "created_at")) ? new Date(col(r0, "created_at")).toISOString() : nowIso(),
      requester: col(r0, "from_email") || col(r0, "from_name") ? { name: col(r0, "from_name"), email: col(r0, "from_email") } : null,
      message_count: rows.length,
      tags: col(r0, "tags").split(/[,;|]/).map((x) => x.trim()).filter(Boolean),
      messages: rows.slice(0, 6).map((r) => {
        const kind = col(r, "author").toLowerCase();
        return {
          author: col(r, "from_name") || col(r, "from_email") || "Unknown",
          agent: /agent|staff|outbound|reply/.test(kind),
          note: /note|internal|private/.test(kind),
          text: col(r, "body").slice(0, 400),
          created_at: Date.parse(col(r, "created_at")) ? new Date(col(r, "created_at")).toISOString() : nowIso(),
          attachments: 0,
        };
      }),
    };
  });
}

// ── Running ──

function publishImport(ws: WsData, x: Import, created = false) {
  publish(ws, created ? "import.created" : "import.updated", { import: x });
}

function stop(ws: WsData, id: string) {
  const st = stateOf(ws);
  const t = st.timers.get(id);
  if (t) clearInterval(t);
  st.timers.delete(id);
}

/** Moves a running import forward until it is done, paused or canceled. */
function drive(ws: WsData, x: Import) {
  stop(ws, x.id);
  let waited = x.run > 1;
  const contacts = ws.contacts.filter((c) => c.email);
  const timer = setInterval(() => {
    if (x.status !== "running") return stop(ws, x.id);
    const now = Date.now();
    if (x.waiting_until && Date.parse(x.waiting_until) > now) return;
    if (x.waiting_until) {
      x.waiting_until = null;
      publishImport(ws, x);
    }
    if (x.phase === "articles") {
      const total = x.totals.articles ?? 40;
      x.counts.articles = Math.min(total, x.counts.articles + 9);
      if (x.counts.articles >= total) x.phase = x.options.tickets ? "tickets" : "done";
    } else {
      const total = x.run > 1 ? 12 : (x.totals.tickets ?? 900);
      const done = x.run > 1 ? x.counts.updated : x.counts.tickets;
      const step = Math.max(1, Math.round(total / 18));
      const n = Math.min(step, total - done);
      if (x.run > 1) x.counts.updated += n;
      else x.counts.tickets += n;
      x.counts.messages += n * 4;
      x.counts.contacts += Math.ceil(n * 0.6);
      x.counts.attachments += x.options.attachments ? Math.floor(n * 0.4) : 0;
      // A few real tickets so the inbox has something to show.
      for (let i = 0; i < Math.min(2, n) && contacts.length; i++) {
        const c = pick(contacts);
        const at = new Date(now - (60 + Math.random() * 300) * 86_400_000).toISOString();
        const tags = x.options.tag ? ensureTags(ws, [x.options.tag], false) : [];
        newTicket(ws, { subject: pick(subjects), contact: contactRef(c), channel: "email", status: x.options.statuses === "closed" ? "closed" : pick(["solved", "closed", "closed"] as const), tags, created_at: at });
      }
      if (stateOf(ws).expiring.has(x.id) && done + n > total * 0.3) {
        stateOf(ws).expiring.delete(x.id);
        x.status = "paused";
        x.needs_credentials = true;
        x.last_error = "Zendesk answered 401 Unauthorized.";
        x.updated_at = nowIso();
        publishImport(ws, x);
        return stop(ws, x.id);
      }
      if (!waited && done + n > total * 0.45) {
        waited = true;
        x.waiting_until = new Date(now + 7_000).toISOString();
      }
      if (done + n === total && x.counts.failed === 0 && x.run === 1) {
        x.counts.skipped += 2;
        x.problems = [
          { at: nowIso(), item: "Ticket #18011", message: "An attachment over 25 MB was left out." },
          { at: nowIso(), item: "Ticket #17533", message: "It had no messages, so it was left out." },
        ];
      }
      if (done + n >= total) x.phase = "done";
    }
    if (x.phase === "done") {
      x.status = "completed";
      x.finished_at = nowIso();
      stop(ws, x.id);
    }
    x.updated_at = nowIso();
    publishImport(ws, x);
  }, 650);
  stateOf(ws).timers.set(x.id, timer);
}

function find(ctx: Ctx): Import {
  const x = stateOf(ctx.ws).imports.find((i) => i.id === ctx.params.id);
  if (!x) throw notFound("Import");
  return x;
}

function change(ctx: Ctx, fn: (x: Import) => void): Import {
  admin(ctx.ws);
  const x = find(ctx);
  fn(x);
  x.updated_at = nowIso();
  publishImport(ctx.ws, x);
  return x;
}

const busy = () => new HttpError(409, "import_running", "Another import is in progress. Finish, cancel or undo it first.");
const active = (ws: WsData) => stateOf(ws).imports.some((i) => i.status === "running" || i.status === "paused" || i.status === "undoing");

export function registerImports() {
  get("/v1/imports", (ctx) => {
    admin(ctx.ws);
    return { data: stateOf(ctx.ws).imports };
  });
  get("/v1/imports/:id", (ctx) => {
    admin(ctx.ws);
    return find(ctx);
  });

  post("/v1/imports/check", async (ctx) => {
    admin(ctx.ws);
    await new Promise((r) => setTimeout(r, 900));
    return check(ctx.ws, ctx.body as unknown as ImportCheckBody);
  });

  post("/v1/imports/csv", async (ctx) => {
    admin(ctx.ws);
    const f = ctx.form?.get("file");
    if (!(f instanceof File)) throw invalid("file is required");
    if (!/\.(csv|tsv|txt)$/i.test(f.name)) throw new HttpError(422, "file_type_refused", "Upload a .csv file. Spreadsheets can save one with File, then Download or Export, as CSV.");
    const all = parseCsv(await f.text());
    const [header, ...rows] = all;
    if (!header || !rows.length) throw new HttpError(422, "csv_unreadable", "The file has a header row but no rows under it.");
    const up: Upload = {
      upload_id: uuid(),
      filename: f.name,
      rows: rows.length,
      columns: header.map((name, i) => ({ name, samples: rows.map((r) => (r[i] ?? "").trim()).filter(Boolean).slice(0, 3).map((v) => v.slice(0, 80)) })),
      suggested: suggest(header),
      preview: [],
      header,
      data: rows,
    };
    up.preview = preview(up, up.suggested);
    stateOf(ctx.ws).uploads.push(up);
    const { header: _h, data: _d, ...out } = up;
    return respond(201, out);
  });

  post("/v1/imports", (ctx) => {
    admin(ctx.ws);
    const b = ctx.body as unknown as ImportCreateBody;
    if (active(ctx.ws)) throw busy();
    const c = check(ctx.ws, { source: b.source, credentials: b.credentials, upload_id: b.upload_id, mapping: b.mapping?.csv });
    const opts: ImportOptions = b.options ?? {
      tickets: true,
      articles: b.source !== "csv",
      attachments: true,
      since: null,
      mailbox_id: null,
      tag: `${b.source}-import`,
      statuses: "keep",
      articles_as_drafts: false,
    };
    if (!c.has_articles) opts.articles = false;
    const now = nowIso();
    const x: Import = {
      id: uuid(),
      source: b.source,
      account: c.account,
      status: "running",
      phase: opts.articles ? "articles" : "tickets",
      run: 1,
      options: opts,
      mapping: b.mapping ?? { agents: {}, groups: {} },
      totals: { tickets: opts.tickets ? c.totals.tickets : null, articles: opts.articles ? c.totals.articles : null },
      counts: { tickets: 0, updated: 0, messages: 0, contacts: 0, companies: 0, articles: 0, attachments: 0, skipped: 0, failed: 0 },
      problems: [],
      last_error: "",
      needs_credentials: false,
      waiting_until: null,
      can_sync: b.source !== "csv",
      started_by: userRef(db.meId),
      started_at: now,
      finished_at: null,
      created_at: now,
      updated_at: now,
    };
    const st = stateOf(ctx.ws);
    st.imports.unshift(x);
    if (b.source === "csv") st.uploads = st.uploads.filter((u) => u.upload_id !== b.upload_id);
    if (Object.values(b.credentials ?? {}).some((v) => /expire/i.test(v))) st.expiring.add(x.id);
    publishImport(ctx.ws, x, true);
    drive(ctx.ws, x);
    return respond(201, x);
  });

  post("/v1/imports/:id/pause", (ctx) =>
    change(ctx, (x) => {
      if (x.status !== "running") throw new HttpError(409, "conflict", "Only a running import can be paused.");
      x.status = "paused";
      x.waiting_until = null;
      stop(ctx.ws, x.id);
    }),
  );
  post("/v1/imports/:id/resume", (ctx) => {
    const creds = (ctx.body as { credentials?: Record<string, string> }).credentials;
    const x = change(ctx, (x) => {
      if (x.status !== "paused" && x.status !== "failed") throw new HttpError(409, "conflict", "Only a paused or failed import can be resumed.");
      // Empty fields keep their stored value; only a refused secret is checked here.
      if (creds && Object.values(creds).some((v) => /bad/i.test(v))) throw new HttpError(422, "import_auth_failed", `${names[x.source]} refused these credentials. Check them and try again.`);
      x.status = "running";
      x.needs_credentials = false;
      x.last_error = "";
      x.finished_at = null;
    });
    drive(ctx.ws, x);
    return x;
  });
  post("/v1/imports/:id/cancel", (ctx) =>
    change(ctx, (x) => {
      if (x.status !== "running" && x.status !== "paused") throw new HttpError(409, "conflict", "Only a running or paused import can be canceled.");
      x.status = "canceled";
      x.can_sync = false;
      x.finished_at = nowIso();
      stop(ctx.ws, x.id);
    }),
  );
  post("/v1/imports/:id/sync", (ctx) => {
    if (active(ctx.ws)) throw busy();
    const x = change(ctx, (x) => {
      if (x.status !== "completed" || !x.can_sync) throw new HttpError(409, "conflict", "Only a completed import that still has its credentials can bring over changes.");
      x.status = "running";
      x.run += 1;
      x.phase = "tickets";
      x.started_at = nowIso();
      x.finished_at = null;
    });
    drive(ctx.ws, x);
    return x;
  });
  post("/v1/imports/:id/undo", (ctx) => {
    const x = change(ctx, (x) => {
      if (x.status === "running") throw new HttpError(409, "conflict", "Pause or cancel the import before undoing it.");
      x.status = "undoing";
      x.can_sync = false;
    });
    setTimeout(() => {
      const tag = x.options.tag;
      if (tag) ctx.ws.tickets = ctx.ws.tickets.filter((t) => !t.tags.some((g) => g.name === tag));
      x.status = "undone";
      x.finished_at = nowIso();
      x.updated_at = nowIso();
      publishImport(ctx.ws, x);
    }, 2500);
    return x;
  });
  del("/v1/imports/:id/credentials", (ctx) =>
    change(ctx, (x) => {
      if (x.status === "running") throw new HttpError(409, "conflict", "Pause the import before removing its credentials.");
      x.can_sync = false;
    }),
  );
}
