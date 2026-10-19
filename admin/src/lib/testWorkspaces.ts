// Test workspaces (contracts/admin.md "Test workspaces"): the sample data sizes
// and kinds the panel offers, what each adds, how expiry and sample status
// read, and the owner address a new test account is offered.

import type { AdminSampleCounts, AdminSampleData, SampleKind, SampleSize, TestPlan } from "./api/types";
import { plural, relative, shortDate } from "./format";
import type { Tone } from "./tones";

export const SIZES: { value: SampleSize; label: string; hint: string }[] = [
  { value: "small", label: "Small", hint: "A month of a small team, quick to look through" },
  { value: "realistic", label: "Realistic", hint: "Three months of a busy team" },
  { value: "large", label: "Large", hint: "A year of a big team, for long lists and reports" },
];

/** What one batch of each size adds: internal/sample's volumes. */
export const VOLUMES: Record<SampleSize, { members: number; teams: number; contacts: number; companies: number; tickets: number; chats: number; articles: number; collections: number; mailAccounts: number; mailThreads: number; days: number; extras: boolean }> = {
  small: { members: 3, teams: 2, contacts: 30, companies: 6, tickets: 40, chats: 5, articles: 6, collections: 2, mailAccounts: 1, mailThreads: 20, days: 30, extras: false },
  realistic: { members: 8, teams: 4, contacts: 300, companies: 30, tickets: 400, chats: 40, articles: 25, collections: 5, mailAccounts: 2, mailThreads: 120, days: 90, extras: true },
  large: { members: 25, teams: 8, contacts: 3000, companies: 120, tickets: 5000, chats: 300, articles: 80, collections: 8, mailAccounts: 4, mailThreads: 600, days: 365, extras: true },
};

export const KINDS: { value: SampleKind; label: string }[] = [
  { value: "tickets", label: "Tickets" },
  { value: "chat", label: "Live chat" },
  { value: "kb", label: "Knowledge base" },
  { value: "team", label: "Teammates" },
  { value: "collab", label: "Collab mail" },
  { value: "history", label: "History" },
];

const n = (v: number) => v.toLocaleString("en-US");

/** What a kind adds at a size, in a sentence. */
export function kindAdds(kind: SampleKind, size: SampleSize): string {
  const v = VOLUMES[size];
  switch (kind) {
    case "tickets":
      return `About ${n(v.tickets)} open, pending and on-hold tickets from ${n(v.contacts)} customers at ${n(v.companies)} companies, with labels and assignees${v.extras ? ", saved views and saved replies" : ""}`;
    case "chat":
      return `${n(v.chats)} widget chats, some waiting for a reply right now`;
    case "kb":
      return `${n(v.articles)} help center articles in ${n(v.collections)} collections`;
    case "team":
      return `${n(v.members)} teammates in ${n(v.teams)} teams; they have no password and never sign in`;
    case "collab":
      return `${n(v.mailAccounts)} demo mail ${v.mailAccounts === 1 ? "account" : "accounts"} with ${n(v.mailThreads)} threads, never connected to a provider`;
    case "history":
      return `Solved and closed tickets with ratings across the last ${n(v.days)} days, so reports have something to show`;
  }
}

export const ALL_KINDS: SampleKind[] = KINDS.map((k) => k.value);

/** The kinds in the generator's order, without repeats. */
export function orderKinds(kinds: SampleKind[]): SampleKind[] {
  return ALL_KINDS.filter((k) => kinds.includes(k));
}

export const PLANS: { value: TestPlan; label: string; hint: string }[] = [
  { value: "free", label: "Free", hint: "The free plan's limits" },
  { value: "pro", label: "Pro", hint: "No mailbox or member limit, $20 of AI a month and a $50 AI grant" },
  { value: "collab", label: "Collab", hint: "Complimentary Collab until the workspace is deleted" },
];

export type DeleteChoice = "1" | "7" | "14" | "30" | "90" | "365" | "keep";
export const DELETE_CHOICES: { value: DeleteChoice; label: string }[] = [
  { value: "1", label: "After a day" },
  { value: "7", label: "After a week" },
  { value: "14", label: "After 2 weeks" },
  { value: "30", label: "After 30 days" },
  { value: "90", label: "After 90 days" },
  { value: "365", label: "After a year" },
  { value: "keep", label: "Keep until deleted" },
];

/** delete_after_days for a choice: null keeps the workspace. */
export function deleteAfterDays(choice: DeleteChoice): number | null {
  return choice === "keep" ? null : Number(choice);
}

const DAY = 86_400_000;

/** How a workspace's expiry reads: kept, overdue (the hourly cleanup takes it next), soon, or a date. */
export function expiryLabel(iso: string | null, now = Date.now()): { label: string; tone: Tone } {
  if (!iso) return { label: "Kept", tone: "gray" };
  const left = Date.parse(iso) - now;
  if (left <= 0) return { label: "Deleting now", tone: "red" };
  if (left < 3 * DAY) return { label: `Deletes ${relative(iso, now)}`, tone: "amber" };
  return { label: `Deletes ${shortDate(iso, now)}`, tone: "gray" };
}

/** How a test account's password expiry reads. */
export function passwordLabel(iso: string | null, now = Date.now()): { label: string; tone: Tone } | null {
  if (!iso) return null;
  const left = Date.parse(iso) - now;
  if (left <= 0) return { label: "Password expired", tone: "red" };
  if (left < 3 * DAY) return { label: `Password expires ${relative(iso, now)}`, tone: "amber" };
  return { label: `Password until ${shortDate(iso, now)}`, tone: "gray" };
}

const COUNT_PARTS: [keyof AdminSampleCounts, string][] = [
  ["tickets", "ticket"],
  ["messages", "message"],
  ["chats", "chat"],
  ["contacts", "contact"],
  ["companies", "company"],
  ["articles", "article"],
  ["members", "teammate"],
  ["teams", "team"],
  ["mail_threads", "mail thread"],
];

/** "120 tickets, 640 messages, 14 articles": what sample data added, the zeros left out. */
export function countsLine(c: AdminSampleCounts | null, max = COUNT_PARTS.length): string {
  if (!c) return "";
  const parts = COUNT_PARTS.filter(([k]) => c[k] > 0).map(([k, one]) => plural(c[k], one, one === "company" ? "companies" : undefined));
  return parts.slice(0, max).join(", ");
}

export function isRunning(s: AdminSampleData) {
  return s.status === "running";
}

/** A short line for the status: "Adding realistic data", "3 batches", the error. */
export function sampleLine(s: AdminSampleData): string {
  if (s.status === "running") return `Adding ${s.size || "sample"} data`;
  if (s.status === "failed") return s.error || "The last batch failed.";
  if (s.status === "done") return countsLine(s.counts, 4) || "Added";
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
  const names = orderKinds(kinds).map((k) => KINDS.find((x) => x.value === k)!.label.toLowerCase());
  const list = names.length > 1 ? `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}` : (names[0] ?? "nothing");
  return `${SIZES.find((s) => s.value === size)!.label} batch of ${list}`;
}

