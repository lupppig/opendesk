// Test workspaces (internal/admin/testworkspaces.go): the list, creating one
// with a test account whose password is shown once, extending it or renewing
// the password, and batches of sample data.

import type { SampleKind, SampleSize, TestPlan } from "@/lib/api/types";
import { db, membersOf, userByEmail, userById, workspaceById, type UserRec, type WorkspaceRec } from "../db";
import { HttpError, conflict, get, has, invalid, notFound, paginate, patch, permit, post, respond, s, stepUp, type Ctx } from "../router";
import { KINDS, SIZES, startBatch } from "../samples";
import { INSTANCE, audit, auditWorkspace, testWorkspaceView } from "../shapes";
import { DAY, generatePassword, isRecord, isUuid, nowIso, slugify, uuid } from "../util";

const signInUrl = (email: string) => `${INSTANCE.app_url}/login?email=${encodeURIComponent(email)}`;

function days(v: unknown, max: number, field: string): number {
  if (typeof v !== "number" || !Number.isInteger(v) || v < 1 || v > max) throw invalid(`${field} must be between 1 and ${max}.`);
  return v;
}

/** {size, kinds}: kinds deduplicated, in the generator's order. */
function sampleRequest(v: unknown): { size: SampleSize; kinds: SampleKind[] } {
  if (!isRecord(v)) throw invalid("Choose a size and at least one kind of sample data.");
  if (!SIZES.includes(v.size as SampleSize)) throw invalid("size must be small, realistic or large.");
  const raw = Array.isArray(v.kinds) ? v.kinds : [];
  if (raw.some((k) => !KINDS.includes(k as SampleKind))) throw invalid("kinds must be among tickets, chat, kb, team, collab and history.");
  const kinds = KINDS.filter((k) => raw.includes(k));
  if (!kinds.length) throw invalid("Choose at least one kind of sample data.");
  return { size: v.size as SampleSize, kinds };
}

function uniqueSlug(name: string) {
  const base = slugify(name);
  let slug = base;
  for (let i = 2; db.workspaces.some((w) => w.slug === slug); i++) slug = `${base}-${i}`;
  return slug;
}

function findTest(ctx: Ctx) {
  const w = workspaceById(ctx.params.id);
  if (!w?.test) throw notFound("Test workspace");
  return w;
}

