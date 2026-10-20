// The seed: a believable opendesk instance. Margin and its team (the app mock's
// world) among sixty customer workspaces on every plan, about two hundred people,
// the operators who run it, test workspaces, background jobs and three weeks of
// the admin audit log. Deterministic: times come from NOW, choices from the
// seeded PRNG.

import type { AdminJob, AdminMailbox, AuditLog, JobState, Role, SampleKind, SampleSize } from "@/lib/api/types";
import { logoMark, portrait } from "./avatars";
import { AI_FEATURES, FIRST, JOB_KINDS, LAST, MARGIN_TEAM, NAMED, OPERATORS, OTHERS, USER_AGENTS, type NamedWorkspace, type OperatorKey } from "./content";
import { db, membersOf, type AdminAuditRec, type UserRec, type WorkspaceRec } from "./db";
import { batchCounts, addCounts } from "./samples";
import { billedSeats, entitlements } from "./shapes";
import { DAY, HOUR, MIN, NOW, ago, chance, cents, int, iso, micro, pick, rand, reseed, slugify, uuid } from "./util";

const ascii = (s: string) => slugify(s).replace(/-/g, "");

function token(prefix: string, len: number) {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  return prefix + Array.from({ length: len }, () => chars[Math.floor(rand() * chars.length)]).join("");
}

/** An address in a documentation range (RFC 5737), stable per person. */
function ipFor(i: number) {
  const nets = ["192.0.2", "198.51.100", "203.0.113"];
  return `${nets[i % 3]}.${(i * 37) % 250 + 3}`;
}

type PersonOpts = Partial<Pick<UserRec, "email_verified" | "totp_enabled" | "has_password" | "last_seen_at" | "test_account" | "password_expires_at">> & {
  createdMs: number;
  photo?: boolean;
  google?: boolean;
  sessions?: number;
};

function person(name: string, email: string, o: PersonOpts): UserRec {
  const created = ago(o.createdMs);
  const google = o.google ?? chance(0.18);
  const u: UserRec = {
    id: uuid(),
    email,
    name,
    avatar_url: (o.photo ?? chance(0.35)) ? portrait(email) : null,
    email_verified: o.email_verified ?? true,
    totp_enabled: o.totp_enabled ?? chance(0.22),
    has_password: o.has_password ?? !(google && chance(0.5)),
    suspended_at: null,
    suspended_reason: "",
    last_seen_at: o.last_seen_at !== undefined ? o.last_seen_at : ago(Math.min(o.createdMs, chance(0.7) ? int(2, 3 * 24 * 60) * MIN : int(4, 60) * DAY)),
    created_at: created,
    test_account: o.test_account ?? false,
    password_expires_at: o.password_expires_at ?? null,
    identities: google ? [{ provider: "google", email, created_at: created }] : [],
    passkeys: chance(0.12) ? int(1, 2) : 0,
    sessions: [],
  };
  const n = o.sessions ?? (u.last_seen_at && Date.parse(u.last_seen_at) > NOW - 14 * DAY ? int(1, 3) : 0);
  for (let i = 0; i < n; i++) {
    const a = pick(USER_AGENTS);
    const used = i === 0 && u.last_seen_at ? u.last_seen_at : ago(int(1, 20) * DAY);
    u.sessions.push({
      id: uuid(),
      client: a.client,
      ip: ipFor(db.users.length * 3 + i),
      user_agent: a.ua,
      mfa_verified: u.totp_enabled,
      created_at: ago(Math.min(o.createdMs, NOW - Date.parse(used) + int(1, 30) * DAY)),
      last_used_at: used,
    });
  }
  db.users.push(u);
  return u;
}

/** Names the flows look up stay unique. */
const RESERVED = new Set(["Dana Whitfield", "Victor Hale", "Rex Dalton"]);

function randomName(): string {
  for (;;) {
    const n = `${pick(FIRST)} ${pick(LAST)}`;
    if (!RESERVED.has(n)) return n;
  }
}

function emailAt(name: string, domain: string): string {
  const [first, ...rest] = name.split(" ");
  const local = chance(0.5) ? ascii(first) : `${ascii(first)}.${ascii(rest.join(""))}`;
  let email = `${local}@${domain}`;
  while (db.users.some((u) => u.email === email)) email = `${ascii(first)}.${ascii(rest.join(""))}${int(2, 99)}@${domain}`;
  return email;
}

function join(w: WorkspaceRec, u: UserRec, role: Role, createdMs: number) {
  db.members.push({ workspace_id: w.id, user_id: u.id, role, created_at: ago(createdMs) });
}

function emptyWorkspace(name: string, createdMs: number): WorkspaceRec {
  let slug = slugify(name);
  while (db.workspaces.some((w) => w.slug === slug)) slug = `${slugify(name)}-${int(2, 99)}`;
  return {
    id: uuid(),
    name,
    slug,
    avatar_url: null,
    created_at: ago(createdMs),
    last_activity_at: null,
    suspended_at: null,
    suspended_reason: "",
    sub: { plan: "free", status: "none", interval: "month", seats: 1, current_period_end: null, cancel_at_period_end: false, stripe_customer_id: null, stripe_subscription_id: null },
    limits: { max_mailboxes: null, max_members: null, monthly_ai_usd: null },
    collab: { access: "none", until: null, note: "" },
    collab_accounts: 0,
    counts: { tickets: 0, open_tickets: 0, contacts: 0, kb_articles: 0, api_keys: 0, webhooks: 0, brands: 1 },
    pending_invitations: 0,
    mailboxes: [],
    ai: { included_used_usd: 0, purchased_usd: 0, by_feature: [] },
    audit: [],
    test: null,
  };
}

