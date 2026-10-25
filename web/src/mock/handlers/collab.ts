// Collab in mock mode (contracts/collab.md): mail accounts members connected, their
// folders, conversations and messages, shares, assignees, comments and each
// member's sidebar. Seeded lazily per workspace from its own RNG, so the rest of
// the seed stays the same. `?mock_collab=off` (or `plan`) shows Collab turned off.

import type {
  Attachment,
  CollabStatus,
  MailAccount,
  MailAddress,
  MailAttachment,
  MailAudience,
  MailComment,
  MailFolder,
  MailFolderRole,
  MailHistory,
  MailIdentity,
  MailMessage,
  MailMessageDetails,
  MailPerson,
  MailThread,
  MailThreadAction,
  MailThreadDetail,
  SidebarPrefs,
} from "@/lib/api/types";
import { collabFromName } from "@/lib/collab/identities";
import { LABEL_TONES, toneHex, type LabelTone } from "@/lib/collab/labelTones";
import { defaultSidebarPrefs, normalizePrefs } from "@/lib/collab/sidebarPrefs";
import { COLLAB_FROM_VARS, fromNameProblem, hasFromVars } from "@/lib/fromname";
import { db, me as meUser, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { del, get, HttpError, invalid, notFound, patch, post, put, respond, strArr, type Ctx } from "../router";
import { escapeHtml, isRecord, nowIso, uuid } from "../util";
import { collabSignatureProblems, fillForSender } from "./templates";

/** The account's addresses besides its own live in `aliases`; its own is built from the account (contracts/collab.md "Addresses"). */
type Account = Omit<MailAccount, "identities" | "default_from"> & { ownerId: string | null; aliases: MailIdentity[]; defaultFrom: string };
type ThreadMeta = { id: string; account_id: string; subject: string; shares: MailAudience; assignees: string[]; comments: MailComment[] };
type State = { accounts: Account[]; folders: MailFolder[]; threads: Map<string, ThreadMeta>; messages: MailMessage[]; prefs: Record<string, SidebarPrefs> };

const states = new Map<string, State>();
/** Gmail's own copy of each two-way signature, by account id ("Get from Gmail"). */
const providerSignature = new Map<string, string>();
/** Gmail's own From name of each address, by account or address id ("Get from Gmail"). */
const providerName = new Map<string, string>();
const emptyAudience = (): MailAudience => ({ everyone: false, team_ids: [], user_ids: [] });

// Its own PRNG: the shared one drives the rest of the seed.
let rs = 0x51c0ab;
function r() {
  rs = (rs + 0x6d2b79f5) | 0;
  let t = Math.imul(rs ^ (rs >>> 15), 1 | rs);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
}
const pickR = <T,>(xs: readonly T[]) => xs[Math.floor(r() * xs.length)];
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

const PEOPLE: MailAddress[] = [
  { name: "Elena Ruiz", email: "elena@northwind.com" },
  { name: "Marcus Lee", email: "marcus@halcyon.studio" },
  { name: "Aisha Patel", email: "aisha@brightline.io" },
  { name: "Tom Becker", email: "tom@oakridgehealth.org" },
  { name: "Nora Kim", email: "nora@paperplane.co" },
  { name: "Daniel Svensson", email: "daniel@fjord.legal" },
  { name: "Grace Ho", email: "grace@lumen.vc" },
  { name: "Stripe", email: "receipts@stripe.com" },
  { name: "GitHub", email: "noreply@github.com" },
  { name: "Linear", email: "notifications@linear.app" },
];

const STORIES: { subject: string; body: string; designed?: boolean; attach?: boolean }[] = [
  { subject: "Q4 renewal and seat count", body: "Hi there,\n\nWe are looking at the renewal for next year. Could you send over a quote for 260 seats with the annual discount?\n\nThanks,\nElena" },
  { subject: "Contract redlines attached", body: "Please find our redlines attached. The main change is the liability cap in section 9.\n\nBest,\nDaniel", attach: true },
  { subject: "Intro: Margin x Lumen", body: "Hi both,\n\nAs promised, connecting you two. Grace leads platform investments at Lumen and has been following Margin for a while.\n\nI'll let you take it from here." },
  { subject: "Your receipt from Margin Inc.", body: "Receipt #2291-4410\n\nAmount paid: $1,240.00\nDate paid: Oct 12, 2026", designed: true },
  { subject: "Design review on Thursday?", body: "Would Thursday at 3pm work for the export flow review? I can share the Figma beforehand.\n\nMarcus" },
  { subject: "Shipment delayed at the border", body: "Quick heads up: the hardware shipment is held at customs. We expect it to clear by Monday.\n\nAisha", attach: true },
  { subject: "[margin/web] Fix sign-in redirect loop (#4821)", body: "@maya requested your review on this pull request.\n\nFixes the redirect loop when the session expires mid-flow.", designed: true },
  { subject: "Offsite agenda draft", body: "Here is the first draft of the offsite agenda. Add anything I missed before Friday.\n\nNora" },
  { subject: "Security questionnaire", body: "Our compliance team sent over the attached questionnaire. Any chance you can fill it in by the end of the month?\n\nTom", attach: true },
  { subject: "Lunch next week", body: "Free for lunch next Tuesday? There's a new ramen place near the office." },
  { subject: "Invoice overdue reminder", body: "This is a friendly reminder that invoice INV-0042 is now 5 days overdue." },
  { subject: "Feedback on the new dashboard", body: "Love the new dashboard. One thing: the date picker is hard to use on a laptop trackpad.\n\nNora" },
];

const designed = (title: string, text: string) =>
  `<table width="100%" cellpadding="0" cellspacing="0" bgcolor="#f6f9fc"><tr><td align="center" style="padding:24px"><table width="560" cellpadding="0" cellspacing="0" bgcolor="#ffffff" style="border-radius:8px"><tr><td style="padding:28px;font-family:Helvetica,Arial,sans-serif;color:#1f2328"><h2 style="margin:0 0 12px">${escapeHtml(title)}</h2><p style="margin:0 0 16px;line-height:1.5">${escapeHtml(text).replace(/\n/g, "<br>")}</p><img src="https://images.example.com/track.gif" width="1" height="1" alt=""><p style="margin:0;color:#6a737d;font-size:12px">You are receiving this because you have an account.</p></td></tr></table></td></tr></table>`;

type Quoted = { html: string; text: string; at: string; from: MailAddress };
const attribution = (q: Quoted) => {
  const d = new Date(q.at);
  const day = d.toLocaleDateString("en-US", { weekday: "short", month: "short", day: "numeric", year: "numeric" });
  const time = d.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" }).toLowerCase();
  return `On ${day} at ${time}, ${q.from.name || q.from.email} <${q.from.email}> wrote:`;
};
const quoteOf = (q: Quoted) =>
  `<br><div class="gmail_quote gmail_quote_container"><div dir="ltr" class="gmail_attr">${escapeHtml(attribution(q))}<br></div><blockquote class="gmail_quote" style="margin:0px 0px 0px 0.8ex;border-left:1px solid rgb(204,204,204);padding-left:1ex">${q.html}</blockquote></div>`;

const fileSvg = (label: string) =>
  `data:image/svg+xml;utf8,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400"><rect width="640" height="400" fill="#eef2f7"/><text x="32" y="210" font-family="Helvetica" font-size="28" fill="#5b6472">${label}</text></svg>`)}`;

/** A Gmail send-as address of the mock's Gmail account: Gmail's changes come in, edits here stay here. */
function alias(email: string, display_name: string, signature_html: string, p: Partial<MailIdentity> = {}): MailIdentity {
  return {
    id: uuid(),
    email,
    display_name,
    signature_html,
    reply_to: "",
    primary: false,
    default: false,
    source: "provider",
    verification: "accepted",
    can_send: true,
    signature_sync: "pull",
    display_name_sync: "pull",
    signature_synced_at: new Date(Date.now() - 6 * MIN).toISOString(),
    ...p,
  };
}

function folder(account_id: string, name: string, role: MailFolderRole | null, kind: "folder" | "label" = "folder", path = name, color: string | null = null): MailFolder {
  return { id: uuid(), account_id, name, path, role, kind, unread: 0, total: 0, color, can_rename: !role };
}

// The Margin wordmark a signature carries, as Gmail would host it.
const MARGIN_WORDMARK = `data:image/svg+xml;utf8,${encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" width="240" height="64" viewBox="0 0 240 64"><rect x="4" y="4" width="56" height="56" rx="12" fill="#4f46e5"/>' +
    '<rect x="16" y="4" width="5" height="56" fill="#ffffff" opacity="0.35"/><rect x="28" y="20" width="22" height="6" rx="3" fill="#ffffff" opacity="0.8"/><rect x="28" y="34" width="16" height="6" rx="3" fill="#ffffff" opacity="0.8"/>' +
    '<text x="76" y="44" font-family="Arial, Helvetica, sans-serif" font-size="34" font-weight="700" fill="#111827" letter-spacing="-1">Margin</text></svg>',
)}`;

/** A signature as Gmail keeps one made with a signature generator: a layout table, inline styles, a sized logo. */
function gmailSignature(name: string, email: string): string {
  const grey = "color:#6b7280";
  return (
    `<div dir="ltr"><div>Thanks,</div><br>` +
    `<table cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;font-family:Arial,Helvetica,sans-serif;font-size:13px;line-height:18px;color:#1f2328"><tbody>` +
    `<tr><td style="font-size:14px"><b>${escapeHtml(name)}</b></td></tr>` +
    `<tr><td style="${grey}">Customer success &amp; onboarding</td></tr>` +
    `<tr><td style="padding:10px 0 8px"><img src="${MARGIN_WORDMARK}" width="120" height="32" alt="Margin"></td></tr>` +
    `<tr><td><a href="mailto:${email}" style="color:#4f46e5;text-decoration:none">${email}</a> <span style="color:#d1d5db">·</span> <a href="https://margin.app" style="color:#4f46e5;text-decoration:none">margin.app</a></td></tr>` +
    `<tr><td style="${grey}">+1 415 555 0134</td></tr>` +
    `<tr><td style="padding-top:10px;color:#9ca3af;font-size:11px;line-height:15px">Margin Labs, Inc. · 548 Market St, San Francisco, CA 94104 · Registered in Delaware No. 7700001</td></tr>` +
    `</tbody></table></div>`
  );
}

/** A team address's designed signature, as Gmail keeps it: a coloured rule, the logo, a line of links. */
function teamSignature(team: string, line: string, link: string, accent: string): string {
  return (
    `<table cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;font-family:Arial,Helvetica,sans-serif;font-size:13px;line-height:18px;color:#1f2328"><tbody><tr>` +
    `<td style="border-left:3px solid ${accent};padding:2px 0 2px 12px">` +
    `<img src="${MARGIN_WORDMARK}" width="96" height="26" alt="Margin"><br>` +
    `<b style="font-size:14px">${escapeHtml(team)}</b><br>` +
    `<span style="color:#6b7280">${escapeHtml(line)}</span><br>` +
    `<a href="https://${link}" style="color:${accent};text-decoration:none">${escapeHtml(link)}</a>` +
    `</td></tr></tbody></table>`
  );
}