export function registerTestWorkspaces() {
  get("/v1/admin/test-workspaces", (ctx) => {
    permit(ctx, "read");
    const list = db.workspaces.filter((w) => w.test).sort((a, b) => a.created_at.localeCompare(b.created_at));
    const page = paginate(ctx, list);
    return { data: page.data.map(testWorkspaceView), next_cursor: page.next_cursor };
  });

  post("/v1/admin/test-workspaces", (ctx) => {
    const a = permit(ctx, "workspaces.test");
    stepUp(ctx);
    const name = (s(ctx, "name") ?? "").trim();
    if (!name) throw invalid("Workspace name is required.");
    if (name.length > 100) throw invalid("Workspace name is too long.");
    const reason = (s(ctx, "reason") ?? "").trim();
    if (!reason || reason.length > 500) throw invalid("Give a reason of 1 to 500 characters.");
    const plan = (s(ctx, "plan") ?? "free") as TestPlan;
    if (!["free", "pro", "collab"].includes(plan)) throw invalid("plan must be free, pro or collab.");
    const passwordDays = has(ctx, "password_days") ? days(ctx.body.password_days, 2, "password_days") : 1;
    const deleteAfter = !has(ctx, "delete_after_days") ? 30 : ctx.body.delete_after_days === null ? null : days(ctx.body.delete_after_days, 365, "delete_after_days");
    const sample = has(ctx, "sample") && ctx.body.sample !== null ? sampleRequest(ctx.body.sample) : null;

    // The owner: an existing account by id or address, or a new test account on .invalid.
    const o = isRecord(ctx.body.owner) ? ctx.body.owner : {};
    const email = typeof o.email === "string" ? o.email.trim().toLowerCase() : "";
    const userId = typeof o.user_id === "string" ? o.user_id : "";
    if (!!email === !!userId) throw invalid("Give the owner's email or user_id, not both.");
    if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) throw new HttpError(422, "invalid_email", "Enter a valid email address for the owner.");
    let owner: UserRec | undefined;
    if (userId) {
      owner = isUuid(userId) ? userById(userId) : undefined;
      if (!owner) throw new HttpError(404, "user_not_found", "No account has this id.");
    } else owner = userByEmail(email);
    const newAccount = !owner;
    let password: string | null = null;
    const now = Date.now();
    const passwordExpires = new Date(now + passwordDays * DAY).toISOString();
    if (!owner) {
      if (!email.endsWith(".invalid"))
        throw new HttpError(422, "test_email_required", "A new test account needs an address ending in .invalid, which never receives mail. To give a real person a test workspace, choose their existing account.");
      const given = typeof o.name === "string" ? o.name.trim().slice(0, 200) : "";
      const local = email.split("@")[0].replace(/[._-]+/g, " ").trim();
      password = generatePassword();
      owner = {
        id: uuid(),
        email,
        name: given || local.replace(/\b\w/g, (c) => c.toUpperCase()) || "Test owner",
        avatar_url: null,
        email_verified: true,
        totp_enabled: false,
        has_password: true,
        suspended_at: null,
        suspended_reason: "",
        last_seen_at: null,
        created_at: nowIso(),
        test_account: true,
        password_expires_at: passwordExpires,
        identities: [],
        passkeys: 0,
        sessions: [],
      };
    } else if (owner.suspended_at) throw conflict("This account is suspended.", "account_suspended");
    if (newAccount) db.users.push(owner);

    const expires = deleteAfter === null ? null : new Date(now + deleteAfter * DAY).toISOString();
    const w: WorkspaceRec = {
      id: uuid(),
      name,
      slug: uniqueSlug(name),
      avatar_url: null,
      created_at: nowIso(),
      last_activity_at: nowIso(),
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
      test: {
        expires_at: expires,
        reason,
        plan,
        created_by: a.user.id,
        sample: { status: "none", size: "", kinds: [], counts: null, batches: 0, started_at: null, seeded_at: null, error: "" },
      },
    };
    db.workspaces.push(w);
    db.members.push({ workspace_id: w.id, user_id: owner.id, role: "owner", created_at: nowIso() });
    // The plan without Stripe (billing is on in the mock).
    if (plan === "pro") {
      w.limits = { max_mailboxes: 0, max_members: 0, monthly_ai_usd: 20 };
      w.ai.purchased_usd = 50;
    } else if (plan === "collab") w.collab = { access: "complimentary", until: expires, note: "Test workspace" };
    if (sample) startBatch(w, sample.size, sample.kinds);

    auditWorkspace(w, "workspace.test_created", "workspace", w.id, { plan, test_expires_at: expires });
    const data: Record<string, unknown> = { plan, owner_id: owner.id, new_account: newAccount, delete_after_days: deleteAfter, test_expires_at: expires, reason };
    if (newAccount) data.password_days = passwordDays;
    if (sample) data.sample = sample;
    audit(a, "test_workspace.created", "workspace", w.id, data);

    const view = testWorkspaceView(w);
    return respond(201, {
      workspace: view,
      owner: { id: owner.id, email: owner.email, new_account: newAccount, password, password_expires_at: newAccount ? passwordExpires : owner.password_expires_at },
      sign_in_url: signInUrl(owner.email),
    });
  });

  patch("/v1/admin/test-workspaces/:id", (ctx) => {
    const a = permit(ctx, "workspaces.test");
    const w = findTest(ctx);
    const setDelete = has(ctx, "delete_after_days");
    const renew = has(ctx, "password_days") && ctx.body.password_days !== null;
    if (!setDelete && !renew) throw invalid("Give delete_after_days or password_days.");
    const deleteAfter = setDelete && ctx.body.delete_after_days !== null ? days(ctx.body.delete_after_days, 365, "delete_after_days") : null;
    const passwordDays = renew ? days(ctx.body.password_days, 2, "password_days") : 0;
    stepUp(ctx);
    const t = w.test!;
    const data: Record<string, unknown> = {};
    if (setDelete) {
      t.expires_at = deleteAfter === null ? null : new Date(Date.now() + deleteAfter * DAY).toISOString();
      data.delete_after_days = deleteAfter;
      data.test_expires_at = t.expires_at;
      if (t.plan === "collab") w.collab = { access: "complimentary", until: t.expires_at, note: "Test workspace" };
    }
    let password: string | null = null;
    const ownerM = membersOf(w.id).find((m) => m.role === "owner");
    const owner = ownerM ? userById(ownerM.user_id) : undefined;
    if (renew) {
      if (!owner?.test_account) throw conflict("The owner's own account keeps its own password; only a test account's is renewed.", "not_test_account");
      password = generatePassword();
      owner.password_expires_at = new Date(Date.now() + passwordDays * DAY).toISOString();
      owner.sessions = [];
      Object.assign(data, { password_renewed: true, password_days: passwordDays, owner_id: owner.id });
    }
    audit(a, "test_workspace.changed", "workspace", w.id, data);
    const view = testWorkspaceView(w);
    return {
      workspace: view,
      owner: owner ? { id: owner.id, email: owner.email, new_account: false, password, password_expires_at: owner.password_expires_at } : { id: "", email: "", new_account: false, password: null, password_expires_at: null },
      sign_in_url: owner ? signInUrl(owner.email) : "",
    };
  });

  post("/v1/admin/workspaces/:id/sample-data", (ctx) => {
    const a = permit(ctx, "workspaces.test");
    const req = sampleRequest(ctx.body);
    const w = workspaceById(ctx.params.id);
    if (!w) throw notFound("Workspace");
    if (!w.test) throw conflict("Sample data is added to test workspaces only.", "not_test");
    if (w.test.sample.status === "running") throw conflict("Sample data is still being added. Try again when it finishes.", "seeding");
    startBatch(w, req.size, req.kinds);
    audit(a, "test_workspace.sample_added", "workspace", w.id, { size: req.size, kinds: req.kinds, batch: w.test.sample.batches });
    return respond(202, testWorkspaceView(w));
  });
}