/** A paid subscription: seats are the billed members, renewing within the interval. */
function subscribe(w: WorkspaceRec, plan: "pro" | "collab", status: string, interval: "month" | "year" = "month", extra: { cancel?: boolean; trialDays?: number } = {}) {
  const span = interval === "year" ? 365 : 30;
  w.sub = {
    plan,
    status,
    interval,
    seats: billedSeats(w),
    current_period_end:
      status === "trialing" ? iso(NOW + (extra.trialDays ?? 7) * DAY) : status === "canceled" ? ago(int(20, 60) * DAY) : iso(NOW + int(2, span - 1) * DAY + int(0, 23) * HOUR),
    cancel_at_period_end: !!extra.cancel,
    stripe_customer_id: token("cus_", 14),
    stripe_subscription_id: token("sub_1", 23),
  };
}

function mailbox(domain: string, m: NamedWorkspace["mailboxes"][number], createdMs: number): AdminMailbox {
  const status = m.error ? "error" : m.disabled ? "disabled" : "active";
  return {
    id: uuid(),
    name: m.name,
    email: `${m.local}@${domain}`,
    inbound_kind: m.inbound,
    outbound_kind: m.outbound,
    status,
    last_error: m.error ?? "",
    last_synced_at: status === "error" ? ago(2 * DAY + 3 * HOUR) : status === "disabled" ? null : ago(int(1, 9) * MIN),
    created_at: ago(createdMs),
  };
}

/** This period's AI calls by feature, adding up to about `total` dollars charged. */
function aiUsage(w: WorkspaceRec, total: number, purchased: number) {
  w.ai.by_feature = [];
  if (total > 0) {
    for (const [feature, perCall, share] of AI_FEATURES) {
      if (total < 1 && share < 0.2) continue;
      const charged = cents(total * share * (0.8 + rand() * 0.4));
      if (charged <= 0) continue;
      w.ai.by_feature.push({ feature, charged_usd: charged, provider_cost_usd: micro(charged * 0.8), calls: Math.max(1, Math.round(charged / perCall)) });
    }
  }
  const spent = w.ai.by_feature.reduce((n, f) => n + f.charged_usd, 0);
  w.ai.included_used_usd = micro(Math.min(spent, entitlements(w).monthly_ai_usd));
  w.ai.purchased_usd = purchased;
}

function counts(w: WorkspaceRec, tickets: number) {
  w.counts = {
    tickets,
    open_tickets: Math.round(tickets * (0.05 + rand() * 0.08)),
    contacts: Math.round(tickets * (0.45 + rand() * 0.3)),
    kb_articles: tickets > 300 ? int(8, 64) : int(0, 6),
    api_keys: tickets > 500 ? int(1, 4) : int(0, 1),
    webhooks: tickets > 500 ? int(0, 3) : 0,
    brands: tickets > 2000 ? int(1, 3) : 1,
  };
  w.pending_invitations = chance(0.3) ? int(1, 3) : 0;
}

/** A few entries of the workspace's own audit log, by its members. */
function workspaceAudit(w: WorkspaceRec, domain: string) {
  const members = membersOf(w.id);
  const ref = (uid: string) => {
    const u = db.users.find((x) => x.id === uid)!;
    return { id: u.id, name: u.name, email: u.email, avatar_url: u.avatar_url };
  };
  const owner = members.find((m) => m.role === "owner");
  const actors = members.filter((m) => m.role === "owner" || m.role === "admin");
  const span = Math.min(NOW - Date.parse(w.created_at), 60 * DAY);
  const n = int(3, 9);
  const rows: AuditLog[] = [];
  for (let i = 0; i < n; i++) {
    const by = pick(actors.length ? actors : owner ? [owner] : members);
    if (!by) break;
    const [action, entity_type, data] = pick<[string, string, Record<string, unknown>]>([
      ["member.invited", "invitation", { email: `${pick(FIRST).toLowerCase()}@${domain}`, role: "agent" }],
      ["mailbox.connected", "mailbox", { email: w.mailboxes[0]?.email ?? `support@${domain}` }],
      ["api_key.created", "api_key", { name: pick(["Zapier", "Data warehouse export", "Status page", "CRM sync"]) }],
      ["settings.updated", "workspace", { fields: [pick(["timezone", "business_hours", "csat_enabled", "help_center_title"])] }],
      ["webhook.created", "webhook", { url: `https://hooks.${domain}/opendesk` }],
      ["member.role_changed", "member", { from: "agent", to: "admin" }],
      ["2fa.enabled", "user", {}],
      ["sla_policy.updated", "sla_policy", { name: "Standard" }],
    ]);
    rows.push({
      id: uuid(),
      actor: { type: "user", user: ref(by.user_id), api_key_id: null },
      action,
      entity_type,
      entity_id: uuid(),
      data,
      ip: ipFor(i + 7),
      created_at: ago(int(1, Math.max(2, Math.floor(span / HOUR))) * HOUR),
    });
  }
  w.audit = rows.sort((a, b) => b.created_at.localeCompare(a.created_at));
}

const ops = {} as Record<OperatorKey, UserRec>;

