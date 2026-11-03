// Test workspaces (contracts/admin.md "Test workspaces"): the sample data sizes
// and kinds the panel offers, what each adds, how expiry and sample status
// read, and the owner address a new test account is offered. Labels are keys of
// the testWorkspaces catalog: `size.<size>`, `kind.<kind>`, `planHint.<plan>`, `delete.<choice>`.

import { i18n, inSentence, listOf } from "@/i18n";
import type { AdminSampleCounts, AdminSampleData, SampleKind, SampleSize, TestPlan } from "./api/types";
import { number, relative, shortDate } from "./format";
import type { Tone } from "./tones";

export const SIZES: SampleSize[] = ["small", "realistic", "large"];

/** What one batch of each size adds: internal/sample's volumes. */
export const VOLUMES: Record<SampleSize, { members: number; teams: number; contacts: number; companies: number; tickets: number; chats: number; articles: number; collections: number; mailAccounts: number; mailThreads: number; days: number; extras: boolean }> = {
  small: { members: 3, teams: 2, contacts: 30, companies: 6, tickets: 40, chats: 5, articles: 6, collections: 2, mailAccounts: 1, mailThreads: 20, days: 30, extras: false },
  realistic: { members: 8, teams: 4, contacts: 300, companies: 30, tickets: 400, chats: 40, articles: 25, collections: 5, mailAccounts: 2, mailThreads: 120, days: 90, extras: true },
  large: { members: 25, teams: 8, contacts: 3000, companies: 120, tickets: 5000, chats: 300, articles: 80, collections: 8, mailAccounts: 4, mailThreads: 600, days: 365, extras: true },
};

export const KINDS: SampleKind[] = ["tickets", "chat", "kb", "team", "collab", "history"];

const n = (v: number) => number(v);
const t = (key: string, opts?: Record<string, unknown>) => String(i18n.t(`testWorkspaces:${key}` as never, opts as never));

/** What a kind adds at a size, in a sentence. */
export function kindAdds(kind: SampleKind, size: SampleSize): string {
  const v = VOLUMES[size];
  switch (kind) {
    case "tickets":
      return t(v.extras ? "adds.ticketsExtras" : "adds.tickets", { tickets: n(v.tickets), contacts: n(v.contacts), companies: n(v.companies) });
    case "chat":
      return t("adds.chat", { chats: n(v.chats) });
    case "kb":
      return t("adds.kb", { articles: n(v.articles), collections: n(v.collections) });
    case "team":
      return t("adds.team", { members: n(v.members), teams: n(v.teams) });
    case "collab":
      return t("adds.collab", { count: v.mailAccounts, accounts: n(v.mailAccounts), threads: n(v.mailThreads) });
    case "history":
      return t("adds.history", { days: n(v.days) });
  }
}

export const ALL_KINDS: SampleKind[] = KINDS;

/** The kinds in the generator's order, without repeats. */
export function orderKinds(kinds: SampleKind[]): SampleKind[] {
  return ALL_KINDS.filter((k) => kinds.includes(k));
}

export const PLANS: TestPlan[] = ["free", "pro", "collab"];

export type DeleteChoice = "1" | "7" | "14" | "30" | "90" | "365" | "keep";
export const DELETE_CHOICES: DeleteChoice[] = ["1", "7", "14", "30", "90", "365", "keep"];

/** delete_after_days for a choice: null keeps the workspace. */
export function deleteAfterDays(choice: DeleteChoice): number | null {
  return choice === "keep" ? null : Number(choice);
}

const DAY = 86_400_000;

/** How a workspace's expiry reads: kept, overdue (the hourly cleanup takes it next), soon, or a date. */
export function expiryLabel(iso: string | null, now = Date.now()): { label: string; tone: Tone } {
  if (!iso) return { label: t("expiry.kept"), tone: "gray" };
  const left = Date.parse(iso) - now;
  if (left <= 0) return { label: t("expiry.now"), tone: "red" };
  if (left < 3 * DAY) return { label: t("expiry.soon", { when: relative(iso, now) }), tone: "amber" };
  return { label: t("expiry.on", { date: shortDate(iso, now) }), tone: "gray" };
}

/** How a test account's password expiry reads. */
export function passwordLabel(iso: string | null, now = Date.now()): { label: string; tone: Tone } | null {
  if (!iso) return null;
  const left = Date.parse(iso) - now;
  if (left <= 0) return { label: t("password.expired"), tone: "red" };
  if (left < 3 * DAY) return { label: t("password.soon", { when: relative(iso, now) }), tone: "amber" };
  return { label: t("password.until", { date: shortDate(iso, now) }), tone: "gray" };
}

/** The counts a line names, in order; each reads `counts.<key>` with its count. */
const COUNT_PARTS: (keyof AdminSampleCounts)[] = ["tickets", "messages", "chats", "contacts", "companies", "articles", "members", "teams", "mail_threads"];

/** "120 tickets, 640 messages, 14 articles": what sample data added, the zeros left out. */
export function countsLine(c: AdminSampleCounts | null, max = COUNT_PARTS.length): string {
  if (!c) return "";
  const parts = COUNT_PARTS.filter((k) => c[k] > 0).map((k) => t(`counts.${k}`, { count: c[k], n: n(c[k]) }));
  return parts.slice(0, max).join(", ");
}

export function isRunning(s: AdminSampleData) {
  return s.status === "running";
}

/** A short line for the status: "Adding realistic data", "3 batches", the error. */
export function sampleLine(s: AdminSampleData): string {
  if (s.status === "running") return t(`sample.adding.${s.size || "sample"}`);
  if (s.status === "failed") return s.error || t("sample.lastFailed");
  if (s.status === "done") return countsLine(s.counts, 4) || t("sample.added");
  return "";
}

/** The suggested address of a new test account: the reserved .invalid domain receives nothing. */
export function suggestEmail(workspaceName: string, rand = Math.random): string {
  const slug =
    workspaceName
      .normalize("NFKD")
      .toLowerCase()
      .replace(/[̀-ͯ]/g, "")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 30)
      .replace(/-+$/g, "") || "demo";
  const tag = Array.from({ length: 4 }, () => "abcdefghjkmnpqrstuvwxyz23456789"[Math.floor(rand() * 31)]).join("");
  return `owner-${tag}@${slug}.invalid`;
}

/** Whether an address is one a new test account may take. */
export function isTestEmail(email: string): boolean {
  return /^[^\s@]+@[^\s@]+\.invalid$/i.test(email.trim());
}

/** "Small batch of tickets and knowledge base" for the add data button and toasts. */
export function batchLine(size: SampleSize, kinds: SampleKind[]): string {
  const names = orderKinds(kinds).map((k) => inSentence(t(`kind.${k}`)));
  return t(`batch.${size}`, { list: names.length ? listOf(names) : t("batch.nothing") });
}