function seedState(ws: WsData): State {
  const me = db.meId;
  const memberIds = ws.members.map((m) => m.user_id);
  const other = memberIds.find((id) => id !== me) ?? me;
  const meUser = db.users.find((u) => u.id === me);
  const local = (meUser?.email ?? "me@margin.app").split("@")[0];
  // Each workspace's accounts are at its own domain (Together shows them side by side).
  const domain = ws.mailboxes[0]?.email.split("@")[1] ?? "margin.app";
  const org = ws.workspace.name.split(" ")[0];
  const now = Date.now();
  const mk = (p: Partial<Account> & Pick<Account, "provider" | "email" | "name" | "color" | "visibility" | "ownerId">): Account => ({
    id: uuid(),
    display_name: meUser?.name ?? "",
    owner: userRef(p.ownerId),
    shared_with: emptyAudience(),
    signature_html: "",
    status: "active",
    reconnect: false,
    last_error: "",
    last_synced_at: new Date(now - 2 * MIN).toISOString(),
    can_manage: true,
    unread: 0,
    created_at: new Date(now - 40 * DAY).toISOString(),
    signature_sync: "none",
    signature_synced_at: null,
    display_name_sync: "none",
    out_of_office: null,
    out_of_office_sync: "none",
    aliases: [],
    defaultFrom: "",
    history: null,
    oldest_message_at: new Date(now - 30 * DAY).toISOString(),
    folders_left_out: 0,
    ...p,
  });
  const work = mk({
    provider: "google",
    email: `${local}@${domain}`,
    name: "Work",
    color: "blue",
    visibility: "private",
    ownerId: me,
    // Synced with Gmail both ways (contracts/collab.md "Account settings").
    // Margin's Work account carries the designed Gmail signature; other workspaces a plain one.
    signature_html:
      domain === "margin.app"
        ? gmailSignature(meUser?.name ?? "", `${local}@margin.app`)
        : `<p><strong>${escapeHtml(meUser?.name ?? "")}</strong><br>Customer success, ${org}<br><a href="https://${domain}">${domain}</a> · +1 415 555 0134</p>`,
    signature_sync: "two_way",
    signature_synced_at: new Date(now - 6 * MIN).toISOString(),
    display_name_sync: "two_way",
    out_of_office: { enabled: false, start: null, end: null, subject: "Out of office", message_html: "", contacts_only: false, internal_only: false },
    out_of_office_sync: "two_way",
    // Gmail's send-as addresses: each with its own From name and signature, one still being confirmed.
    aliases: [
      alias("support@margin.app", "Margin Support", teamSignature("Margin Support", "We usually answer within a few hours.", "margin.app/help", "#4f46e5")),
      alias("billing@margin.app", "Margin Billing", teamSignature("Margin Billing", "Invoices and receipts", "margin.app/billing", "#0f766e"), { reply_to: "accounts@margin.app" }),
      alias("press@margin.app", "", "", { verification: "pending", can_send: false }),
    ],
  });
  const sales = mk({
    provider: "microsoft",
    email: `sales@${domain}`,
    name: "Sales",
    color: "teal",
    visibility: "shared",
    ownerId: other,
    shared_with: { everyone: true, team_ids: [], user_ids: [] },
    // A shared account whose mail names whoever sends it, with their own title (contracts/collab.md "Addresses").
    display_name: `{agent} at ${org} Sales`,
    signature_html: `<p><b>{{ agent.name }}</b><br>{{ agent.title }}, ${org} Sales<br>{{ mailbox.email }}</p>`,
    // Outlook: no signature API, a From name someone chose, automatic replies both ways (on now).
    out_of_office: {
      enabled: true,
      start: new Date(now - DAY).toISOString(),
      end: new Date(now + 4 * DAY).toISOString(),
      subject: "",
      message_html: "<p>Thanks for reaching out. The sales team is at a conference this week and will reply when we're back.</p>",
      contacts_only: false,
      internal_only: false,
    },
    out_of_office_sync: "two_way",
    // Its older mail coming in (contracts/collab.md "History").
    oldest_message_at: new Date(now - 412 * DAY).toISOString(),
    history: {
      status: "running",
      since: null,
      include_spam_trash: false,
      requested_by: userRef(other),
      started_at: new Date(now - 3 * HOUR).toISOString(),
      finished_at: null,
      reached: new Date(now - 412 * DAY).toISOString(),
      imported: 12_340,
      present: 210,
      too_large: 2,
      unreadable: 0,
      total: 38_600,
      last_error: "",
    },
  });
  const personal = mk({
    provider: "imap",
    email: `${local}@fastmail.com`,
    name: "Personal",
    color: "violet",
    visibility: "private",
    ownerId: me,
    status: "error",
    reconnect: true,
    last_error: "The server refused the password. Connect the account again.",
    folders_left_out: 4,
    imap: { imap_host: "imap.fastmail.com", imap_port: 993, imap_tls: "tls", imap_username: `${local}@fastmail.com`, smtp_host: "smtp.fastmail.com", smtp_port: 465, smtp_tls: "tls", smtp_username: `${local}@fastmail.com` },
  });
  const accounts = [work, sales, personal];
  providerSignature.set(work.id, work.signature_html);
  providerName.set(work.id, work.display_name);
  for (const x of work.aliases) {
    providerSignature.set(x.id!, x.signature_html);
    providerName.set(x.id!, x.display_name);
  }
  const folders: MailFolder[] = [];
  for (const a of accounts) {
    const roles: [string, MailFolderRole][] = [["Inbox", "inbox"], ["Starred", "starred"], ["Sent", "sent"], ["Drafts", "drafts"], ["Archive", "archive"], ["Spam", "spam"], ["Trash", "trash"]];
    for (const [n, role] of roles) folders.push(folder(a.id, n, role, a.provider === "google" ? "label" : "folder"));
  }
  folders.push(folder(work.id, "Clients", null, "label", "Clients", "#4a86e8"));
  folders.push(folder(work.id, "Acme", null, "label", "Clients/Acme", "#16a765"));
  folders.push(folder(work.id, "Northwind", null, "label", "Clients/Northwind", "#f691b3"));
  folders.push(folder(work.id, "Receipts", null, "label", "Receipts", "#ffad46"));
  folders.push(folder(sales.id, "Leads", null));
  folders.push(folder(sales.id, "Partners", null, "folder", "Leads/Partners"));
  // Outlook categories are the Microsoft account's labels.
  folders.push(folder(sales.id, "Red team", null, "label", "Red team", "#e74856"));
  folders.push(folder(sales.id, "Follow up", null, "label", "Follow up", "#ffb900"));
  folders.push(folder(personal.id, "Travel", null));

  const roleId = (accountId: string, role: MailFolderRole) => folders.find((f) => f.account_id === accountId && f.role === role)!.id;
  const userFolders = (accountId: string) => folders.filter((f) => f.account_id === accountId && !f.role);
  const threads = new Map<string, ThreadMeta>();
  const messages: MailMessage[] = [];

  // Mail from an account names whoever sent it (here, you); mail to it, the account.
  const sentAs = (a: Account) => filledName(ws, a, a.display_name);
  const writtenTo = (a: Account) => (hasFromVars(a.display_name) ? a.name : a.display_name);
  const addThread = (a: Account, i: number) => {
    const story = STORIES[(i + accounts.indexOf(a) * 3) % STORIES.length];
    const person = pickR(PEOPLE);
    const id = uuid();
    const meta: ThreadMeta = { id, account_id: a.id, subject: story.subject, shares: emptyAudience(), assignees: [], comments: [] };
    threads.set(id, meta);
    const n = 1 + Math.floor(r() * 3);
    const start = now - (i * 5 + r() * 4) * HOUR - (i > 8 ? i * DAY * 0.4 : 0);
    const place = i % 9 === 7 ? "spam" : i % 11 === 10 ? "trash" : i % 5 === 4 ? "archive" : "inbox";
    const labels = a.provider === "google" && r() < 0.4 ? [pickR(userFolders(a.id)).id] : [];
    let prev: Quoted | null = null;
    for (let k = 0; k < n; k++) {
      const fromUs = k % 2 === 1;
      const at = new Date(Math.min(start + k * 3 * HOUR, now - (n - k) * 7 * MIN)).toISOString();
      const own = fromUs ? `Thanks, ${person.name.split(" ")[0]}. I'll get back to you shortly.\n\n${sentAs(a)}` : story.body;
      const ownHtml = story.designed && !fromUs ? designed(story.subject, own) : `<p>${escapeHtml(own).replace(/\n\n/g, "</p><p>").replace(/\n/g, "<br>")}</p>`;
      // A reply carries the mail before it, quoted the way Gmail does.
      const html: string = prev ? `${ownHtml}${quoteOf(prev)}` : ownHtml;
      const text: string = prev ? `${own}\n\n${attribution(prev)}\n${prev.text.replace(/^/gm, "> ")}` : own;
      const from = fromUs ? { name: sentAs(a), email: a.email } : person;
      prev = { html, text, at, from };
      const attachments: MailAttachment[] =
        story.attach && !fromUs && k === 0
          ? [
              { id: "0", filename: "document.pdf", content_type: "application/pdf", size: 240_118, inline: false, content_id: "", url: "data:text/plain;charset=utf-8,Mock%20PDF" },
              { id: "1", filename: "photo.png", content_type: "image/png", size: 88_210, inline: false, content_id: "", url: fileSvg(story.subject) },
            ]
          : [];
      const last = k === n - 1;
      messages.push({
        id: uuid(),
        thread_id: id,
        account_id: a.id,
        message_id: `${uuid()}@mail.example.com`,
        from,
        to: fromUs ? [person] : [{ name: writtenTo(a), email: a.email }],
        cc: !fromUs && k === 0 && r() < 0.3 ? [pickR(PEOPLE)] : [],
        bcc: [],
        reply_to: [],
        subject: k === 0 ? story.subject : `Re: ${story.subject}`,
        snippet: own.replace(/\s+/g, " ").slice(0, 140),
        body_html: html,
        body_text: text,
        sent_at: at,
        unread: last && !fromUs && i < 6,
        starred: last && i % 7 === 2,
        draft: false,
        folder_ids: fromUs ? [roleId(a.id, "sent"), ...(place === "inbox" ? [roleId(a.id, "inbox")] : [])] : [roleId(a.id, place), ...labels],
        attachments,
        sent_by: fromUs && k === 1 && a.visibility === "shared" ? userRef(other) : null,
        outgoing: fromUs,
      });
    }
  };
  accounts.forEach((a) => {
    const count = a.id === personal.id ? 8 : 24;
    for (let i = 0; i < count; i++) addThread(a, i);
  });
  // A conversation shared on its own, one assigned to you, a comment.
  const salesThreads = [...threads.values()].filter((t) => t.account_id === sales.id);
  if (salesThreads[1]) {
    salesThreads[1].assignees = [me];
    salesThreads[1].comments.push({ id: uuid(), thread_id: salesThreads[1].id, author: userRef(other), body_html: `<p>Can you take this one? They asked about the annual discount.</p>`, mentions: [me], created_at: new Date(now - 2 * HOUR).toISOString(), updated_at: new Date(now - 2 * HOUR).toISOString() });
  }
  const workThreads = [...threads.values()].filter((t) => t.account_id === work.id);
  if (workThreads[2]) workThreads[2].shares = { everyone: false, team_ids: [], user_ids: [other] };
  // Categories on some of the shared account's conversations.
  const categories = folders.filter((f) => f.account_id === sales.id && f.kind === "label" && !f.role);
  salesThreads.forEach((t, i) => {
    if (i % 4 !== 1) return;
    const first = messages.find((m) => m.thread_id === t.id);
    if (first) first.folder_ids = [...first.folder_ids, categories[(i >> 2) % categories.length].id];
  });
  // Drafts: a reply being written in a conversation, and new mail not sent yet.
  const replyTo = workThreads[0] ? messages.filter((m) => m.thread_id === workThreads[0].id && m.from.email !== work.email).pop() : undefined;
  if (replyTo) {
    const id = uuid();
    const text = `Thanks, ${replyTo.from.name.split(" ")[0] || "there"}. I'll confirm the seat count by Friday.`;
    messages.push({
      ...replyTo,
      id,
      message_id: `${uuid()}@margin.app`,
      from: { name: work.display_name, email: work.email },
      to: [replyTo.from],
      cc: [],
      reply_to: [],
      subject: `Re: ${workThreads[0].subject}`,
      snippet: text,
      body_html: `<p>${escapeHtml(text)}</p><p></p><div class="od-signature">${work.signature_html}</div>`,
      body_text: text,
      sent_at: new Date(now - 25 * MIN).toISOString(),
      unread: false,
      starred: false,
      draft: true,
      draft_id: id,
      in_reply_to_id: replyTo.id,
      folder_ids: [roleId(work.id, "drafts")],
      attachments: [],
      sent_by: userRef(me),
      outgoing: true,
    });
  }
  {
    const tid = uuid();
    const id = uuid();
    const subject = "Offsite venue options";
    const text = "Hi Nora, here are the three venues I liked for the offsite. The lake house fits everyone and has a good room for the planning day.";
    threads.set(tid, { id: tid, account_id: work.id, subject, shares: emptyAudience(), assignees: [], comments: [] });
    messages.push({
      id,
      thread_id: tid,
      account_id: work.id,
      message_id: `${uuid()}@margin.app`,
      from: { name: work.display_name, email: work.email },
      to: [PEOPLE[4]],
      cc: [],
      bcc: [],
      reply_to: [],
      subject,
      snippet: text.slice(0, 140),
      body_html: `<p>${escapeHtml(text)}</p><p></p><div class="od-signature">${work.signature_html}</div>`,
      body_text: text,
      sent_at: new Date(now - 3 * HOUR).toISOString(),
      unread: false,
      starred: false,
      draft: true,
      draft_id: id,
      in_reply_to_id: null,
      folder_ids: [roleId(work.id, "drafts")],
      attachments: [],
      sent_by: userRef(me),
      outgoing: true,
    });
  }

  // A follow-up that quotes the one before it under its signature, as most mail clients send it.
  {
    const tid = uuid();
    const subject = "Your application to Fieldway";
    const from = { name: "Jade Morrow", email: "jade@fieldway.io" };
    const sig = `<div>Jade Morrow<br><b>Growth @ Fieldway</b><br>28 W 23rd St, New York, NY 10010</div><br><div>Not the right time for Fieldway? Reply and I'll stop reaching out.</div><br><br><br>`;
    const first = { text: "Matthew,\n\nHaven't heard from you, are you still interested in Fieldway?\n\nFeel free to reply with a simple yes or no.\n\nBest,\nJade", at: new Date(now - 4 * DAY).toISOString(), from };
    const second = "Hi Matthew,\n\nI was curious to ask: what's holding you back from completing your application? It looks like you started it a few days ago but haven't submitted it yet.\n\nIf you have any questions, send me a quick note. Happy to answer by email or jump on a quick call.\n\nBest,\nJade";
    const html = (text: string) => `<div dir="ltr">${text.split("\n\n").map((p) => `<div>${escapeHtml(p).replace(/\n/g, "<br>")}</div><div><br></div>`).join("")}${sig}</div>`;
    threads.set(tid, { id: tid, account_id: work.id, subject, shares: emptyAudience(), assignees: [], comments: [] });
    const msg = (text: string, body_html: string, at: string, unread: boolean, sub: string): MailMessage => ({
      id: uuid(),
      thread_id: tid,
      account_id: work.id,
      message_id: `${uuid()}@fieldway.io`,
      from,
      to: [{ name: work.display_name, email: work.email }],
      cc: [],
      bcc: [],
      reply_to: [],
      subject: sub,
      snippet: text.replace(/\s+/g, " ").slice(0, 140),
      body_html,
      body_text: text,
      sent_at: at,
      unread,
      starred: false,
      draft: false,
      folder_ids: [roleId(work.id, "inbox")],
      attachments: [],
      sent_by: null,
      outgoing: false,
    });
    const firstHtml = html(first.text);
    messages.push(msg(first.text, firstHtml, first.at, false, subject));
    messages.push(msg(second, `${html(second)}${quoteOf({ ...first, html: firstHtml })}`, new Date(now - 12 * MIN).toISOString(), true, `Re: ${subject}`));
  }

  // Mail that only claims to be from PayPal: it fails DMARC, so it carries a warning.
  {
    const tid = uuid();
    const subject = "Your account access has been limited";
    const text = "We noticed unusual activity on your account. To restore access, confirm your details within 24 hours.\n\nConfirm your account: https://paypal-review.example/confirm\n\nPayPal Security";
    threads.set(tid, { id: tid, account_id: work.id, subject, shares: emptyAudience(), assignees: [], comments: [] });
    messages.push({
      id: uuid(),
      thread_id: tid,
      account_id: work.id,
      message_id: `${uuid()}@mail-relay.top`,
      from: { name: "PayPal", email: "service@paypal.com" },
      to: [{ name: work.display_name, email: work.email }],
      cc: [],
      bcc: [],
      reply_to: [{ name: "", email: "support-desk@mail-relay.top" }],
      subject,
      snippet: text.replace(/\s+/g, " ").slice(0, 140),
      body_html: `<p>${escapeHtml(text).replace(/\n\n/g, "</p><p>").replace(/\n/g, "<br>")}</p>`,
      body_text: text,
      sent_at: new Date(now - 95 * MIN).toISOString(),
      unread: true,
      starred: false,
      draft: false,
      folder_ids: [roleId(work.id, "inbox")],
      attachments: [],
      sent_by: null,
      outgoing: false,
    });
  }

  // A customer writing to one of the Gmail account's send-as addresses (support@): a reply goes from it.
  {
    const tid = uuid();
    const subject = "Export to CSV keeps timing out";
    const from = { name: "Priya Shah", email: "priya@cobalt.dev" };
    const text = "Hi Margin team,\n\nExporting our workspace to CSV times out after about a minute. It worked last week. Is there a size limit we hit?\n\nThanks,\nPriya";
    threads.set(tid, { id: tid, account_id: work.id, subject, shares: emptyAudience(), assignees: [], comments: [] });
    messages.push({
      id: uuid(),
      thread_id: tid,
      account_id: work.id,
      message_id: `${uuid()}@cobalt.dev`,
      from,
      to: [{ name: "Margin Support", email: "support@margin.app" }],
      cc: [],
      bcc: [],
      reply_to: [],
      subject,
      snippet: text.replace(/\s+/g, " ").slice(0, 140),
      body_html: `<p>${escapeHtml(text).replace(/\n\n/g, "</p><p>").replace(/\n/g, "<br>")}</p>`,
      body_text: text,
      sent_at: new Date(now - 12 * MIN).toISOString(),
      unread: true,
      starred: false,
      draft: false,
      folder_ids: [roleId(work.id, "inbox")],
      attachments: [],
      sent_by: null,
      outgoing: false,
    });
  }

  // A long newsletter in the shared inbox, with the team discussing it below.
  {
    const tid = uuid();
    const subject = "Wednesday Web Drop";
    const from = { name: "Webdrop", email: "newsletter@webdrop.dev" };
    const text = "New web apps this week. Featuring Base44, Plane, and Quo.";
    const at = (ago: number) => new Date(now - ago).toISOString();
    const comment = (author: string, body: string, ago: number): MailComment => ({ id: uuid(), thread_id: tid, author: userRef(author), body_html: `<p>${body}</p>`, mentions: [], created_at: at(ago), updated_at: at(ago) });
    threads.set(tid, {
      id: tid,
      account_id: sales.id,
      subject,
      shares: emptyAudience(),
      assignees: [],
      comments: [comment(other, "Plane and Quo both sell to teams our size. Worth a look for the partner list?", 14 * MIN), comment(me, "Agreed, I'll add Plane to the Leads folder.", 9 * MIN)],
    });
    messages.push({
      id: uuid(),
      thread_id: tid,
      account_id: sales.id,
      message_id: `${uuid()}@webdrop.dev`,
      from,
      to: [{ name: writtenTo(sales), email: sales.email }],
      cc: [],
      bcc: [],
      reply_to: [],
      subject,
      snippet: text,
      body_html: newsletter(),
      body_text: text,
      sent_at: at(20 * MIN),
      unread: true,
      starred: false,
      draft: false,
      folder_ids: [roleId(sales.id, "inbox")],
      attachments: [],
      sent_by: null,
      outgoing: false,
    });
  }

  // A teammate's own private account, which only she sees; her Ollie answers from it when asked
  // (contracts/ai.md "Asking a teammate"; handlers/assistantPeer.ts).
  const priya = db.users.find((u) => u.email === "priya@margin.app" && ws.members.some((m) => m.user_id === u.id));
  if (priya) {
    const pa = mk({ provider: "google", email: "priya@margin.app", name: "Work", color: "green", visibility: "private", ownerId: priya.id, display_name: priya.name });
    accounts.push(pa);
    for (const role of ["inbox", "sent", "archive"] as const) folders.push(folder(pa.id, role[0].toUpperCase() + role.slice(1), role, "label"));
  }

  return { accounts, folders, threads, messages, prefs: {} };
}