function seedOperators() {
  for (const [i, o] of OPERATORS.entries()) {
    const u = person(o.name, o.email, {
      createdMs: o.days * DAY,
      totp_enabled: o.totp,
      photo: o.key === "ava" || o.key === "noah" || o.key === "hana",
      google: o.key === "noah",
      has_password: true,
      last_seen_at: o.key === "ben" ? ago(3 * DAY) : ago((i + 1) * 7 * MIN),
      sessions: o.key === "ben" ? 1 : 2,
    });
    ops[o.key] = u;
  }
  const grant = (k: OperatorKey, role: "viewer" | "support" | "super", by: OperatorKey | null, days: number) =>
    db.admins.push({ user_id: ops[k].id, role, granted_by: by ? ops[by].id : null, created_at: ago(days * DAY) });
  grant("ava", "super", null, 410);
  grant("noah", "super", "ava", 290);
  grant("kofi", "support", "ava", 200);
  grant("elena", "viewer", "ava", 150);
  grant("hana", "support", "noah", 18);
}

function seedNamed() {
  for (const def of NAMED) {
    const createdMs = def.days * DAY + int(1, 20) * HOUR;
    const w = emptyWorkspace(def.name, createdMs);
    db.workspaces.push(w);
    if (def.logo) w.avatar_url = logoMark(def.name);

    if (def.name === "Margin") {
      for (const [i, p] of MARGIN_TEAM.entries()) {
        const u = person(p.name, p.email, {
          createdMs: createdMs - i * 9 * DAY,
          photo: i === 0 || i === 1 || i === 4,
          google: i === 1 || i === 4,
          has_password: i !== 4,
          totp_enabled: i === 1,
          last_seen_at: ago((i + 1) * 4 * MIN),
        });
        join(w, u, p.role, createdMs - i * 9 * DAY);
      }
    } else {
      const ownerName = def.owner ?? randomName();
      const owner = person(ownerName, emailAt(ownerName, def.domain), { createdMs, totp_enabled: def.name === "Halcyon Studio" ? true : undefined });
      join(w, owner, "owner", createdMs);
      for (let i = 0; i < def.team; i++) {
        const name = randomName();
        const joined = Math.max(createdMs - int(1, Math.max(1, def.days - 1)) * DAY, HOUR);
        const role: Role = i < Math.ceil(def.team / 6) ? "admin" : i >= def.team - Math.floor(def.team / 5) ? "viewer" : "agent";
        join(w, person(name, emailAt(name, def.domain), { createdMs: joined }), role, joined);
      }
    }

    w.mailboxes = def.mailboxes.map((m, i) => mailbox(def.domain, m, createdMs - i * 3 * DAY));
    if (def.sub.plan !== "free") subscribe(w, def.sub.plan, def.sub.status, def.sub.interval, { cancel: def.sub.cancel, trialDays: def.sub.trialDays });
    if (def.limits) w.limits = { ...w.limits, ...def.limits };
    if (def.collab) {
      w.collab = { access: def.collab.access, until: def.collab.days ? iso(NOW + def.collab.days * DAY) : null, note: def.collab.note };
      w.collab_accounts = def.collab.accounts ?? 0;
    }
    counts(w, def.tickets);
    aiUsage(w, def.ai, def.purchased ?? 0);
    w.last_activity_at = def.sub.status === "canceled" ? ago(38 * DAY) : ago(int(1, 50) * MIN);
    if (def.suspended) {
      w.suspended_at = ago(def.suspended.days * DAY + 2 * HOUR);
      w.suspended_reason = def.suspended.reason;
      w.last_activity_at = w.suspended_at;
    }
    workspaceAudit(w, def.domain);
  }

  // Dana runs Halcyon and helps Paperplane; Victor is suspended with his workspace.
  const dana = db.users.find((u) => u.name === "Dana Whitfield")!;
  const paperplane = db.workspaces.find((w) => w.name === "Paperplane")!;
  join(paperplane, dana, "admin", 40 * DAY);
  const victor = db.users.find((u) => u.name === "Victor Hale")!;
  victor.suspended_at = ago(9 * DAY + HOUR);
  victor.suspended_reason = "Owner of Quick Gift Rewards, suspended for phishing (abuse ticket 4471).";
  victor.sessions = [];
  victor.last_seen_at = ago(9 * DAY + 3 * HOUR);
}

function seedOthers() {
  for (const [i, [name, plan, team]] of OTHERS.entries()) {
    const days = i % 7 === 3 ? int(1, 28) : int(30, 400);
    const createdMs = days * DAY + int(1, 20) * HOUR;
    const w = emptyWorkspace(name, createdMs);
    db.workspaces.push(w);
    const domain = `${ascii(name)}.com`;
    const ownerName = randomName();
    join(w, person(ownerName, emailAt(ownerName, domain), { createdMs }), "owner", createdMs);
    for (let j = 0; j < team; j++) {
      const n = randomName();
      const joined = Math.max(createdMs - int(1, Math.max(1, days - 1)) * DAY, HOUR);
      join(w, person(n, emailAt(n, domain), { createdMs: joined }), j === 0 && team > 2 ? "admin" : "agent", joined);
    }
    w.mailboxes = [mailbox(domain, pick([{ name: "Support", local: "support", inbound: "gmail", outbound: "gmail" }, { name: "Help", local: "help", inbound: "microsoft", outbound: "microsoft" }, { name: "Hello", local: "hello", inbound: "forward", outbound: "smtp" }]), createdMs)];
    if (plan === "pro") subscribe(w, "pro", chance(0.1) ? "past_due" : "active", chance(0.25) ? "year" : "month");
    counts(w, plan === "pro" ? int(200, 2400) : int(0, 180));
    aiUsage(w, plan === "pro" ? cents(2 + rand() * 30) : chance(0.6) ? cents(rand() * 0.5) : 0, 0);
    w.last_activity_at = chance(0.75) ? ago(int(5, 72 * 60) * MIN) : ago(int(8, 90) * DAY);
    workspaceAudit(w, domain);
  }
}

