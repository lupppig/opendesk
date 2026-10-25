// Auth, account, sessions and two-factor.

import type { Account, Membership, TokenResponse, User } from "@/lib/api/types";
import { db, me, userFromToken } from "../db";
import { HttpError, invalid, post, get, patch, del, required, respond, s, type Ctx } from "../router";
import { SECOND_ACCOUNT_EMAIL } from "../seed";
import { nowIso, randomToken, uuid } from "../util";

// The browser's refresh cookies, one per signed-in account (contracts/tokens.md),
// kept as account emails in localStorage so mock sign-ins survive a reload. By
// default Maya is signed in with two accounts, so the account switcher shows.
const KEY = "od-mock-session";
const ACCOUNTS_KEY = "od-mock-accounts";
const LAST_KEY = "od-mock-last-account";
const MAX_ACCOUNTS = 5;

function load(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function save(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    /* storage unavailable */
  }
}

/** The emails of the accounts signed in to, in the order they were added. */
function cookieJar(): string[] {
  const raw = load(ACCOUNTS_KEY);
  if (raw !== null) {
    try {
      const v: unknown = JSON.parse(raw);
      if (Array.isArray(v)) return v.filter((x): x is string => typeof x === "string");
    } catch {
      /* reset below */
    }
  }
  // First run (or the single-account flag of earlier mock builds).
  return load(KEY) === "0" ? [] : [db.users[0]?.email, SECOND_ACCOUNT_EMAIL].filter((x): x is string => !!x);
}
function setJar(emails: string[]) {
  save(ACCOUNTS_KEY, JSON.stringify(emails));
  save(KEY, emails.length ? "1" : "0");
}
function signedInUsers(): User[] {
  return cookieJar()
    .map((e) => db.users.find((u) => u.email.toLowerCase() === e.toLowerCase()))
    .filter((u): u is User => !!u);
}

export function signedIn(): boolean {
  return signedInUsers().length > 0;
}

export function setSignedIn(on: boolean) {
  if (!on) setJar([]);
  else if (!signedIn()) setJar([db.users[0].email]);
}

/** Access tokens name their account, so each request runs as the account it was sent for. */
export function tokens(): TokenResponse {
  return { access_token: `mock_access_${db.meId}_${randomToken(24)}`, expires_in: 900, user: me() };
}

export function memberships(uid = db.meId): Membership[] {
  return db.workspaces
    .filter((w) => w.members.some((m) => m.user_id === uid))
    .map((w) => ({ workspace: w.workspace, role: w.members.find((m) => m.user_id === uid)!.role }));
}

/** Sign u in beside the other accounts (or replace its own session); at most five. */
function signInAs(u: User): TokenResponse {
  const jar = cookieJar();
  const has = jar.some((e) => e.toLowerCase() === u.email.toLowerCase());
  if (!has && signedInUsers().length >= MAX_ACCOUNTS)
    throw new HttpError(409, "too_many_accounts", "You're signed in to the most accounts this browser can hold. Log out of one, then try again.");
  if (!has) setJar([...jar, u.email]);
  db.meId = u.id;
  save(LAST_KEY, u.email);
  return tokens();
}

function signOutOf(uid: string) {
  const u = db.users.find((x) => x.id === uid);
  if (u) setJar(cookieJar().filter((e) => e.toLowerCase() !== u.email.toLowerCase()));
}

function accountOut(u: User): Account {
  return {
    user: { id: u.id, name: u.name, email: u.email, avatar_url: u.avatar_url },
    memberships: memberships(u.id).map((m) => ({
      role: m.role,
      workspace: { id: m.workspace.id, name: m.workspace.name, slug: m.workspace.slug, avatar_url: m.workspace.avatar_url },
    })),
  };
}

let pendingMfa: string | null = null;

function byEmail(email: string): User | undefined {
  return db.users.find((u) => u.email.toLowerCase() === email.trim().toLowerCase());
}

function checkPassword(pw: string | undefined) {
  if (!pw || pw.length < 12) throw invalid("Password must be at least 12 characters");
  if (pw.length > 128) throw invalid("Password must be at most 128 characters");
  if (["password1234", "123456789012", "qwertyuiopas"].includes(pw.toLowerCase())) throw invalid("This password appears in a list of breached passwords. Choose another one.");
}