/** A member's own mail accounts (never ones shared with them), for agent-to-agent; none while Collab is off. */
export function ownMailAccounts(ws: WsData, uid: string): Pick<MailAccount, "id" | "email" | "name" | "provider">[] {
  if (!status(ws).enabled) return [];
  return state(ws)
    .accounts.filter((a) => a.ownerId === uid)
    .map((a) => ({ id: a.id, email: a.email, name: a.name, provider: a.provider }));
}

/** Whether the workspace has Collab in the mock (`?mock_collab=off` turns it off). */
export function collabOn(ws: WsData): boolean {
  return status(ws).enabled;
}

/** A long designed newsletter: the kind of mail that pushes everything under it out of sight. */
function newsletter() {
  const apps: [string, string][] = [
    ["Base44", "A no-code AI platform for building apps, websites and AI agents."],
    ["Plane", "Work infrastructure for humans and AI agents."],
    ["Quo", "Calls, texts and contacts together in one shared space."],
    ["Mercury", "Online business banking."],
    ["Voicenotes", "An AI notetaker that works in the background."],
    ["Tally", "Create beautiful forms for free."],
    ["Cal", "Scheduling infrastructure for everyone."],
    ["Raycast", "A launcher for everything on your Mac."],
  ];
  const row = ([name, line]: [string, string]) =>
    `<tr><td style="padding:14px 0;border-bottom:1px solid #eceef1"><table cellpadding="0" cellspacing="0"><tr><td width="44" height="44" bgcolor="#f1f3f5" style="border-radius:10px"></td><td style="padding-left:14px"><div style="font-weight:600;font-size:14px">${name}</div><div style="color:#5b6472;font-size:13px;margin-top:2px">${line}</div></td></tr></table></td></tr>`;
  return `<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:48px 16px"><table width="520" cellpadding="0" cellspacing="0" style="font-family:Helvetica,Arial,sans-serif;color:#1f2328"><tr><td style="padding-bottom:28px;border-bottom:1px solid #dfe2e6"><h1 style="margin:0;font-size:22px">New web apps this week.</h1><p style="margin:6px 0 0;font-size:18px;color:#5b6472">Featuring Base44, Plane, and Quo.</p></td></tr>${apps
    .slice(0, 3)
    .map(([n, l]) => `<tr><td style="padding:22px 0 0;font-size:13px;line-height:1.6;color:#3b424b"><strong>${n}</strong> ${l}</td></tr>`)
    .join("")}<tr><td style="padding-top:36px"><table width="100%" cellpadding="0" cellspacing="0">${apps.map(row).join("")}</table></td></tr><tr><td align="center" style="padding:32px 0"><a href="https://webdrop.dev" style="display:block;background:#111;color:#fff;text-decoration:none;font-weight:600;font-size:14px;padding:14px;border-radius:999px">View on Webdrop</a></td></tr><tr><td style="padding-top:24px;border-top:1px solid #eceef1;font-size:12px;line-height:1.6;color:#8a929c">You are receiving this because you subscribed to the Wednesday Web Drop.<br>Webdrop, 548 Market St, San Francisco, CA</td></tr></table></td></tr></table>`;
}

function state(ws: WsData): State {
  let s = states.get(ws.workspace.id);
  if (!s) {
    s = seedState(ws);
    states.set(ws.workspace.id, s);
  }
  return s;
}

function status(ws: WsData): CollabStatus {
  const p = new URLSearchParams(location.search).get("mock_collab");
  const off = p === "off" || p === "plan";
  return { enabled: !off, reason: off ? (p === "plan" ? "plan" : "disabled") : null, providers: { google: true, microsoft: true, imap: true }, accounts: off ? 0 : visibleAccounts(ws).length, storage: storage() };
}

/**
 * The workspace's Collab storage (contracts/collab.md "Storage"). Unlimited, as on a
 * self-hosted instance, unless ?mock_storage= says otherwise: "cloud" (an allowance with
 * room), "limited" (at it: history imports wait) or "full" (past twice it: new mail waits).
 */