/** People with an account and no workspace: recent signups, some unconfirmed, and one suspended. */
function seedLoners() {
  for (let i = 0; i < 14; i++) {
    const name = randomName();
    const recent = i < 6;
    const domain = pick(["gmail.example", "outlook.example", "proton.example", "fastmail.example"]);
    person(name, emailAt(name, domain), {
      createdMs: recent ? int(1, 9 * 24) * HOUR : int(30, 300) * DAY,
      email_verified: !recent || i % 2 === 0,
      last_seen_at: recent && i % 2 === 1 ? null : undefined,
      sessions: recent && i % 2 === 1 ? 0 : undefined,
    });
  }
  const rex = person("Rex Dalton", "rex.dalton@mailbox.example", { createdMs: 26 * DAY, email_verified: true, totp_enabled: false });
  rex.suspended_at = ago(17 * DAY);
  rex.suspended_reason = "Signed up 14 accounts from one address to resell the free AI allowance.";
  rex.sessions = [];
  rex.last_seen_at = ago(17 * DAY + 2 * HOUR);
}

type TestDef = {
  name: string;
  by: OperatorKey | null;
  days: number;
  deleteAfter: number | null;
  plan: "free" | "pro" | "collab";
  reason: string;
  owner: { testEmail: string; name: string; passwordFor: number } | { existing: () => UserRec };
  batches: { size: SampleSize; kinds: SampleKind[] }[];
  failed?: string;
};

function seedTestWorkspaces() {
  const northwind = db.workspaces.find((w) => w.name === "Northwind Traders")!;
  const nwAdmin = membersOf(northwind.id).find((m) => m.role === "admin")!;
  const all: SampleKind[] = ["tickets", "chat", "kb", "team", "collab", "history"];
  const defs: TestDef[] = [
    {
      name: "Reports preview",
      by: null,
      days: 30,
      deleteAfter: 90,
      plan: "pro",
      reason: "Screenshots of the new reports for the docs",
      owner: { existing: () => ops.hana },
      batches: [{ size: "small", kinds: ["tickets", "history"] }],
    },
    {
      name: "Widget QA",
      by: "hana",
      days: 25,
      deleteAfter: 30,
      plan: "free",
      reason: "Checking the chat widget on a free workspace",
      owner: { testEmail: "owner-q4wd@widget-qa.invalid", name: "Widget tester", passwordFor: -23 * DAY },
      batches: [],
    },
    {
      name: "Acme demo",
      by: "kofi",
      days: 18,
      deleteAfter: 30,
      plan: "pro",
      reason: "Sales demo for Acme on Thursday",
      owner: { testEmail: "owner-k7m2@acme-demo.invalid", name: "Alex Morgan", passwordFor: 30 * HOUR },
      batches: [{ size: "realistic", kinds: all }],
    },
    {
      name: "Northwind pilot",
      by: "ava",
      days: 40,
      deleteAfter: null,
      plan: "collab",
      reason: "Collab pilot with Northwind's support leads",
      owner: { existing: () => db.users.find((u) => u.id === nwAdmin.user_id)! },
      batches: [
        { size: "realistic", kinds: ["tickets", "collab"] },
        { size: "small", kinds: ["team", "collab"] },
      ],
    },
    {
      name: "Load test, large",
      by: "noah",
      days: 6,
      deleteAfter: 7,
      plan: "free",
      reason: "Long lists and reports at a year of data",
      owner: { testEmail: "owner-l9tx@load-test-large.invalid", name: "Load tester", passwordFor: 20 * HOUR },
      batches: [{ size: "large", kinds: all }],
      failed: "sample: insert tickets: context deadline exceeded",
    },
  ];
  for (const d of defs.sort((a, b) => b.days - a.days)) {
    const createdMs = d.days * DAY + int(1, 6) * HOUR;
    const w = emptyWorkspace(d.name, createdMs);
    db.workspaces.push(w);
    let owner: UserRec;
    if ("existing" in d.owner) owner = d.owner.existing();
    else {
      owner = person(d.owner.name, d.owner.testEmail, {
        createdMs,
        photo: false,
        google: false,
        totp_enabled: false,
        has_password: true,
        test_account: true,
        // How long the password still works (negative: it has run out); at most 2 days from its last renewal.
        password_expires_at: iso(NOW + d.owner.passwordFor),
        last_seen_at: d.name === "Widget QA" ? ago(12 * DAY) : ago(int(2, 30) * HOUR),
      });
    }
    join(w, owner, "owner", createdMs);
    const expires = d.deleteAfter === null ? null : iso(NOW - createdMs + d.deleteAfter * DAY);
    w.test = {
      expires_at: expires,
      reason: d.reason,
      plan: d.plan,
      created_by: d.by ? ops[d.by].id : null,
      sample: { status: "none", size: "", kinds: [], counts: null, batches: 0, started_at: null, seeded_at: null, error: "" },
    };
    for (const [i, b] of d.batches.entries()) {
      const started = ago(createdMs - i * 2 * DAY - MIN);
      const last = i === d.batches.length - 1;
      const failed = last && d.failed;
      const c = batchCounts(b.size, b.kinds);
      w.test.sample = {
        status: failed ? "failed" : "done",
        size: b.size,
        kinds: b.kinds,
        counts: failed ? w.test.sample.counts : addCounts(w.test.sample.counts, c),
        batches: i + 1,
        started_at: started,
        seeded_at: failed ? w.test.sample.seeded_at : ago(createdMs - i * 2 * DAY - 4 * MIN),
        error: failed ? d.failed! : "",
      };
      if (!failed) {
        w.counts.tickets += c.tickets;
        w.counts.open_tickets += Math.round(c.tickets * 0.35);
        w.counts.contacts += c.contacts;
        w.counts.kb_articles += c.articles;
        w.collab_accounts += c.mail_accounts;
        for (let j = 0; j < c.members; j++) {
          const n = randomName();
          const u = person(n, `${ascii(n)}@${slugify(d.name)}.invalid`, { createdMs: createdMs - MIN, test_account: true, has_password: false, totp_enabled: false, google: false, photo: false, last_seen_at: null, sessions: 0 });
          join(w, u, j === 0 ? "admin" : "agent", createdMs - MIN);
        }
      }
    }
    if (d.plan === "pro") {
      w.limits = { max_mailboxes: 0, max_members: 0, monthly_ai_usd: 20 };
      aiUsage(w, w.test.sample.counts ? cents(3 + rand() * 6) : 0, 50);
    } else if (d.plan === "collab") {
      w.collab = { access: "complimentary", until: expires, note: "Test workspace" };
      aiUsage(w, cents(4 + rand() * 4), 0);
    }
    w.last_activity_at = ago(int(1, 48) * HOUR);
  }
}

