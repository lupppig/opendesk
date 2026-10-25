// Asking a teammate's Ollie (contracts/ai.md "Asking a teammate"): the pure parts of the
// caller's sharing settings, shared by the settings page and the mock.

import type { PeerAccount, PeerMode, PeerResult, PeerSettings, PeerSettingsInput } from "@/lib/api/types";

export const PEER_MODES: PeerMode[] = ["verbatim", "summary", "none"];

/** The longest sharing rules the server keeps. */
export const PEER_RULES_MAX = 2000;

/**
 * An account's effective mode: its own choice, else the default for every account, else
 * nothing (chosen accounts only).
 */
export function effectiveMode(allAccounts: boolean, defaultMode: PeerMode, chosen: PeerMode | undefined): PeerMode {
  if (chosen) return chosen;
  return allAccounts ? defaultMode : "none";
}

/** The caller's own accounts with their effective modes, from the explicit choices. */
export function peerAccounts(
  own: Omit<PeerAccount, "mode" | "chosen">[],
  allAccounts: boolean,
  defaultMode: PeerMode,
  choices: Record<string, PeerMode>,
): PeerAccount[] {
  return own.map((a) => ({ ...a, mode: effectiveMode(allAccounts, defaultMode, choices[a.id]), chosen: a.id in choices }));
}

/** The PUT body that keeps everything a settings view shows. */
export function peerInput(s: PeerSettings): PeerSettingsInput {
  return {
    enabled: s.enabled,
    share_with: s.share_with,
    audience: { user_ids: s.audience.users.map((u) => u.id), team_ids: s.audience.teams.map((t) => t.id) },
    all_accounts: s.all_accounts,
    default_mode: s.default_mode,
    accounts: s.accounts.filter((a) => a.chosen).map((a) => ({ id: a.id, mode: a.mode })),
    rules: s.rules,
  };
}

/** Recomputes the accounts' effective modes after the scope or the default changed. */
function remode(s: PeerSettings): PeerSettings {
  return { ...s, accounts: s.accounts.map((a) => (a.chosen ? a : { ...a, mode: effectiveMode(s.all_accounts, s.default_mode, undefined) })) };
}

/** One account set to a mode: an explicit choice from then on. */
export function withAccountMode(s: PeerSettings, id: string, mode: PeerMode): PeerSettings {
  return { ...s, accounts: s.accounts.map((a) => (a.id === id ? { ...a, mode, chosen: true } : a)) };
}

/** Every account (new ones at the default) or only the chosen ones. */
export function withAllAccounts(s: PeerSettings, all: boolean): PeerSettings {
  return remode({ ...s, all_accounts: all });
}

/** The mode of accounts without their own choice. */
export function withDefaultMode(s: PeerSettings, mode: PeerMode): PeerSettings {
  return remode({ ...s, default_mode: mode });
}

/** How many own accounts a teammate's Ollie may draw on now. */
export function sharedAccountCount(s: PeerSettings): number {
  return s.accounts.filter((a) => a.mode !== "none").length;
}

/** The i18n key and count of a step's or an ask's outcome. */
export function peerOutcome(result: PeerResult, sources: number): { key: PeerResult; count: number } {
  return { key: result, count: result === "answered" ? Math.max(0, sources) : 0 };
}