function storage(): CollabStatus["storage"] {
  const mode = new URLSearchParams(location.search).get("mock_storage");
  const limit = 3 * 25 * 1024 ** 3;
  if (mode === "limited") return { used_bytes: limit + 120 * 1024 ** 2, limit_bytes: limit, history_limited: true, full: false };
  if (mode === "full") return { used_bytes: 2 * limit + 5 * 1024 ** 2, limit_bytes: limit, history_limited: true, full: true };
  if (mode === "cloud") return { used_bytes: 9.4 * 1024 ** 3, limit_bytes: limit, history_limited: false, full: false };
  return { used_bytes: 9.4 * 1024 ** 3, limit_bytes: 0, history_limited: false, full: false };
}

/** A history import moving on: a batch every couple of seconds while it runs, until its total. */
const historyTimers = new Map<string, ReturnType<typeof setInterval>>();
function runHistory(ws: WsData, a: Account) {
  clearInterval(historyTimers.get(a.id));
  historyTimers.delete(a.id);
  const h = a.history;
  if (!h || h.status !== "running") return;
  if (storage().history_limited) {
    h.status = "limited";
    return;
  }
  historyTimers.set(
    a.id,
    setInterval(() => {
      const cur = a.history;
      if (!cur || cur.status !== "running") return void clearInterval(historyTimers.get(a.id));
      const total = cur.total ?? 4_000;
      const step = Math.min(Math.max(total - (cur.imported + cur.present + cur.too_large + cur.unreadable), 0), 640);
      cur.imported += Math.round(step * 0.96);
      cur.present += step - Math.round(step * 0.96);
      const floor = cur.since ? new Date(cur.since).getTime() : Date.now() - 6 * 365 * DAY;
      const reached = Math.max(floor, (cur.reached ? new Date(cur.reached).getTime() : Date.now()) - 21 * DAY);
      cur.reached = new Date(reached).toISOString();
      if (!a.oldest_message_at || cur.reached < a.oldest_message_at) a.oldest_message_at = cur.reached;
      if (cur.imported + cur.present + cur.too_large + cur.unreadable >= total) {
        cur.status = "done";
        cur.finished_at = nowIso();
        clearInterval(historyTimers.get(a.id));
        historyTimers.delete(a.id);
      }
      emit(ws, "collab.accounts", { account_id: a.id }, audienceOf(ws, a.id, []));
    }, 2_000),
  );
}

/** GET /v1/collab/counts for a workspace, or null when it has no Collab (the overview, handlers/together.ts). */
export function collabCountsFor(ws: WsData) {
  return status(ws).enabled ? counts(ws) : null;
}

function requireCollab(ws: WsData) {
  if (!status(ws).enabled) throw new HttpError(402, "collab_required", "Collab is not on this workspace's plan.");
}

const teamsOf = (ws: WsData, uid: string) => ws.teams.filter((t) => t.member_ids?.includes(uid)).map((t) => t.id);
const inAudience = (ws: WsData, a: MailAudience, uid: string) => a.everyone || a.user_ids.includes(uid) || a.team_ids.some((t) => teamsOf(ws, uid).includes(t));
const roleOf = (ws: WsData, uid: string) => ws.members.find((m) => m.user_id === uid)?.role ?? "viewer";
const isAdmin = (ws: WsData, uid: string) => ["owner", "admin"].includes(roleOf(ws, uid));
const canActRole = (ws: WsData, uid: string) => roleOf(ws, uid) !== "viewer";

function seesAccount(ws: WsData, a: Account, uid: string) {
  if (a.ownerId === uid) return true;
  return a.visibility === "shared" && inAudience(ws, a.shared_with, uid);
}

function visibleAccounts(ws: WsData): Account[] {
  const s = state(ws);
  const me = db.meId;
  return s.accounts.filter((a) => seesAccount(ws, a, me)).sort((a, b) => Number(b.ownerId === me) - Number(a.ownerId === me));
}

function seesThread(ws: WsData, t: ThreadMeta, uid: string) {
  const a = state(ws).accounts.find((x) => x.id === t.account_id);
  if (a && seesAccount(ws, a, uid)) return true;
  return inAudience(ws, t.shares, uid) || t.assignees.includes(uid);
}

function accountOut(ws: WsData, a: Account): MailAccount {
  const me = db.meId;
  const { ownerId, aliases: _aliases, defaultFrom: _default, ...rest } = a;
  const manage = (!!ownerId && ownerId === me) || (a.visibility === "shared" && isAdmin(ws, me));
  return { ...rest, owner: userRef(ownerId), can_manage: manage, unread: unreadInbox(ws, a.id), imap: manage ? a.imap : undefined, identities: identitiesOut(ws, a), default_from: defaultOf(a) };
}

/** An address of the account that can send, if email is one. */
function sendableAddress(a: Account, email: string | undefined): MailIdentity | undefined {
  const e = (email ?? "").trim().toLowerCase();
  if (!e) return undefined;
  return rawIdentities(a).find((i) => i.email === e && i.can_send);
}
const defaultOf = (a: Account) => (a.defaultFrom && a.aliases.some((x) => x.email === a.defaultFrom && x.can_send) ? a.defaultFrom : a.email.toLowerCase());

/** Its own address first (the account's From name and signature), then the others, as stored. */
function rawIdentities(a: Account): MailIdentity[] {
  const def = defaultOf(a);
  const own: MailIdentity = {
    id: null,
    email: a.email.toLowerCase(),
    display_name: a.display_name,
    signature_html: a.signature_html,
    reply_to: "",
    primary: true,
    default: false,
    source: "account",
    verification: "accepted",
    can_send: true,
    signature_sync: a.signature_sync,
    display_name_sync: a.display_name_sync,
    signature_synced_at: a.signature_synced_at,
  };
  return [own, ...a.aliases].map((i) => ({ ...i, default: i.email === def }));
}