function seedJobs() {
  type Spec = { kind: string; state: JobState; createdMs: number; attempt: number; error?: string; scheduledInMs?: number; finalizedAgoMs?: number; args?: Record<string, unknown> };
  const ws = db.workspaces.filter((w) => !w.test);
  const wid = () => pick(ws).id;
  const halcyon = db.workspaces.find((w) => w.name === "Halcyon Studio")!;
  const loadTest = db.workspaces.find((w) => w.name === "Load test, large")!;
  const argsFor = (kind: string): Record<string, unknown> => {
    switch (kind) {
      case "email.send_reply":
        return { workspace_id: wid(), ticket_id: uuid(), message_id: uuid(), mailbox_id: uuid(), subject: "[redacted]", html: "[redacted]" };
      case "mail.system":
        return { template: pick(["verify_email", "password_reset", "invitation", "two_factor_disabled"]), to: pick(db.users).email, subject: "[redacted]", html: "[redacted]", text: "[redacted]" };
      case "webhook.deliver":
        return { webhook_id: uuid(), delivery_id: uuid(), event: pick(["ticket.created", "ticket.updated", "message.created"]), url: "https://hooks.copperleaf.dev/opendesk" };
      case "mailbox.sync":
        return { workspace_id: halcyon.id, mailbox_id: halcyon.mailboxes[0].id };
      case "collab.sync":
        return { workspace_id: wid(), account_id: uuid(), folder: "INBOX" };
      case "inbound.process":
        return { inbound_id: uuid(), mailbox_id: uuid(), size_bytes: int(4_000, 2_400_000) };
      case "ai.triage":
        return { workspace_id: wid(), ticket_id: uuid(), message_id: uuid() };
      case "ai.embed_article":
        return { workspace_id: wid(), article_id: uuid(), content: "[redacted]" };
      case "automation.run":
        return { workspace_id: wid(), automation_id: uuid(), ticket_id: uuid(), trigger: "ticket.updated" };
      case "billing.sync_seats":
        return { workspace_id: wid() };
      case "notification.push":
        return { user_id: pick(db.users).id, device_id: uuid(), title: "New reply", body: "[redacted]" };
      case "admin.seed_test_workspace":
        return { workspace_id: loadTest.id, size: "large", kinds: ["tickets", "chat", "kb", "team", "collab", "history"], batch: 1 };
      default:
        return {};
    }
  };
  const specs: Spec[] = [
    // Failing, waiting to try again.
    { kind: "email.send_reply", state: "retryable", createdMs: 38 * MIN, attempt: 3, error: JOB_KINDS["email.send_reply"].errors[0], scheduledInMs: 6 * MIN },
    { kind: "email.send_reply", state: "retryable", createdMs: 2 * HOUR, attempt: 6, error: JOB_KINDS["email.send_reply"].errors[1], scheduledInMs: 41 * MIN },
    { kind: "webhook.deliver", state: "retryable", createdMs: 3 * HOUR, attempt: 5, error: JOB_KINDS["webhook.deliver"].errors[0], scheduledInMs: 22 * MIN },
    { kind: "webhook.deliver", state: "retryable", createdMs: 50 * MIN, attempt: 2, error: JOB_KINDS["webhook.deliver"].errors[1], scheduledInMs: 3 * MIN },
    { kind: "mailbox.sync", state: "retryable", createdMs: 2 * DAY, attempt: 14, error: JOB_KINDS["mailbox.sync"].errors[0], scheduledInMs: 2 * HOUR },
    { kind: "collab.sync", state: "retryable", createdMs: 25 * MIN, attempt: 2, error: JOB_KINDS["collab.sync"].errors[0], scheduledInMs: 90 * 1000 },
    { kind: "ai.triage", state: "retryable", createdMs: 12 * MIN, attempt: 2, error: JOB_KINDS["ai.triage"].errors[0], scheduledInMs: 40 * 1000 },
    { kind: "automation.run", state: "retryable", createdMs: 70 * MIN, attempt: 3, error: JOB_KINDS["automation.run"].errors[0], scheduledInMs: 17 * MIN },
    // Out of attempts, more than a day ago.
    { kind: "ai.embed_article", state: "discarded", createdMs: 3 * DAY, attempt: 3, error: JOB_KINDS["ai.embed_article"].errors[0], finalizedAgoMs: 3 * DAY - 20 * MIN },
    { kind: "mail.system", state: "discarded", createdMs: 5 * DAY, attempt: 10, error: JOB_KINDS["mail.system"].errors[0], finalizedAgoMs: 4 * DAY },
    { kind: "admin.seed_test_workspace", state: "discarded", createdMs: 6 * DAY, attempt: 1, error: JOB_KINDS["admin.seed_test_workspace"].errors[0], finalizedAgoMs: 6 * DAY - 30 * MIN },
    { kind: "inbound.process", state: "discarded", createdMs: 8 * DAY, attempt: 5, error: JOB_KINDS["inbound.process"].errors[0], finalizedAgoMs: 8 * DAY - 2 * HOUR },
    { kind: "notification.push", state: "discarded", createdMs: 11 * DAY, attempt: 5, error: JOB_KINDS["notification.push"].errors[0], finalizedAgoMs: 11 * DAY - HOUR },
    // Waiting and running.
    { kind: "email.send_reply", state: "available", createdMs: 4000, attempt: 0 },
    { kind: "inbound.process", state: "available", createdMs: 3000, attempt: 0 },
    { kind: "ai.triage", state: "available", createdMs: 2000, attempt: 0 },
    { kind: "webhook.deliver", state: "available", createdMs: 1000, attempt: 0 },
    { kind: "mailbox.sync", state: "running", createdMs: 9000, attempt: 1 },
    { kind: "inbound.process", state: "running", createdMs: 7000, attempt: 1 },
    { kind: "ai.triage", state: "running", createdMs: 5000, attempt: 1 },
    // Cancelled by hand.
    { kind: "billing.sync_seats", state: "cancelled", createdMs: 4 * DAY, attempt: 1, error: JOB_KINDS["billing.sync_seats"].errors[0], finalizedAgoMs: 3 * DAY },
    { kind: "webhook.deliver", state: "cancelled", createdMs: 6 * DAY, attempt: 9, error: JOB_KINDS["webhook.deliver"].errors[1], finalizedAgoMs: 6 * DAY - 4 * HOUR },
    { kind: "collab.sync", state: "cancelled", createdMs: 9 * DAY, attempt: 0, finalizedAgoMs: 9 * DAY - HOUR },
  ];
  // Periodic work, scheduled ahead.
  for (const [i, kind] of ["sla.check", "sla.check", "admin.updates", "admin.expire_test_workspaces", "billing.sync_seats", "sla.check", "mail.system", "email.send_reply", "notification.push"].entries())
    specs.push({ kind, state: "scheduled", createdMs: int(1, 50) * MIN, attempt: 0, scheduledInMs: (i + 1) * int(3, 15) * MIN });
  // What already ran today.
  const done = ["email.send_reply", "inbound.process", "webhook.deliver", "ai.triage", "mailbox.sync", "automation.run", "notification.push", "mail.system", "sla.check", "collab.sync", "billing.sync_seats", "ai.embed_article"];
  for (let i = 0; i < 84; i++) {
    const createdMs = int(1, 20 * 60) * MIN;
    specs.push({ kind: pick(done), state: "completed", createdMs, attempt: chance(0.1) ? 2 : 1, finalizedAgoMs: Math.max(createdMs - int(1, 40) * 1000, 500) });
  }

  specs.sort((a, b) => b.createdMs - a.createdMs);
  let id = 48_212;
  db.jobs = specs.map((sp): AdminJob => {
    id += int(1, 23);
    const k = JOB_KINDS[sp.kind];
    const created = ago(sp.createdMs);
    const attempted = sp.attempt > 0 ? ago(Math.max(sp.createdMs - int(1, 20) * 1000, 0)) : null;
    return {
      id,
      kind: sp.kind,
      queue: k.queue,
      state: sp.state,
      attempt: sp.attempt,
      max_attempts: k.max,
      args: sp.args ?? argsFor(sp.kind),
      last_error: sp.error ?? null,
      errors: sp.error ? Math.max(sp.attempt, 1) : 0,
      created_at: created,
      scheduled_at: sp.scheduledInMs !== undefined ? iso(NOW + sp.scheduledInMs) : created,
      attempted_at: attempted,
      finalized_at: sp.finalizedAgoMs !== undefined ? ago(sp.finalizedAgoMs) : null,
    };
  });
  db.nextJobId = id + 1;
}

