// Shared helpers for the end-to-end runs: config from env, a browser context
// that records console errors and failed API calls, mail links read from the
// API's log mailer output, and small assertion helpers.

import { createHmac } from "node:crypto";
import fs from "node:fs";
import { chromium } from "playwright";

export const cfg = {
  app: process.env.E2E_APP_URL || "http://localhost:5173",
  api: process.env.E2E_API_URL || "http://localhost:8080",
  apiLog: process.env.E2E_API_LOG || "",
  mailSink: process.env.E2E_MAIL_SINK || "",
  inboundSecret: process.env.E2E_INBOUND_SECRET || "dev",
  headless: process.env.E2E_HEADED !== "1",
  shots: process.env.E2E_SHOTS || "",
};

export const PASSWORD = "correct horse battery staple 42";

export function uid() {
  return Math.random().toString(36).slice(2, 8);
}

let browser;
export async function launch() {
  browser ??= await chromium.launch({ headless: cfg.headless });
  return browser;
}

export async function close() {
  await browser?.close();
  browser = undefined;
}

/** A fresh context and page that collect console errors and failed /v1 calls. */
export async function newPage(label = "page") {
  const b = await launch();
  const ctx = await b.newContext({ viewport: { width: 1400, height: 900 } });
  ctx.setDefaultTimeout(Number(process.env.E2E_TIMEOUT || 10_000));
  const page = await ctx.newPage();
  const problems = [];
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().includes("status of 401")) problems.push(`[${label}] console: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`[${label}] pageerror: ${e.message} ${(e.stack ?? "").split("\n").slice(1, 3).join(" ")}`));
  page.on("response", async (r) => {
    const u = r.url();
    if (!u.includes("/v1/") || r.status() < 400) return;
    if (r.status() === 401 && u.endsWith("/v1/auth/refresh")) return;
    let body = "";
    try {
      body = (await r.text()).slice(0, 300);
    } catch {
      /* gone */
    }
    problems.push(`[${label}] ${r.request().method()} ${u.replace(cfg.api, "")} -> ${r.status()} ${body}`);
  });
  return { ctx, page, problems };
}

/** Undoes quoted-printable (soft breaks, =XX) in a raw message that uses it, so links read as sent. */
function qpDecode(raw) {
  if (!/Content-Transfer-Encoding:\s*quoted-printable/i.test(raw)) return raw;
  return raw.replace(/=\r?\n/g, "").replace(/=([0-9A-F]{2})/g, (_, h) => String.fromCharCode(parseInt(h, 16)));
}

/**
 * The newest link to `path` (like "/verify" or "/csat#t=") in mail sent to `to`:
 * from the SMTP sink file (E2E_MAIL_SINK, see smtp-sink.mjs) when set, else
 * from the API's log mailer output (E2E_API_LOG).
 */
export async function mailLink(path, to, timeoutMs = 10_000) {
  if (!cfg.mailSink && !cfg.apiLog) throw new Error("set E2E_MAIL_SINK or E2E_API_LOG");
  const deadline = Date.now() + timeoutMs;
  const re = new RegExp(`${cfg.app.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}${path}[^\\s"'<>\\\\]*`, "g");
  for (;;) {
    const mails = cfg.mailSink
      ? (fs.existsSync(cfg.mailSink) ? fs.readFileSync(cfg.mailSink, "utf8") : "").split("\n").filter(Boolean).map((l) => JSON.parse(l)).map((m) => ({ to: m.to.join(","), body: m.data }))
      : fs.readFileSync(cfg.apiLog, "utf8").split("\n").filter((l) => l.includes("mail (log driver)")).map((l) => ({ to: l, body: l }));
    for (const m of mails.reverse()) {
      if (to && !m.to.includes(to)) continue;
      // The SMTP sink already decodes each MIME part before writing it.
      const body = cfg.mailSink ? m.body : qpDecode(m.body);
      const found = [...body.matchAll(re)];
      if (found.length) return found[found.length - 1][0].replace(/&amp;/g, "&");
    }
    if (Date.now() > deadline) throw new Error(`no ${path} mail for ${to ?? "anyone"}`);
    await new Promise((r) => setTimeout(r, 300));
  }
}

/** Calls the API with a bearer token taken from the page (the access token lives in memory). */
export async function api(method, path, { token, workspace, body, headers = {} } = {}) {
  const h = { Accept: "application/json", ...headers };
  if (token) h.Authorization = `Bearer ${token}`;
  if (workspace) h["OD-Workspace"] = workspace;
  if (body !== undefined && typeof body !== "string") h["Content-Type"] = "application/json";
  const res = await fetch(cfg.api + path, { method, headers: h, body: typeof body === "string" ? body : body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let json = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    /* not JSON */
  }
  return { status: res.status, json, text, headers: res.headers };
}

export async function inboundEmail(to, raw) {
  // Signed like the Cloudflare Email Worker: HMAC-SHA256 over "<t>.<to>.<envelope from>." and the message.
  const t = Math.floor(Date.now() / 1000);
  const mac = createHmac("sha256", cfg.inboundSecret).update(`${t}.${to}..`).update(raw).digest("hex");
  const res = await fetch(`${cfg.api}/v1/inbound/email?to=${encodeURIComponent(to)}`, {
    method: "POST",
    headers: { "OD-Inbound-Signature": `t=${t},v1=${mac}`, "Content-Type": "message/rfc822" },
    body: raw,
  });
  if (res.status !== 202) throw new Error(`inbound -> ${res.status} ${await res.text()}`);
}

export function rawEmail({ from, fromName = "Casey Customer", to, subject, text, messageId, inReplyTo }) {
  const id = messageId ?? `<${uid()}.${Date.now()}@customer.test>`;
  return [
    `From: ${fromName} <${from}>`,
    `To: ${to}`,
    `Subject: ${subject}`,
    `Message-ID: ${id}`,
    ...(inReplyTo ? [`In-Reply-To: ${inReplyTo}`, `References: ${inReplyTo}`] : []),
    `Date: ${new Date().toUTCString()}`,
    "MIME-Version: 1.0",
    "Content-Type: text/plain; charset=utf-8",
    "",
    text,
    "",
  ].join("\r\n");
}

export async function shot(page, name) {
  if (!cfg.shots) return;
  fs.mkdirSync(cfg.shots, { recursive: true });
  await page.screenshot({ path: `${cfg.shots}/${name}.png`, fullPage: false });
}

export function assert(cond, msg) {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

/** Polls fn until it returns a truthy value. */
export async function until(fn, msg, timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  let last;
  for (;;) {
    try {
      last = await fn();
      if (last) return last;
    } catch (e) {
      last = e;
    }
    if (Date.now() > deadline) throw new Error(`timed out: ${msg} (${last instanceof Error ? last.message : JSON.stringify(last)})`);
    await new Promise((r) => setTimeout(r, 250));
  }
}

/** TOTP (RFC 6238, SHA-1, 30 s, 6 digits) for a base32 secret. */
export async function totp(secret, offset = 0) {
  const { createHmac } = await import("node:crypto");
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const c of secret.replace(/=+$/, "").toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, "0");
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const counter = Math.floor(Date.now() / 1000 / 30) + offset;
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(counter));
  const h = createHmac("sha1", key).update(msg).digest();
  const o = h[h.length - 1] & 0xf;
  const n = ((h.readUInt32BE(o) & 0x7fffffff) % 1_000_000).toString().padStart(6, "0");
  return n;
}

/** Opens a ticket from the inbox list by subject (retrying while the list re-renders). */
export async function openTicket(page, subject) {
  const row = page.getByRole("option").filter({ hasText: subject }).first();
  await row.waitFor({ timeout: 15_000 });
  await until(async () => {
    if (/\/[0-9a-f-]{36}$/.test(page.url()) && (await page.getByRole("button", { name: "More actions" }).count())) return true;
    await row.click().catch(() => {});
    await page.waitForTimeout(400);
    return false;
  }, `open ticket ${subject}`);
}