const templated = (sig: string) => /\{\{|\{%/.test(sig);

/** A From name as your mail carries it: {agent} is you, {mailbox} the account's name. */
function filledName(ws: WsData, a: Account, format: string): string {
  return hasFromVars(format) ? collabFromName(format, { agent: meUser().name, account: a.name || a.email, workspace: ws.workspace.name }) : format;
}

/** The addresses for the API: each From name and signature filled for you, and whether Gmail keeps its own. */
function identitiesOut(ws: WsData, a: Account): MailIdentity[] {
  const google = a.provider === "google";
  return rawIdentities(a).map((i) => {
    const from_name = filledName(ws, a, i.display_name || a.display_name);
    return {
      ...i,
      from_name,
      filled_signature_html: templated(i.signature_html) ? fillForSender(ws, i.signature_html, { name: from_name, email: i.email }) : null,
      display_name_local: google && hasFromVars(i.display_name),
      signature_local: google && templated(i.signature_html),
    };
  });
}

/** A From name and signature as the server checks them (422 with the first problem). */
function checkSender(name: unknown, signature: unknown) {
  if (typeof name === "string" && /[{}]/.test(name)) {
    const problem = fromNameProblem(name, COLLAB_FROM_VARS);
    if (problem === "variable") throw invalid("display_name takes only the variables {agent}, {agent_first}, {mailbox} and {workspace}");
    if (problem) throw invalid(problem === "long" ? "display_name must be 80 characters or less" : "display_name cannot contain < or > characters");
  }
  if (typeof signature === "string") {
    const p = collabSignatureProblems(signature)[0];
    if (p) throw invalid(p.message);
  }
}

/** The account's address a message was sent from or written to. */
function ownAddressOf(a: Account, m: MailMessage): string | null {
  const own = new Set(rawIdentities(a).map((i) => i.email));
  return [m.from, ...m.to, ...m.cc].map((x) => x.email.toLowerCase()).find((e) => own.has(e)) ?? null;
}

/** The From of mail sent from the account: the chosen address (422 when it is not one that can send), else for a reply the one it answers, else the default. */
function senderFor(ws: WsData, a: Account, fromEmail: unknown, replied?: MailMessage): MailAddress {
  // The From name names whoever sends (a format like "{agent} at Margin Sales").
  if (typeof fromEmail === "string" && fromEmail.trim()) {
    const i = sendableAddress(a, fromEmail);
    if (!i) throw invalid("Choose one of the account's addresses to send from.");
    return { name: filledName(ws, a, i.display_name || a.display_name), email: i.email };
  }
  const i = sendableAddress(a, replied ? (ownAddressOf(a, replied) ?? undefined) : undefined) ?? sendableAddress(a, defaultOf(a))!;
  return { name: filledName(ws, a, i.display_name || a.display_name), email: i.email };
}

const msgsOf = (s: State, threadId: string) => s.messages.filter((m) => m.thread_id === threadId).sort((a, b) => a.sent_at.localeCompare(b.sent_at));

function threadOut(ws: WsData, t: ThreadMeta): MailThread {
  const s = state(ws);
  const ms = msgsOf(s, t.id);
  const last = ms[ms.length - 1];
  const a = s.accounts.find((x) => x.id === t.account_id);
  const people: MailAddress[] = [];
  for (const m of [...ms].reverse()) for (const p of [m.from, ...m.to, ...m.cc]) if (!people.some((x) => x.email === p.email)) people.push(p);
  const mine = new Set(a ? rawIdentities(a).map((i) => i.email) : []);
  const own = people.filter((p) => mine.has(p.email.toLowerCase()));
  const others = people.filter((p) => !mine.has(p.email.toLowerCase()));
  const aliases = new Set(a?.aliases.map((x) => x.email) ?? []);
  return {
    id: t.id,
    account_id: t.account_id,
    subject: t.subject,
    snippet: last?.snippet ?? "",
    participants: [...others, ...own].slice(0, 10),
    last_message_at: last?.sent_at ?? nowIso(),
    message_count: ms.length,
    unread: ms.some((m) => m.unread),
    starred: ms.some((m) => m.starred),
    has_attachments: ms.some((m) => m.attachments.some((x) => !x.inline)),
    folder_ids: [...new Set(ms.flatMap((m) => m.folder_ids))],
    shared: t.shares.everyone || t.shares.team_ids.length > 0 || t.shares.user_ids.length > 0,
    assignees: t.assignees.map((id) => userRef(id)).filter((x): x is NonNullable<typeof x> => !!x),
    comment_count: t.comments.length,
    to_address: people.map((p) => p.email.toLowerCase()).find((e) => aliases.has(e)) ?? null,
  };
}

function detailOut(ws: WsData, t: ThreadMeta): MailThreadDetail {
  const s = state(ws);
  const a = s.accounts.find((x) => x.id === t.account_id)!;
  return {
    thread: threadOut(ws, t),
    account: { id: a.id, email: a.email, name: a.name, display_name: a.display_name, color: a.color, provider: a.provider, signature_html: a.signature_html, identities: identitiesOut(ws, a), default_from: defaultOf(a) },
    messages: msgsOf(s, t.id).map((m) => ({ ...m, own_address: ownAddressOf(a, m) })),
    comments: t.comments,
    shares: t.shares,
    can_act: canActRole(ws, db.meId),
  };
}

const roleIdOf = (s: State, accountId: string, role: MailFolderRole) => s.folders.find((f) => f.account_id === accountId && f.role === role)?.id;
function inRole(s: State, t: MailThread, role: MailFolderRole) {
  const id = roleIdOf(s, t.account_id, role);
  return !!id && t.folder_ids.includes(id);
}
function unreadInbox(ws: WsData, accountId: string) {
  const s = state(ws);
  return [...s.threads.values()].filter((t) => t.account_id === accountId).map((t) => threadOut(ws, t)).filter((t) => t.unread && inRole(s, t, "inbox")).length;
}

function visibleThreads(ws: WsData): MailThread[] {
  const s = state(ws);
  return [...s.threads.values()].filter((t) => seesThread(ws, t, db.meId)).map((t) => threadOut(ws, t));
}

function listThreads(ws: WsData, ctx: Ctx) {
  const s = state(ws);
  const me = db.meId;
  const box = ctx.query.get("box") || "inbox";
  const accountId = ctx.query.get("account_id");
  const folderId = ctx.query.get("folder_id");
  const q = (ctx.query.get("q") ?? "").trim().toLowerCase();
  const seeAcc = new Set(visibleAccounts(ws).map((a) => a.id));
  let rows = visibleThreads(ws);
  if (folderId) rows = rows.filter((t) => t.folder_ids.includes(folderId));
  else {
    if (accountId) rows = rows.filter((t) => t.account_id === accountId);
    // A person's conversations (with) include those shared with you on their own.
    else if (box !== "shared" && box !== "assigned" && !ctx.query.get("with")) rows = rows.filter((t) => seeAcc.has(t.account_id));
    rows = rows.filter((t) => {
      const junk = inRole(s, t, "trash") || inRole(s, t, "spam");
      switch (box) {
        case "inbox":
          return inRole(s, t, "inbox");
        case "unread":
          return t.unread && !junk;
        case "starred":
          return t.starred && !junk;
        case "sent":
        case "drafts":
        case "archive":
        case "trash":
        case "spam":
          return inRole(s, t, box);
        case "all":
          return !junk;
        case "shared":
          return t.shared || (s.accounts.find((a) => a.id === t.account_id)?.ownerId !== me && !junk && !!s.accounts.find((a) => a.id === t.account_id && a.visibility === "shared"));
        case "assigned":
          return t.assignees.some((x) => x.id === me);
        default:
          return false;
      }
    });
  }
  const to = (ctx.query.get("to") ?? "").trim().toLowerCase();
  if (to) rows = rows.filter((t) => msgsOf(s, t.id).some((m) => [m.from, ...m.to, ...m.cc].some((x) => x.email.toLowerCase() === to)));
  const withAddr = (ctx.query.get("with") ?? "").trim().toLowerCase();
  if (withAddr) rows = rows.filter((t) => hasAddress(s, t.id, withAddr));
  if (q)
    rows = rows.filter(
      (t) =>
        t.subject.toLowerCase().includes(q) ||
        t.snippet.toLowerCase().includes(q) ||
        t.participants.some((p) => p.email.toLowerCase().includes(q) || p.name.toLowerCase().includes(q)) ||
        msgsOf(s, t.id).some((m) => m.body_text.toLowerCase().includes(q)),
    );
  rows.sort((a, b) => b.last_message_at.localeCompare(a.last_message_at));
  const limit = Math.min(Math.max(Number(ctx.query.get("limit") ?? 50) || 50, 1), 100);
  const offset = Number(ctx.query.get("cursor") ?? 0) || 0;
  return { data: rows.slice(offset, offset + limit), next_cursor: offset + limit < rows.length ? String(offset + limit) : null };
}

const hasAddress = (s: State, threadId: string, email: string) =>
  msgsOf(s, threadId).some((m) => [m.from, ...m.to, ...m.cc, ...m.bcc].some((x) => x.email.toLowerCase() === email));

/** One address across the conversations you see (contracts/collab.md "People"). */
function personOut(ws: WsData, ctx: Ctx): MailPerson {
  const s = state(ws);
  const raw = (ctx.query.get("email") ?? "").trim();
  const email = (raw.match(/<([^>]+)>/)?.[1] ?? raw).toLowerCase();
  if (!/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(email)) throw invalid("Enter an email address.");
  const exclude = ctx.query.get("exclude");
  const limit = Math.min(Math.max(Number(ctx.query.get("limit") ?? 8) || 8, 1), 20);
  const rows = visibleThreads(ws)
    .filter((t) => !inRole(s, t, "trash") && !inRole(s, t, "spam") && hasAddress(s, t.id, email))
    .sort((a, b) => b.last_message_at.localeCompare(a.last_message_at));
  const msgs = rows.flatMap((t) => msgsOf(s, t.id)).filter((m) => !m.draft);
  const name = rows.flatMap((t) => t.participants).find((p) => p.email.toLowerCase() === email && p.name)?.name ?? "";
  const files = msgs
    .filter((m) => m.from.email.toLowerCase() === email)
    .sort((a, b) => b.sent_at.localeCompare(a.sent_at))
    .flatMap((m) => m.attachments.filter((x) => !x.inline).map((x) => ({ thread_id: m.thread_id, message_id: m.id, sent_at: m.sent_at, attachment: x })))
    .slice(0, 6);
  const times = msgs.map((m) => m.sent_at).sort();
  return {
    email,
    name,
    conversations: rows.length,
    others: rows.filter((t) => t.id !== exclude).length,
    first_at: times[0] ?? null,
    last_at: rows[0]?.last_message_at ?? null,
    threads: rows.filter((t) => t.id !== exclude).slice(0, limit),
    files,
  };
}

function counts(ws: WsData) {
  const s = state(ws);
  const me = db.meId;
  const seeAcc = new Set(visibleAccounts(ws).map((a) => a.id));
  const all = visibleThreads(ws);
  const mine = all.filter((t) => seeAcc.has(t.account_id));
  const inbox = mine.filter((t) => inRole(s, t, "inbox"));
  const accounts: Record<string, number> = {};
  for (const a of seeAcc) accounts[a] = inbox.filter((t) => t.account_id === a && t.unread).length;
  const folders: Record<string, number> = {};
  for (const f of s.folders) if (seeAcc.has(f.account_id)) folders[f.id] = mine.filter((t) => t.unread && t.folder_ids.includes(f.id)).length;
  return {
    boxes: {
      inbox: inbox.filter((t) => t.unread).length,
      unread: mine.filter((t) => t.unread && !inRole(s, t, "trash") && !inRole(s, t, "spam")).length,
      shared: all.filter((t) => t.shared && inRole(s, t, "inbox")).length,
      assigned: all.filter((t) => t.assignees.some((a) => a.id === me) && inRole(s, t, "inbox")).length,
      drafts: mine.filter((t) => inRole(s, t, "drafts")).length,
    },
    accounts,
    folders,
  };
}

/** Who sees an account or conversations: the event goes to them alone (contracts/collab.md "Events"). */
function audienceOf(ws: WsData, accountId: string | null, threadIds: string[]): string[] {
  const s = state(ws);
  const out = new Set<string>();
  for (const m of ws.members) {
    const a = accountId ? s.accounts.find((x) => x.id === accountId) : undefined;
    if (a && seesAccount(ws, a, m.user_id)) out.add(m.user_id);
    for (const id of threadIds) {
      const t = s.threads.get(id);
      if (t && seesThread(ws, t, m.user_id)) out.add(m.user_id);
    }
  }
  return [...out];
}

function emit(ws: WsData, type: "collab.changed" | "collab.accounts", data: Record<string, unknown>, users: string[]) {
  // Scoped to people: their user channels get it in full, the workspace channel the stand-in.
  publish(ws, type, data, { teamId: "__collab_private", topics: users.map((u) => `user:${u}`) });
}

function changed(ws: WsData, accountId: string | null, threadIds: string[], folders = true) {
  emit(ws, "collab.changed", { account_id: accountId, thread_ids: threadIds, folders }, audienceOf(ws, accountId, threadIds));
}

function threadOr404(ws: WsData, id: string): ThreadMeta {
  const t = state(ws).threads.get(id);
  if (!t || !seesThread(ws, t, db.meId)) throw notFound("Conversation");
  return t;
}

function accountOr404(ws: WsData, id: string): Account {
  const a = state(ws).accounts.find((x) => x.id === id);
  const orphan = !!a && !a.ownerId && a.visibility === "shared" && isAdmin(ws, db.meId);
  if (!a || (!seesAccount(ws, a, db.meId) && !orphan)) throw notFound("Account");
  return a;
}

function requireAct(ws: WsData) {
  if (!canActRole(ws, db.meId)) throw new HttpError(403, "forbidden", "Viewers can read but not act.");
}

function requireManage(ws: WsData, a: Account) {
  if (!((!!a.ownerId && a.ownerId === db.meId) || (a.visibility === "shared" && isAdmin(ws, db.meId)))) throw new HttpError(403, "forbidden", "Only whoever connected this account can change it.");
}

function audienceFrom(v: unknown): MailAudience {
  if (!isRecord(v)) return emptyAudience();
  return { everyone: v.everyone === true, team_ids: strArr(v.team_ids) ?? [], user_ids: strArr(v.user_ids) ?? [] };
}

function applyAction(ws: WsData, t: ThreadMeta, action: MailThreadAction, folderId?: string) {
  const s = state(ws);
  const a = s.accounts.find((x) => x.id === t.account_id)!;
  const ms = msgsOf(s, t.id);
  const rid = (role: MailFolderRole) => roleIdOf(s, a.id, role)!;
  const keep = (m: MailMessage) => m.folder_ids.filter((f) => f === rid("sent") || f === rid("drafts"));
  const isLabel = (id: string) => s.folders.find((f) => f.id === id)?.kind === "label" && !s.folders.find((f) => f.id === id)?.role;
  for (const m of ms) {
    switch (action) {
      case "read":
        m.unread = false;
        break;
      case "unread":
        m.unread = m === ms[ms.length - 1];
        break;
      case "star":
        m.starred = m === ms[ms.length - 1] || m.starred;
        break;
      case "unstar":
        m.starred = false;
        break;
      case "archive":
        m.folder_ids = [...new Set([...m.folder_ids.filter((f) => f !== rid("inbox")), rid("archive")])];
        break;
      case "inbox":
        m.folder_ids = [...new Set([...m.folder_ids.filter((f) => f !== rid("trash") && f !== rid("spam") && f !== rid("archive")), rid("inbox")])];
        break;
      case "trash":
        m.folder_ids = [...keep(m), rid("trash")];
        break;
      case "spam":
        m.folder_ids = [rid("spam")];
        break;
      case "not_spam":
        m.folder_ids = [rid("inbox")];
        break;
      case "move":
        if (!folderId) break;
        m.folder_ids = isLabel(folderId) ? [...new Set([...m.folder_ids.filter((f) => f !== rid("inbox")), folderId])] : [...keep(m), folderId];
        break;
      case "label":
        if (folderId && !m.folder_ids.includes(folderId)) m.folder_ids = [...m.folder_ids, folderId];
        break;
      case "unlabel":
        m.folder_ids = m.folder_ids.filter((f) => f !== folderId);
        break;
    }
  }
}

function notifyUser(ws: WsData, uid: string, type: "collab_assigned" | "collab_mention", data: Record<string, unknown>) {
  const n = { id: uuid(), type, ticket: null, data: { ...data, actor: userRef(db.meId) }, read_at: null, created_at: nowIso() };
  if (uid === db.meId) ws.notifications.unshift(n);
  publish(ws, "notification.created", { notification: n }, { topics: [`user:${uid}`] });
}

const addr = (email: string): MailAddress => {
  const known = PEOPLE.find((p) => p.email.toLowerCase() === email.toLowerCase());
  return known ?? { name: "", email };
};

export function registerCollab() {
  get("/v1/collab", (ctx) => status(ctx.ws));

  get("/v1/collab/accounts", (ctx) => {
    requireCollab(ctx.ws);
    const rows = visibleAccounts(ctx.ws);
    // manage=true: an admin also gets the shared accounts of people who left.
    if (ctx.query.get("manage") === "true" && isAdmin(ctx.ws, db.meId)) rows.push(...state(ctx.ws).accounts.filter((a) => a.visibility === "shared" && !a.ownerId && !rows.includes(a)));
    return rows.map((a) => accountOut(ctx.ws, a));
  });

  post("/v1/collab/accounts/oauth/:provider/start", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    if (ctx.params.provider !== "google" && ctx.params.provider !== "microsoft") throw new HttpError(422, "provider_not_configured", "That provider is not set up on this instance.");
    // The mock has no provider to visit: come straight back as if one was connected.
    const existing = visibleAccounts(ctx.ws).find((a) => a.provider === ctx.params.provider);
    const back = new URL("/settings/mail-accounts", location.origin);
    if (existing) back.searchParams.set("connected", existing.id);
    else back.searchParams.set("error", "provider_error");
    return { url: back.toString() };
  });

  post("/v1/collab/accounts/imap", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const b = ctx.body;
    const email = String(b.email ?? "").trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) throw invalid("email is not an address");
    if (String(b.password ?? "") === "wrong") throw new HttpError(422, "connection_failed", "Failed to connect", { detail: "IMAP imap.example.com:993: authentication failed (AUTHENTICATIONFAILED)" });
    if (String(b.imap_host ?? "").includes("unreachable")) throw new HttpError(422, "connection_failed", "IMAP: dial tcp: lookup unreachable: no such host");
    const s = state(ctx.ws);
    if (s.accounts.some((a) => a.email.toLowerCase() === email.toLowerCase())) throw new HttpError(409, "account_conflict", "That address is already connected in this workspace.");
    const me = db.meId;
    const a: Account = {
      id: uuid(),
      provider: "imap",
      email,
      name: String(b.name ?? "") || email,
      display_name: String(b.display_name ?? "") || (db.users.find((u) => u.id === me)?.name ?? ""),
      color: "indigo",
      owner: userRef(me),
      ownerId: me,
      visibility: b.visibility === "shared" ? "shared" : "private",
      shared_with: b.visibility === "shared" ? audienceFrom(b.shared_with) : emptyAudience(),
      signature_html: "",
      status: "syncing",
      reconnect: false,
      last_error: "",
      last_synced_at: null,
      can_manage: true,
      unread: 0,
      signature_sync: "none",
      signature_synced_at: null,
      display_name_sync: "none",
      out_of_office: null,
      out_of_office_sync: "none",
      aliases: [],
      defaultFrom: "",
      history: null,
      oldest_message_at: null,
      folders_left_out: 0,
      imap: {
        imap_host: String(b.imap_host),
        imap_port: Number(b.imap_port),
        imap_tls: b.imap_tls === "starttls" ? "starttls" : "tls",
        imap_username: String(b.imap_username ?? email),
        smtp_host: String(b.smtp_host),
        smtp_port: Number(b.smtp_port),
        smtp_tls: b.smtp_tls === "starttls" ? "starttls" : "tls",
        smtp_username: String(b.smtp_username ?? b.imap_username ?? email),
      },
      created_at: nowIso(),
    };
    s.accounts.push(a);
    for (const [n, role] of [["Inbox", "inbox"], ["Sent", "sent"], ["Drafts", "drafts"], ["Archive", "archive"], ["Spam", "spam"], ["Trash", "trash"]] as [string, MailFolderRole][]) s.folders.push(folder(a.id, n, role));
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    setTimeout(() => {
      a.status = "active";
      a.last_synced_at = nowIso();
      emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    }, 2500);
    return respond(201, accountOut(ctx.ws, a));
  });

  patch("/v1/collab/accounts/:id", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const b = ctx.body;
    // Two-way settings are made at Gmail first; a From name someone chose stops following Microsoft's.
    if (b.signature_html === "<p>gmail refuses</p>" && a.signature_sync === "two_way") throw new HttpError(502, "provider_error", "Gmail refused the request (400 INVALID_ARGUMENT).");
    if (typeof b.name === "string") a.name = b.name.trim() || a.email;
    checkSender(b.display_name, b.signature_html);
    // A value with variables is kept in opendesk: Gmail keeps its own (contracts/collab.md "Account settings").
    if (typeof b.display_name === "string" && b.display_name.trim() !== a.display_name) {
      a.display_name = b.display_name.trim().replace(/\s+/g, " ");
      if (a.provider === "microsoft") a.display_name_sync = "none";
      if (a.display_name_sync === "two_way" && !hasFromVars(a.display_name)) providerName.set(a.id, a.display_name);
    }
    if (typeof b.color === "string") a.color = b.color;
    if (typeof b.signature_html === "string" && b.signature_html !== a.signature_html) {
      a.signature_html = b.signature_html;
      if (a.signature_sync === "two_way" && !templated(b.signature_html)) {
        providerSignature.set(a.id, b.signature_html);
        a.signature_synced_at = nowIso();
      }
    }
    if (typeof b.default_from === "string") {
      if (!sendableAddress(a, b.default_from)) throw invalid("Choose one of the account's addresses that can send.");
      a.defaultFrom = b.default_from.trim().toLowerCase() === a.email.toLowerCase() ? "" : b.default_from.trim().toLowerCase();
    }
    if (b.visibility === "private" || b.visibility === "shared") a.visibility = b.visibility;
    if (b.shared_with !== undefined) a.shared_with = audienceFrom(b.shared_with);
    if (a.visibility === "private") a.shared_with = emptyAudience();
    if (isRecord(b.imap) && a.imap) {
      if (b.imap.password === "wrong") throw new HttpError(422, "connection_failed", "IMAP: authentication failed (AUTHENTICATIONFAILED)");
      a.imap = { ...a.imap, ...(b.imap as Partial<typeof a.imap>) };
      if (b.imap.password) {
        a.reconnect = false;
        a.status = "active";
        a.last_error = "";
      }
    }
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });

  post("/v1/collab/accounts/:id/signature/pull", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    if (a.provider !== "google") throw new HttpError(422, "signature_sync_unsupported", "Only Gmail accounts share their signature with opendesk.");
    a.signature_html = providerSignature.get(a.id) ?? a.signature_html;
    a.signature_synced_at = nowIso();
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });

  post("/v1/collab/accounts/:id/display-name/pull", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    if (a.provider !== "google") throw new HttpError(422, "display_name_sync_unsupported", "Only Gmail accounts share their From name with opendesk.");
    a.display_name = providerName.get(a.id) || meUser().name;
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });

  // Your From name and signature filled for you, as typed (nothing saved).
  post("/v1/collab/accounts/:id/identities/preview", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const b = ctx.body;
    const email = String(b.email ?? "").trim().toLowerCase() || a.email.toLowerCase();
    let format = String(b.display_name ?? "").trim() || a.display_name;
    const problem = /[{}<>]/.test(format) ? fromNameProblem(format, COLLAB_FROM_VARS) : null;
    const from_name_problem = problem ? "Fix the From name first." : "";
    if (problem) format = "";
    const from_name = filledName(ctx.ws, a, format);
    const sig = String(b.signature_html ?? "");
    return {
      from_name,
      from: from_name ? `${from_name} <${email}>` : email,
      signature_html: templated(sig) ? fillForSender(ctx.ws, sig, { name: from_name, email }) : sig,
      from_name_problem,
      problems: collabSignatureProblems(sig),
    };
  });

  put("/v1/collab/accounts/:id/out-of-office", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    if (a.out_of_office_sync === "none") throw new HttpError(422, "out_of_office_unsupported", "This account's provider does not share its automatic reply with opendesk.");
    if (a.out_of_office_sync === "read_only") throw new HttpError(422, "out_of_office_read_only", "Reconnect the account to change its automatic reply from opendesk.");
    const b = ctx.body;
    const at = (v: unknown) => (typeof v === "string" && !Number.isNaN(Date.parse(v)) ? new Date(v).toISOString() : null);
    const o = {
      enabled: b.enabled === true,
      start: at(b.start),
      end: at(b.end),
      subject: a.provider === "microsoft" ? "" : String(b.subject ?? "").slice(0, 200),
      message_html: String(b.message_html ?? ""),
      contacts_only: b.contacts_only === true,
      internal_only: b.internal_only === true,
    };
    if (o.start && o.end && Date.parse(o.end) <= Date.parse(o.start)) throw invalid("The automatic reply must end after it starts.");
    if (a.provider === "microsoft" && o.enabled && !!o.start !== !!o.end) throw invalid("Outlook needs both a start and an end to schedule an automatic reply.");
    if (a.provider === "microsoft" && o.internal_only) o.contacts_only = false;
    a.out_of_office = o;
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });

  // Addresses (contracts/collab.md "Addresses"): Gmail's come from Gmail; Microsoft's and IMAP's are added by hand.
  const identityOr404 = (a: Account, id: string) => {
    const i = a.aliases.find((x) => x.id === id);
    if (!i) throw notFound("Address");
    return i;
  };
  post("/v1/collab/accounts/:id/identities", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    if (a.provider === "google") throw new HttpError(422, "identities_from_provider", "Add and remove send-as addresses in Gmail's settings (Accounts, Send mail as). They show here within 15 minutes.");
    const b = ctx.body;
    const email = String(b.email ?? "").trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) throw invalid("Enter the address to send from.");
    if (email === a.email.toLowerCase() || a.aliases.some((x) => x.email === email)) throw new HttpError(409, "identity_conflict", "The account already has this address.");
    checkSender(b.display_name, b.signature_html);
    a.aliases.push({
      ...alias(email, String(b.display_name ?? "").trim(), String(b.signature_html ?? "")),
      source: "manual",
      reply_to: String(b.reply_to ?? "").trim().toLowerCase(),
      signature_sync: "none",
      display_name_sync: "none",
      signature_synced_at: null,
    });
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });
  patch("/v1/collab/accounts/:id/identities/:identityId", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const i = identityOr404(a, ctx.params.identityId);
    const b = ctx.body;
    if (typeof b.reply_to === "string" && i.source === "provider" && b.reply_to.trim().toLowerCase() !== i.reply_to) throw invalid("Gmail keeps this address's reply-to. Change it in Gmail's settings.");
    checkSender(b.display_name, b.signature_html);
    if (typeof b.display_name === "string") i.display_name = b.display_name.trim().replace(/\s+/g, " ");
    // Kept here: Gmail lets apps change its main address alone (a later change in Gmail replaces it).
    if (typeof b.signature_html === "string") i.signature_html = b.signature_html;
    if (typeof b.reply_to === "string" && i.source === "manual") i.reply_to = b.reply_to.trim().toLowerCase();
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });
  post("/v1/collab/accounts/:id/identities/:identityId/signature/pull", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const i = identityOr404(a, ctx.params.identityId);
    if (i.source !== "provider") throw new HttpError(422, "signature_sync_unsupported", "Only Gmail's send-as addresses share their signature with opendesk.");
    i.signature_html = providerSignature.get(i.id!) ?? i.signature_html;
    i.signature_synced_at = nowIso();
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });
  post("/v1/collab/accounts/:id/identities/:identityId/display-name/pull", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const i = identityOr404(a, ctx.params.identityId);
    if (i.source !== "provider") throw new HttpError(422, "display_name_sync_unsupported", "Only Gmail accounts share their From name with opendesk.");
    i.display_name = providerName.get(i.id!) ?? i.display_name;
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });
  del("/v1/collab/accounts/:id/identities/:identityId", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const i = identityOr404(a, ctx.params.identityId);
    if (i.source === "provider") throw new HttpError(422, "identities_from_provider", "Add and remove send-as addresses in Gmail's settings (Accounts, Send mail as).");
    a.aliases = a.aliases.filter((x) => x.id !== i.id);
    if (a.defaultFrom === i.email) a.defaultFrom = "";
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });
  get("/v1/collab/accounts/:id/identities/suggestions", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    if (a.provider !== "microsoft") return { addresses: [] };
    return { addresses: ["deals@margin.app", "partners@margin.app"].filter((e) => !a.aliases.some((x) => x.email === e)) };
  });

  del("/v1/collab/accounts/:id", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const users = audienceOf(ctx.ws, a.id, []);
    const s = state(ctx.ws);
    s.accounts = s.accounts.filter((x) => x.id !== a.id);
    s.folders = s.folders.filter((f) => f.account_id !== a.id);
    s.messages = s.messages.filter((m) => m.account_id !== a.id);
    for (const [id, t] of s.threads) if (t.account_id === a.id) s.threads.delete(id);
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, users);
    return respond(204);
  });

  post("/v1/collab/accounts/:id/sync", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    a.status = a.reconnect ? "error" : "syncing";
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    setTimeout(() => {
      if (!a.reconnect) a.status = "active";
      a.last_synced_at = a.reconnect ? a.last_synced_at : nowIso();
      emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    }, 1500);
    return respond(202);
  });

  // History imports (contracts/collab.md "History").
  put("/v1/collab/accounts/:id/history", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const b = ctx.body;
    let since: string | null = null;
    if (b.since !== null && b.since !== undefined) {
      const d = new Date(String(b.since));
      if (Number.isNaN(d.getTime()) || d.getTime() > Date.now()) throw invalid("since must be a date in the past");
      since = d.toISOString();
    }
    const history: MailHistory = {
      status: "running",
      since,
      include_spam_trash: b.include_spam_trash === true,
      requested_by: userRef(db.meId),
      started_at: nowIso(),
      finished_at: null,
      reached: null,
      imported: 0,
      present: 0,
      too_large: 0,
      unreadable: 0,
      total: a.provider === "microsoft" && since ? null : since ? 2_400 : 9_800,
      last_error: "",
    };
    a.history = history;
    runHistory(ctx.ws, a);
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });

  patch("/v1/collab/accounts/:id/history", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const h = a.history;
    if (!h) throw notFound("History import");
    if (h.status === "done" || h.status === "canceled" || h.status === "error") throw invalid("This import has ended. Start a new one.");
    h.status = ctx.body.paused === true ? "paused" : "running";
    runHistory(ctx.ws, a);
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });

  del("/v1/collab/accounts/:id/history", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    requireManage(ctx.ws, a);
    const h = a.history;
    if (!h) throw notFound("History import");
    if (h.status !== "done" && h.status !== "canceled" && h.status !== "error") {
      h.status = "canceled";
      h.finished_at = nowIso();
    }
    runHistory(ctx.ws, a);
    emit(ctx.ws, "collab.accounts", { account_id: a.id }, audienceOf(ctx.ws, a.id, []));
    return accountOut(ctx.ws, a);
  });

  get("/v1/collab/accounts/:id/folders", (ctx) => {
    requireCollab(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    const s = state(ctx.ws);
    const all = [...s.threads.values()].filter((t) => t.account_id === a.id).map((t) => threadOut(ctx.ws, t));
    const order: (MailFolderRole | null)[] = ["inbox", "starred", "important", "sent", "drafts", "archive", "all", "spam", "trash"];
    return s.folders
      .filter((f) => f.account_id === a.id)
      .map((f) => ({ ...f, unread: all.filter((t) => t.unread && t.folder_ids.includes(f.id)).length, total: all.filter((t) => t.folder_ids.includes(f.id)).length }))
      .sort((x, y) => (x.role ? order.indexOf(x.role) : 99) - (y.role ? order.indexOf(y.role) : 99) || x.path.localeCompare(y.path));
  });

  post("/v1/collab/accounts/:id/folders", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const a = accountOr404(ctx.ws, ctx.params.id);
    const name = String(ctx.body.name ?? "").trim();
    if (!name || name.length > 100) throw invalid("name must be 1 to 100 characters");
    const s = state(ctx.ws);
    const parent = typeof ctx.body.parent_id === "string" ? s.folders.find((f) => f.id === ctx.body.parent_id) : undefined;
    // Gmail makes labels; Microsoft a folder, or with kind label an Outlook category; IMAP folders only.
    const label = a.provider === "google" || ctx.body.kind === "label";
    if (label && a.provider === "imap") throw new HttpError(422, "labels_unsupported", "IMAP accounts have no labels. Move the conversation to a folder instead.");
    const tone = LABEL_TONES.find((x) => x === ctx.body.color);
    const color = label ? (tone ? toneHex(a.provider, tone) : a.provider === "google" ? "#a479e2" : null) : null;
    const f = folder(a.id, name, null, label ? "label" : "folder", parent && !(label && a.provider === "microsoft") ? `${parent.path}/${name}` : name, color);
    s.folders.push(f);
    changed(ctx.ws, a.id, [], true);
    return respond(201, f);
  });

  patch("/v1/collab/folders/:id", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const s = state(ctx.ws);
    const f = s.folders.find((x) => x.id === ctx.params.id);
    if (!f) throw notFound("Folder");
    accountOr404(ctx.ws, f.account_id);
    if (f.role) throw invalid("A special folder cannot be renamed");
    const a = state(ctx.ws).accounts.find((x) => x.id === f.account_id)!;
    const name = String(ctx.body.name ?? "").trim();
    const color = ctx.body.color;
    if (!name && color === undefined) throw invalid("Give a new name or a colour.");
    if (color !== undefined) {
      const tone = LABEL_TONES.find((x) => x === color) as LabelTone | undefined;
      if (!tone) throw invalid("color must be one of the label colours");
      if (f.kind !== "label" || a.provider === "imap") throw invalid("Only Gmail labels and Outlook categories have colours.");
      f.color = toneHex(a.provider, tone);
    }
    if (name && name !== f.name) {
      if (f.kind === "label" && a.provider === "microsoft") throw new HttpError(422, "rename_unsupported", "Outlook categories cannot be renamed. Create a new one instead.");
      f.path = f.path.includes("/") ? `${f.path.slice(0, f.path.lastIndexOf("/"))}/${name}` : name;
      f.name = name;
    }
    changed(ctx.ws, f.account_id, [], true);
    return f;
  });

  del("/v1/collab/folders/:id", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const s = state(ctx.ws);
    const f = s.folders.find((x) => x.id === ctx.params.id);
    if (!f) throw notFound("Folder");
    accountOr404(ctx.ws, f.account_id);
    if (f.role) throw invalid("A special folder cannot be deleted");
    s.folders = s.folders.filter((x) => x.id !== f.id);
    for (const m of s.messages) m.folder_ids = m.folder_ids.filter((x) => x !== f.id);
    changed(ctx.ws, f.account_id, [], true);
    return respond(204);
  });

  get("/v1/collab/threads", (ctx) => {
    requireCollab(ctx.ws);
    return listThreads(ctx.ws, ctx);
  });

  get("/v1/collab/person", (ctx) => {
    requireCollab(ctx.ws);
    return personOut(ctx.ws, ctx);
  });

  get("/v1/collab/counts", (ctx) => {
    requireCollab(ctx.ws);
    return counts(ctx.ws);
  });

  get("/v1/collab/threads/:id", (ctx) => {
    requireCollab(ctx.ws);
    return detailOut(ctx.ws, threadOr404(ctx.ws, ctx.params.id));
  });

  get("/v1/collab/messages/:id/details", (ctx) => {
    requireCollab(ctx.ws);
    const s = state(ctx.ws);
    const m = s.messages.find((x) => x.id === ctx.params.id);
    if (!m) throw notFound("Message");
    const t = threadOr404(ctx.ws, m.thread_id);
    const a = s.accounts.find((x) => x.id === t.account_id)!;
    return messageDetails(m, a.provider, m.sent_by !== null || m.from.email === a.email);
  });

  post("/v1/collab/threads/actions", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const ids = strArr(ctx.body.thread_ids) ?? [];
    if (!ids.length || ids.length > 100) throw invalid("thread_ids must hold 1 to 100 ids");
    const action = String(ctx.body.action) as MailThreadAction;
    const folderId = typeof ctx.body.folder_id === "string" ? ctx.body.folder_id : undefined;
    const ts = ids.map((id) => threadOr404(ctx.ws, id));
    const s = state(ctx.ws);
    if (action === "label" || action === "unlabel") {
      for (const t of ts) if (s.accounts.find((a) => a.id === t.account_id)?.provider === "imap") throw new HttpError(422, "labels_unsupported", "IMAP accounts have no labels. Move the conversation to a folder instead.");
    }
    if ((action === "move" || action === "label" || action === "unlabel") && (!folderId || !s.folders.some((f) => f.id === folderId))) throw invalid("folder_id is required");
    for (const t of ts) applyAction(ctx.ws, t, action, folderId);
    const accountIds = [...new Set(ts.map((t) => t.account_id))];
    for (const acc of accountIds) changed(ctx.ws, acc, ts.filter((t) => t.account_id === acc).map((t) => t.id), true);
    return { threads: ts.map((t) => threadOut(ctx.ws, t)) };
  });

  put("/v1/collab/threads/:id/shares", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const t = threadOr404(ctx.ws, ctx.params.id);
    const before = audienceOf(ctx.ws, null, [t.id]);
    t.shares = audienceFrom(ctx.body.shares);
    const after = audienceOf(ctx.ws, null, [t.id]);
    emit(ctx.ws, "collab.changed", { account_id: null, thread_ids: [t.id], folders: true }, [...new Set([...before, ...after])]);
    return detailOut(ctx.ws, t);
  });

  put("/v1/collab/threads/:id/assignees", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const t = threadOr404(ctx.ws, ctx.params.id);
    const ids = strArr(ctx.body.user_ids) ?? [];
    if (ids.length > 20) throw invalid("At most 20 assignees");
    for (const id of ids) if (!ctx.ws.members.some((m) => m.user_id === id && m.role !== "viewer")) throw invalid("Each assignee must be an agent or above");
    const added = ids.filter((id) => !t.assignees.includes(id));
    t.assignees = ids;
    for (const uid of added) notifyUser(ctx.ws, uid, "collab_assigned", { thread_id: t.id, account_id: t.account_id, subject: t.subject });
    changed(ctx.ws, t.account_id, [t.id], true);
    return detailOut(ctx.ws, t);
  });

  post("/v1/collab/threads/:id/comments", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const t = threadOr404(ctx.ws, ctx.params.id);
    const html = String(ctx.body.body_html ?? "");
    if (!html.trim() || html.length > 20000) throw invalid("body_html must be 1 to 20000 characters");
    const mentions = (strArr(ctx.body.mentions) ?? []).filter((id) => ctx.ws.members.some((m) => m.user_id === id));
    const at = nowIso();
    const c: MailComment = { id: uuid(), thread_id: t.id, author: userRef(db.meId), body_html: html, mentions, created_at: at, updated_at: at };
    t.comments.push(c);
    for (const uid of mentions) {
      if (!seesThread(ctx.ws, t, uid)) t.shares = { ...t.shares, user_ids: [...new Set([...t.shares.user_ids, uid])] };
      if (uid !== db.meId) notifyUser(ctx.ws, uid, "collab_mention", { thread_id: t.id, comment_id: c.id, subject: t.subject });
    }
    changed(ctx.ws, t.account_id, [t.id], false);
    return respond(201, c);
  });

  patch("/v1/collab/comments/:id", (ctx) => {
    requireCollab(ctx.ws);
    for (const t of state(ctx.ws).threads.values()) {
      const c = t.comments.find((x) => x.id === ctx.params.id);
      if (!c) continue;
      threadOr404(ctx.ws, t.id);
      if (c.author?.id !== db.meId) throw new HttpError(403, "forbidden", "Only its author can edit a comment.");
      c.body_html = String(ctx.body.body_html ?? c.body_html);
      c.updated_at = nowIso();
      changed(ctx.ws, t.account_id, [t.id], false);
      return c;
    }
    throw notFound("Comment");
  });

  del("/v1/collab/comments/:id", (ctx) => {
    requireCollab(ctx.ws);
    for (const t of state(ctx.ws).threads.values()) {
      const c = t.comments.find((x) => x.id === ctx.params.id);
      if (!c) continue;
      threadOr404(ctx.ws, t.id);
      if (c.author?.id !== db.meId && !isAdmin(ctx.ws, db.meId)) throw new HttpError(403, "forbidden", "Only its author or an admin can delete a comment.");
      t.comments = t.comments.filter((x) => x.id !== c.id);
      changed(ctx.ws, t.account_id, [t.id], false);
      return respond(204);
    }
    throw notFound("Comment");
  });

  post("/v1/collab/messages", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const s = state(ctx.ws);
    const b = ctx.body;
    const a = accountOr404(ctx.ws, String(b.account_id ?? ""));
    const mode = String(b.mode ?? "new");
    const to = strArr(b.to) ?? [];
    const cc = strArr(b.cc) ?? [];
    const bcc = strArr(b.bcc) ?? [];
    if (!to.length && !cc.length && !bcc.length) throw invalid("Add at least one recipient");
    if (to.length + cc.length + bcc.length > 100) throw invalid("At most 100 recipients");
    if (to.some((e) => e.endsWith("@bounce.test"))) throw new HttpError(502, "send_failed", "The provider refused the message: 550 5.1.1 mailbox unavailable");
    const replied = mode !== "new" ? s.messages.find((m) => m.id === b.message_id) : undefined;
    if (mode !== "new" && !replied) throw invalid("message_id is required unless new");
    const from = senderFor(ctx.ws, a, b.from_email, replied);
    let thread: ThreadMeta;
    if (replied && mode !== "forward") thread = s.threads.get(replied.thread_id)!;
    else {
      thread = { id: uuid(), account_id: a.id, subject: String(b.subject ?? ""), shares: emptyAudience(), assignees: [], comments: [] };
      s.threads.set(thread.id, thread);
    }
    const ids = strArr(b.attachment_ids) ?? [];
    const uploads: Attachment[] = db.attachments.filter((x) => ids.includes(x.id));
    const html = String(b.body_html ?? "");
    const text = html.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
    const sentId = roleIdOf(s, a.id, "sent");
    const m: MailMessage = {
      id: uuid(),
      thread_id: thread.id,
      account_id: a.id,
      message_id: `${uuid()}@margin.app`,
      from,
      to: to.map(addr),
      cc: cc.map(addr),
      bcc: bcc.map(addr),
      reply_to: [],
      subject: String(b.subject ?? ""),
      snippet: text.slice(0, 140),
      body_html: html,
      body_text: text,
      sent_at: nowIso(),
      unread: false,
      starred: false,
      draft: false,
      folder_ids: sentId ? [sentId] : [],
      attachments: [
        ...uploads.map((u, i) => ({ id: String(i), filename: u.filename, content_type: u.content_type, size: u.size_bytes, inline: false, content_id: "", url: u.url })),
        ...(mode === "forward" && b.forward_attachments && replied ? replied.attachments.map((x, i) => ({ ...x, id: String(uploads.length + i) })) : []),
      ],
      sent_by: a.visibility === "shared" ? userRef(db.meId) : null,
      outgoing: true,
    };
    s.messages.push(m);
    changed(ctx.ws, a.id, [thread.id], true);
    return respond(201, m);
  });

  // Drafts (contracts/collab.md "Drafts"): kept "at the provider" in Drafts; sending moves them to Sent.
  const draftOr404 = (ws: WsData, id: string) => {
    const s = state(ws);
    const m = s.messages.find((x) => x.id === id && x.draft);
    if (!m) throw notFound("Draft");
    threadOr404(ws, m.thread_id);
    return m;
  };
  const writeDraft = (ws: WsData, m: MailMessage, b: Record<string, unknown>, a: Account) => {
    const to = strArr(b.to) ?? [];
    const cc = strArr(b.cc) ?? [];
    const bcc = strArr(b.bcc) ?? [];
    if (to.length + cc.length + bcc.length > 100) throw invalid("At most 100 recipients");
    const ids = strArr(b.attachment_ids) ?? [];
    const uploads: Attachment[] = db.attachments.filter((x) => ids.includes(x.id));
    const keep = strArr(b.keep_attachments);
    const kept = m.attachments.filter((x) => !x.inline && (keep ? keep.includes(x.id) : true));
    const html = String(b.body_html ?? "");
    const text = html.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
    // A draft keeps its From unless another of the account's addresses is chosen.
    const from = typeof b.from_email === "string" && b.from_email.trim() ? senderFor(ws, a, b.from_email) : sendableAddress(a, m.from.email) ? senderFor(ws, a, m.from.email) : senderFor(ws, a, undefined);
    Object.assign(m, {
      from,
      to: to.map(addr),
      cc: cc.map(addr),
      bcc: bcc.map(addr),
      subject: String(b.subject ?? ""),
      snippet: text.slice(0, 140),
      body_html: html,
      body_text: text,
      sent_at: nowIso(),
      attachments: [...kept, ...uploads.map((u) => ({ id: "", filename: u.filename, content_type: u.content_type, size: u.size_bytes, inline: false, content_id: "", url: u.url }))].map((x, i) => ({ ...x, id: String(i) })),
      sent_by: userRef(db.meId),
      outgoing: true,
    });
    // A conversation of drafts alone is named by them.
    const t = state(ws).threads.get(m.thread_id);
    if (t && !state(ws).messages.some((x) => x.thread_id === t.id && !x.draft)) t.subject = m.subject;
  };

  post("/v1/collab/drafts", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const s = state(ctx.ws);
    const b = ctx.body;
    const a = accountOr404(ctx.ws, String(b.account_id ?? ""));
    const mode = String(b.mode ?? "new");
    const replied = mode !== "new" ? s.messages.find((m) => m.id === b.message_id) : undefined;
    if (mode !== "new" && !replied) throw invalid("message_id is required unless new");
    let thread: ThreadMeta;
    if (replied && mode !== "forward") thread = threadOr404(ctx.ws, replied.thread_id);
    else {
      thread = { id: uuid(), account_id: a.id, subject: String(b.subject ?? ""), shares: emptyAudience(), assignees: [], comments: [] };
      s.threads.set(thread.id, thread);
    }
    const id = uuid();
    const m: MailMessage = {
      id,
      thread_id: thread.id,
      account_id: a.id,
      message_id: `${uuid()}@margin.app`,
      from: senderFor(ctx.ws, a, b.from_email, replied),
      to: [],
      cc: [],
      bcc: [],
      reply_to: [],
      subject: "",
      snippet: "",
      body_html: "",
      body_text: "",
      sent_at: nowIso(),
      unread: false,
      starred: false,
      draft: true,
      draft_id: id,
      in_reply_to_id: replied && mode !== "forward" ? replied.id : null,
      folder_ids: [roleIdOf(s, a.id, "drafts")].filter((x): x is string => !!x),
      attachments: mode === "forward" && b.forward_attachments && replied ? replied.attachments.filter((x) => !x.inline) : [],
      sent_by: userRef(db.meId),
      outgoing: true,
    };
    s.messages.push(m);
    writeDraft(ctx.ws, m, b, a);
    changed(ctx.ws, a.id, [thread.id], true);
    return respond(201, m);
  });

  put("/v1/collab/drafts/:id", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const m = draftOr404(ctx.ws, ctx.params.id);
    const a = state(ctx.ws).accounts.find((x) => x.id === m.account_id)!;
    writeDraft(ctx.ws, m, ctx.body, a);
    changed(ctx.ws, a.id, [m.thread_id], true);
    return m;
  });

  del("/v1/collab/drafts/:id", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const s = state(ctx.ws);
    const m = draftOr404(ctx.ws, ctx.params.id);
    s.messages = s.messages.filter((x) => x.id !== m.id);
    if (!s.messages.some((x) => x.thread_id === m.thread_id)) s.threads.delete(m.thread_id);
    changed(ctx.ws, m.account_id, [m.thread_id], true);
    return respond(204);
  });

  post("/v1/collab/drafts/:id/send", (ctx) => {
    requireCollab(ctx.ws);
    requireAct(ctx.ws);
    const s = state(ctx.ws);
    const m = draftOr404(ctx.ws, ctx.params.id);
    const a = s.accounts.find((x) => x.id === m.account_id)!;
    if (isRecord(ctx.body) && Object.keys(ctx.body).length) writeDraft(ctx.ws, m, ctx.body, a);
    const all = [...m.to, ...m.cc, ...m.bcc];
    if (!all.length) throw invalid("Add a recipient.");
    if (all.some((x) => x.email.endsWith("@bounce.test"))) throw new HttpError(502, "send_failed", "The provider refused the message: 550 5.1.1 mailbox unavailable");
    const sentId = roleIdOf(s, a.id, "sent");
    Object.assign(m, { draft: false, draft_id: null, folder_ids: sentId ? [sentId] : [], sent_at: nowIso(), outgoing: true, sent_by: a.visibility === "shared" ? userRef(db.meId) : null });
    changed(ctx.ws, a.id, [m.thread_id], true);
    return m;
  });

  get("/v1/collab/sidebar", (ctx) => {
    requireCollab(ctx.ws);
    return state(ctx.ws).prefs[db.meId] ?? defaultSidebarPrefs();
  });

  put("/v1/collab/sidebar", (ctx) => {
    requireCollab(ctx.ws);
    if (JSON.stringify(ctx.body).length > 16 * 1024) throw invalid("Sidebar preferences are limited to 16 KB");
    const body = ctx.body as Partial<SidebarPrefs>;
    const saved = state(ctx.ws).prefs[db.meId] ?? defaultSidebarPrefs();
    // Like the API: a client that leaves out the numbers or the tabs keeps them as saved.
    const p = normalizePrefs({ ...body, badges: body.badges ?? saved.badges, mail_tabs: body.mail_tabs ?? saved.mail_tabs });
    if (p.pinned.length > 30) throw invalid("At most 30 favorites");
    if (Object.keys(p.badges).length > 300) throw invalid("Numbers can be chosen for at most 300 rows.");
    if (p.mail_tabs.length > 30) throw invalid("The folder bar holds at most 30 tabs.");
    state(ctx.ws).prefs[db.meId] = p;
    return p;
  });
}