function seedAudit() {
  const rows: AdminAuditRec[] = [];
  const opIp: Record<string, string> = {};
  OPERATORS.forEach((o, i) => (opIp[o.key] = ipFor(i * 11 + 1)));
  opIp.ava = db.ip;
  const ws = (name: string) => db.workspaces.find((w) => w.name === name);
  const user = (name: string) => db.users.find((u) => u.name === name);
  const add = (by: OperatorKey | "cli" | "system", action: string, target_type: AdminAuditRec["target_type"], target_id: string | null, data: Record<string, unknown>, agoMs: number) =>
    rows.push({
      id: uuid(),
      actor: by === "cli" ? { type: "cli", id: null, email: "cli" } : by === "system" ? { type: "system", id: null, email: "system" } : { type: "admin", id: ops[by].id, email: ops[by].email },
      action,
      target_type,
      target_id,
      data,
      ip: by === "cli" || by === "system" ? "" : opIp[by],
      created_at: ago(agoMs),
    });

  // Sign-ins over the last three weeks: a working day starts with one, some end with signing out.
  for (let d = 0; d < 22; d++) {
    for (const k of ["ava", "noah", "kofi", "hana", "elena"] as const) {
      const weekend = new Date(NOW - d * DAY).getUTCDay() % 6 === 0;
      if (weekend ? !chance(0.15) : !chance(k === "elena" ? 0.35 : 0.8)) continue;
      if (k === "hana" && d > 17) continue;
      const at = d * DAY + int(0, 9) * HOUR + int(0, 59) * MIN + 40 * MIN;
      if (chance(0.08)) add(k, "login.failed", "admin", ops[k].id, { method: "totp" }, at + 2 * MIN);
      add(k, "login", "admin", ops[k].id, { factor: chance(0.05) ? "recovery" : "totp" }, at);
      if (chance(0.3)) add(k, "logout", "admin", ops[k].id, {}, at - int(1, 6) * HOUR);
    }
  }
  // A guess at Marcus's account after his access ended.
  add("marcus", "login.failed", "admin", ops.marcus.id, { method: "password" }, 9 * DAY + 3 * HOUR);

  const halcyon = ws("Halcyon Studio")!;
  const paperplane = ws("Paperplane")!;
  const fjord = ws("Fjord Analytics")!;
  const copperleaf = ws("Copperleaf")!;
  const vantage = ws("Vantage Legal")!;
  const tidewater = ws("Tidewater Media")!;
  const quick = ws("Quick Gift Rewards")!;
  const lumen = ws("Lumen Dental")!;
  const meridian = ws("Meridian Schools")!;
  const victor = user("Victor Hale")!;
  const rex = user("Rex Dalton")!;
  const priya = user("Priya Raman")!;
  const sam = user("Sam Okafor")!;
  const unverified = db.users.find((u) => !u.email_verified)!;
  const retried = db.jobs.find((j) => j.state === "completed")!;
  const cancelled = db.jobs.find((j) => j.state === "cancelled")!;
  const test = (name: string) => ws(name)!;

  add("noah", "admin.granted", "admin", ops.hana.id, { role: "support" }, 18 * DAY);
  add("ava", "admin.role_changed", "admin", ops.elena.id, { from: "support", to: "viewer" }, 12 * DAY + 3 * HOUR);
  add("ava", "admin.revoked", "admin", ops.marcus.id, { role: "support" }, 10 * DAY + 5 * HOUR);
  add("kofi", "user.unsuspended", "user", sam.id, {}, 19 * DAY + 2 * HOUR);
  add("kofi", "user.suspended", "user", rex.id, { reason: rex.suspended_reason }, 17 * DAY);
  add("noah", "user.deleted", "user", uuid(), {}, 16 * DAY + 6 * HOUR);
  add("kofi", "workspace.limits_changed", "workspace", paperplane.id, { max_mailboxes: null, max_members: 5, monthly_ai_usd: null }, 14 * DAY + 4 * HOUR);
  add("ava", "workspace.ai_granted", "workspace", fjord.id, { amount_usd: 25, note: "Credit for the triage slowdown on May 30", grant: uuid() }, 13 * DAY + HOUR);
  add("noah", "user.mfa_disabled", "user", priya.id, {}, 11 * DAY + 2 * HOUR);
  add("ava", "test_workspace.sample_added", "workspace", test("Northwind pilot").id, { size: "small", kinds: ["team", "collab"], batch: 2 }, 38 * DAY);
  add("hana", "test_workspace.created", "workspace", test("Widget QA").id, { plan: "free", owner_id: membersOf(test("Widget QA").id)[0].user_id, new_account: true, password_days: 14, delete_after_days: 30, test_expires_at: test("Widget QA").test!.expires_at, reason: test("Widget QA").test!.reason }, 25 * DAY);
  add("cli", "test_workspace.created", "workspace", test("Reports preview").id, { plan: "pro", owner_id: ops.hana.id, new_account: false, delete_after_days: 90, test_expires_at: test("Reports preview").test!.expires_at, sample: { size: "small", kinds: ["tickets", "history"] }, reason: test("Reports preview").test!.reason }, 30 * DAY);
  add("kofi", "test_workspace.created", "workspace", test("Acme demo").id, { plan: "pro", owner_id: membersOf(test("Acme demo").id).find((m) => m.role === "owner")!.user_id, new_account: true, password_days: 30, delete_after_days: 30, test_expires_at: test("Acme demo").test!.expires_at, sample: { size: "realistic", kinds: ["tickets", "chat", "kb", "team", "collab", "history"] }, reason: test("Acme demo").test!.reason }, 18 * DAY);
  add("kofi", "workspace.suspended", "workspace", quick.id, { reason: quick.suspended_reason }, 9 * DAY + 2 * HOUR);
  add("kofi", "user.suspended", "user", victor.id, { reason: victor.suspended_reason }, 9 * DAY + HOUR);
  add("ava", "workspace.collab_changed", "workspace", copperleaf.id, { access: "complimentary", until: copperleaf.collab.until, note: copperleaf.collab.note }, 8 * DAY + 5 * HOUR);
  add("noah", "workspace.deleted", "workspace", uuid(), {}, 7 * DAY + 3 * HOUR);
  add("noah", "test_workspace.created", "workspace", test("Load test, large").id, { plan: "free", owner_id: membersOf(test("Load test, large").id)[0].user_id, new_account: true, password_days: 7, delete_after_days: 7, test_expires_at: test("Load test, large").test!.expires_at, sample: { size: "large", kinds: ["tickets", "chat", "kb", "team", "collab", "history"] }, reason: test("Load test, large").test!.reason }, 6 * DAY);
  add("noah", "workspace.suspended", "workspace", lumen.id, { reason: lumen.suspended_reason }, 6 * DAY + 2 * HOUR);
  add("hana", "workspace.collab_changed", "workspace", tidewater.id, { access: "offered", until: null, note: tidewater.collab.note }, 5 * DAY + 3 * HOUR);
  add("kofi", "workspace.collab_changed", "workspace", vantage.id, { access: "offered", until: null, note: vantage.collab.note }, 4 * DAY + HOUR);
  add("kofi", "job.cancelled", "job", String(cancelled.id), { from: "retryable" }, 3 * DAY);
  add("system", "test_workspace.expired", "workspace", uuid(), { accounts_deleted: 1 }, 3 * DAY - 25 * MIN);
  add("kofi", "user.sessions_revoked", "user", sam.id, { all: true }, 2 * DAY + 4 * HOUR);
  add("ava", "workspace.limits_changed", "workspace", meridian.id, { max_mailboxes: null, max_members: null, monthly_ai_usd: 150 }, 2 * DAY + HOUR);
  add("kofi", "job.retried", "job", String(retried.id), { from: "retryable" }, 20 * HOUR);
  add("hana", "user.verification_sent", "user", unverified.id, {}, 5 * HOUR);
  add("kofi", "workspace.ai_granted", "workspace", halcyon.id, { amount_usd: 10, note: "Goodwill while their card is updated", grant: uuid() }, 3 * HOUR);
  halcyon.ai.purchased_usd = 10;

  db.audit = rows.sort((a, b) => b.created_at.localeCompare(a.created_at));

  // The operators' actions show in each workspace's own log too.
  const own = (w: WorkspaceRec, action: string, data: Record<string, unknown>, agoMs: number) =>
    w.audit.unshift({ id: uuid(), actor: { type: "system", user: null, api_key_id: null }, action, entity_type: "workspace", entity_id: w.id, data: { ...data, actor: "instance_admin" }, ip: "", created_at: ago(agoMs) });
  own(paperplane, "workspace.limits_changed", { max_mailboxes: null, max_members: 5, monthly_ai_usd: null }, 14 * DAY + 4 * HOUR);
  own(fjord, "workspace.ai_granted", { amount_usd: 25 }, 13 * DAY + HOUR);
  own(quick, "workspace.suspended", {}, 9 * DAY + 2 * HOUR);
  own(copperleaf, "workspace.collab_changed", { access: "complimentary", until: copperleaf.collab.until }, 8 * DAY + 5 * HOUR);
  own(lumen, "workspace.suspended", {}, 6 * DAY + 2 * HOUR);
  own(tidewater, "workspace.collab_changed", { access: "offered", until: null }, 5 * DAY + 3 * HOUR);
  own(vantage, "workspace.collab_changed", { access: "offered", until: null }, 4 * DAY + HOUR);
  own(meridian, "workspace.limits_changed", { max_mailboxes: null, max_members: null, monthly_ai_usd: 150 }, 2 * DAY + HOUR);
  own(halcyon, "workspace.ai_granted", { amount_usd: 10 }, 3 * HOUR);
  for (const w of db.workspaces) w.audit.sort((a, b) => b.created_at.localeCompare(a.created_at));
}