/** OAuth and link sign-ins: the default account (any provider maps to Maya). */
function login(): TokenResponse {
  return signInAs(db.users[0]);
}

function requestUser(ctx: Ctx): string | null {
  return userFromToken(ctx.headers.get("Authorization"));
}

export function registerAuth() {
  get("/v1/auth/config", () => ({
    providers: [
      { id: "google", name: "Google" },
      { id: "microsoft", name: "Microsoft" },
      { id: "github", name: "GitHub" },
      { id: "oidc", name: "SSO" },
    ],
    password_login: true,
    signup_enabled: true,
    turnstile_site_key: null,
    web_push_key: null,
  }));

  post("/v1/auth/login", (ctx) => {
    const email = required(ctx, "email");
    const password = s(ctx, "password") ?? "";
    if (password === "wrong" || !password) throw new HttpError(401, "invalid_credentials", "Email or password is incorrect");
    // A known address signs in to its account; any other one to Maya's (the demo account).
    const u = byEmail(email) ?? db.users[0];
    if (email.toLowerCase().includes("mfa")) {
      pendingMfa = u.id;
      return { mfa_required: true, mfa_token: "mfa_mock" };
    }
    return signInAs(u);
  });

  post("/v1/auth/register", (ctx) => {
    const email = required(ctx, "email");
    const name = required(ctx, "name");
    checkPassword(s(ctx, "password"));
    if (email === "taken@margin.app" || byEmail(email)) throw new HttpError(409, "email_taken", "An account with this email already exists");
    if (signedInUsers().length >= MAX_ACCOUNTS)
      throw new HttpError(409, "too_many_accounts", "You're signed in to the most accounts this browser can hold. Log out of one, then try again.");
    // A new account has no workspace yet: it goes through onboarding.
    const u: User = { id: uuid(), email, name, avatar_url: null, avatar_source: null, email_verified: false, totp_enabled: false, time_format: "auto", created_at: nowIso() };
    db.users.push(u);
    return signInAs(u);
  });

  post("/v1/auth/mfa", (ctx) => {
    const code = (s(ctx, "code") ?? "").replace(/\s/g, "");
    if (code !== "123456" && !/^[a-z0-9]{4}(-[a-z0-9]{4}){3}$/i.test(code)) throw new HttpError(401, "invalid_code", "That code is not valid. Try again.");
    const u = db.users.find((x) => x.id === pendingMfa) ?? db.users[0];
    pendingMfa = null;
    return signInAs(u);
  });

  // One account's cookie: the named one, or the one used most recently.
  post("/v1/auth/refresh", (ctx) => {
    const users = signedInUsers();
    const want = s(ctx, "user_id");
    const last = load(LAST_KEY);
    const u = want ? users.find((x) => x.id === want) : (users.find((x) => x.email === last) ?? users[0]);
    if (!u) throw new HttpError(401, "unauthenticated", "Your session has ended. Sign in again.");
    db.meId = u.id;
    save(LAST_KEY, u.email);
    return respond(200, tokens());
  });

  post("/v1/auth/accounts", () => ({ accounts: signedInUsers().map(accountOut) }));

  post("/v1/auth/logout", (ctx) => {
    if (ctx.body.all === true) setJar([]);
    else {
      const uid = requestUser(ctx) ?? s(ctx, "user_id");
      if (uid) signOutOf(uid);
    }
    return respond(204);
  });

  post("/v1/auth/password/forgot", () => respond(204));
  post("/v1/auth/password/reset", (ctx) => {
    const token = required(ctx, "token");
    if (token === "expired") throw new HttpError(400, "invalid_token", "This reset link has expired. Request a new one.");
    checkPassword(s(ctx, "password"));
    return respond(204);
  });
  post("/v1/auth/email/verify", (ctx) => {
    const token = required(ctx, "token");
    if (token === "expired") throw new HttpError(400, "invalid_token", "This verification link has expired.");
    me().email_verified = true;
    return respond(204);
  });
  post("/v1/auth/email/resend", () => respond(204));

  get("/v1/auth/oauth/:provider/start", (ctx) => respond(302, undefined, { location: `/auth/callback?code=mock-${ctx.params.provider}` }));

  post("/v1/auth/oauth/exchange", (ctx) => {
    const code = required(ctx, "code");
    if (code === "mock-link") return { link_required: true, link_token: "lt_mock", email: me().email };
    if (code === "mock-mfa") return { mfa_required: true, mfa_token: "mfa_mock" };
    if (code === "mock-bad") throw new HttpError(400, "invalid_code", "This sign-in link has expired. Try again.");
    return login();
  });

  post("/v1/auth/oauth/link", (ctx) => {
    required(ctx, "link_token");
    const password = s(ctx, "password");
    if (!password || password === "wrong") throw new HttpError(401, "invalid_credentials", "Password is incorrect");
    return login();
  });

  post("/v1/auth/totp/setup", () => {
    const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
    let secret = "";
    for (let i = 0; i < 32; i++) secret += alphabet[Math.floor(Math.random() * 32)];
    db.totpPending = secret;
    return respond(200, { secret, otpauth_url: `otpauth://totp/opendesk:${encodeURIComponent(me().email)}?secret=${secret}&issuer=opendesk&algorithm=SHA1&digits=6&period=30` });
  });
  post("/v1/auth/totp/enable", (ctx) => {
    const code = s(ctx, "code") ?? "";
    if (!/^\d{6}$/.test(code)) throw new HttpError(400, "invalid_code", "Enter the 6-digit code from your app");
    if (!db.totpPending) throw new HttpError(409, "conflict", "Start setup first");
    me().totp_enabled = true;
    db.totpPending = null;
    const codes = Array.from({ length: 10 }, () => {
      const r = randomToken(16).toLowerCase().replace(/[-_]/g, "x");
      return `${r.slice(0, 4)}-${r.slice(4, 8)}-${r.slice(8, 12)}-${r.slice(12, 16)}`;
    });
    return respond(200, { recovery_codes: codes });
  });
  post("/v1/auth/totp/disable", (ctx) => {
    if (!/^\d{6}$/.test(s(ctx, "code") ?? "")) throw new HttpError(400, "invalid_code", "Enter the 6-digit code from your app");
    me().totp_enabled = false;
    return respond(204);
  });

  get("/v1/me", () => ({ user: me(), memberships: memberships() }));
  patch("/v1/me", (ctx): User => {
    const u = me();
    const name = s(ctx, "name");
    if (name !== undefined) {
      if (!name.trim()) throw invalid("Name can't be empty");
      u.name = name.trim().slice(0, 80);
    }
    // The API copies the picture behind an https URL and serves it from /v1/images; "" removes it.
    const avatar = s(ctx, "avatar_url");
    if (avatar !== undefined) {
      if (avatar && !/^https:\/\/\S+$/i.test(avatar)) throw invalid("Use an https URL for the picture");
      u.avatar_url = avatar || null;
      u.avatar_source = avatar ? "upload" : null;
    }
    const clock = s(ctx, "time_format");
    if (clock !== undefined) {
      if (clock !== "auto" && clock !== "12h" && clock !== "24h") throw invalid("time_format must be auto, 12h or 24h");
      u.time_format = clock;
    }
    return u;
  });
  post("/v1/me/password", (ctx) => {
    if ((s(ctx, "current_password") ?? "") === "wrong") throw new HttpError(401, "invalid_credentials", "Current password is incorrect");
    checkPassword(s(ctx, "new_password"));
    return respond(204);
  });
  get("/v1/me/sessions", () => ({ data: db.sessions, next_cursor: null }));
  del("/v1/me/sessions/:id", (ctx) => {
    const sess = db.sessions.find((x) => x.id === ctx.params.id);
    if (!sess) throw new HttpError(404, "not_found", "Session not found");
    if (sess.current) signOutOf(db.meId);
    db.sessions = db.sessions.filter((x) => x.id !== ctx.params.id);
    return respond(204);
  });
  del("/v1/me", (ctx) => {
    if (s(ctx, "password") === "wrong") throw new HttpError(401, "invalid_credentials", "Password is incorrect");
    const sole = db.workspaces.find((w) => {
      const mine = w.members.find((m) => m.user_id === db.meId);
      return mine?.role === "owner" && w.members.filter((m) => m.role === "owner").length === 1 && w.members.length > 1;
    });
    if (sole) throw new HttpError(409, "sole_owner", `You're the only owner of ${sole.workspace.name}. Transfer ownership or delete the workspace first.`);
    signOutOf(db.meId);
    return respond(204);
  });

  // Keep the current session's "last used" fresh.
  for (const x of db.sessions) if (x.current) x.last_used_at = nowIso();
}