// How the mock's mail came: senders on their own domain pass every check; a few come
// through a mailing service ("via"), and the PayPal look-alike fails DMARC.
const MOCK_SOURCES: Record<string, Partial<MailMessageDetails>> = {
  "service@paypal.com": { mailed_by: "mail-relay.top", signed_by: [], via: "mail-relay.top", spf: "softfail", dkim: "none", dmarc: "fail", encryption: "none", warning: "spoofed" },
  "newsletter@webdrop.dev": { mailed_by: "mg.mailgun.org", signed_by: ["mailgun.org"], via: "mailgun.org" },
};

function messageDetails(m: MailMessage, provider: string, fromAccount: boolean): MailMessageDetails {
  const domain = m.from.email.split("@")[1] ?? "";
  const date = new Date(m.sent_at).toUTCString().replace("GMT", "+0000");
  const addr = (xs: MailAddress[]) => xs.map((x) => (x.name ? `${x.name} <${x.email}>` : x.email)).join(", ");
  const base: [string, string][] = [
    ["From", addr([m.from])],
    ["To", addr(m.to)],
    ...(m.cc.length ? [["Cc", addr(m.cc)] as [string, string]] : []),
    ...(m.reply_to.length ? [["Reply-To", addr(m.reply_to)] as [string, string]] : []),
    ["Subject", m.subject],
    ["Date", date],
    ["Message-ID", `<${m.message_id}>`],
    ["MIME-Version", "1.0"],
    ["Content-Type", 'multipart/alternative; boundary="000000000000a1b2c3"'],
  ];
  if (fromAccount) {
    const headers = [...base, ...(m.bcc.length ? [["Bcc", addr(m.bcc)] as [string, string]] : [])];
    return { mailed_by: "", signed_by: [], via: "", spf: "", dkim: "", dmarc: "", encryption: "", warning: "", headers: headers.map(([name, value]) => ({ name, value })) };
  }
  const d: MailMessageDetails = { mailed_by: domain, signed_by: [domain], via: "", spf: "pass", dkim: "pass", dmarc: "pass", encryption: "tls", warning: "", headers: [], ...MOCK_SOURCES[m.from.email] };
  const mx = provider === "google" ? "mx.google.com" : provider === "microsoft" ? "outlook.office365.com" : "mx.margin.app";
  const signer = d.signed_by[0];
  const results = [
    signer ? `dkim=pass header.i=@${signer} header.s=s1 header.b=Xk3pQa9z` : "dkim=none (no signature)",
    `spf=${d.spf} (${mx}: domain of bounce@${d.mailed_by} ${d.spf === "pass" ? "designates 203.0.113.24 as permitted sender" : "does not designate 198.51.100.7 as permitted sender"}) smtp.mailfrom=bounce@${d.mailed_by}`,
    `dmarc=${d.dmarc} (p=${d.dmarc === "fail" ? "REJECT" : "NONE"} sp=NONE dis=${d.dmarc === "fail" ? "QUARANTINE" : "NONE"}) header.from=${domain}`,
  ];
  const sending = d.encryption === "tls" ? "with ESMTPS id 5b1f17b1804b1-43a8f" : "with ESMTP id 9c2a64e1f0a3d-12be4";
  d.headers = [
    { name: "Delivered-To", value: m.to[0]?.email ?? "" },
    { name: "Received", value: `by 2002:a05:7300:5b8e:b0:1a4 with SMTP id k14csp; ${date}` },
    { name: "Authentication-Results", value: `${mx}; ${results.join("; ")}` },
    { name: "Return-Path", value: `<bounce@${d.mailed_by}>` },
    { name: "Received", value: `from mta-24.${d.mailed_by} (mta-24.${d.mailed_by}. [203.0.113.24]) by ${mx} ${sending} for <${m.to[0]?.email ?? ""}>; ${date}` },
    ...(signer ? [{ name: "DKIM-Signature", value: `v=1; a=rsa-sha256; c=relaxed/relaxed; d=${signer}; s=s1; h=from:to:subject:date:message-id; bh=47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=; b=Xk3pQa9z` }] : []),
    ...base.map(([name, value]) => ({ name, value })),
  ];
  return d;
}