function seedUpdates() {
  const started = ago(2 * DAY + 7 * HOUR);
  db.updates = {
    current: "v1.9.2",
    previous_version: "v1.9.1",
    running_since: started,
    latest: { version: "v1.9.3", url: "https://github.com/useopendesk/opendesk/releases/tag/v1.9.3", published_at: ago(5 * HOUR) },
    checked_at: ago(2 * HOUR + 12 * MIN),
    auto: { reported: true, enabled: true, last_run_at: ago(6 * HOUR + 30 * MIN), last_result: "current", last_detail: "Already on v1.9.2, the newest release." },
  };
  // Busier on weekdays, so the chart has a rhythm.
  db.aiWeights = Array.from({ length: 30 }, (_, i) => {
    const day = new Date(NOW - (29 - i) * DAY).getUTCDay();
    return (day === 0 || day === 6 ? 0.45 : 1) * (0.75 + rand() * 0.5);
  });
}

/** Fills the in-memory database. */
export function seed() {
  reseed();
  db.users = [];
  db.members = [];
  db.workspaces = [];
  db.admins = [];
  db.jobs = [];
  db.audit = [];
  db.session = null;
  db.pendingMfa.clear();
  seedOperators();
  seedNamed();
  seedOthers();
  seedLoners();
  seedTestWorkspaces();
  seedJobs();
  seedAudit();
  seedUpdates();
}
