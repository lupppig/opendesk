// Notification preferences and the unsubscribe link (contracts/api.md "Notification
// preferences and emails"). Channels are per workspace and member; the email
// schedule and the pause are the account's.

import type { EmailCadence, NotificationPreferences, NotificationType, NotificationTypePrefs } from "@/lib/api/types";
import { ADMIN_GROUPS, DEFAULT_PACING, MAX_PAUSE_MS, NOTIFICATION_DEFAULTS, NOTIFICATION_ORDER, URGENT_TYPES } from "@/lib/notificationPrefs";
import { db, me, type NotificationSettingsRow, type WsData } from "../db";
import { HttpError, get, invalid, patch, post, respond } from "../router";
import { isRecord } from "../util";

const CADENCES: EmailCadence[] = ["instant", "hourly", "daily", "off"];
const CHANNELS = ["email", "push", "desktop"] as const;

function settingsOf(uid: string): NotificationSettingsRow {
  return (db.notificationSettings[uid] ??= { email_cadence: "instant", digest_hour: 9, timezone: "UTC", paused_until: null });
}

/** The types a member can receive here: owners and admins also get the AI, billing and workspace notices. */
function typesFor(ws: WsData, uid: string): NotificationType[] {
  const role = ws.members.find((m) => m.user_id === uid)?.role ?? "viewer";
  const admin = role === "owner" || role === "admin";
  return NOTIFICATION_ORDER.filter((t) => admin || !ADMIN_GROUPS.has(NOTIFICATION_DEFAULTS[t].group));
}

export function preferencesOut(ws: WsData, uid: string): NotificationPreferences {
  const s = settingsOf(uid);
  const set = ws.notificationPrefs?.[uid] ?? {};
  const user = db.users.find((u) => u.id === uid);
  const types: NotificationTypePrefs[] = typesFor(ws, uid).map((type) => {
    const { group, defaults } = NOTIFICATION_DEFAULTS[type];
    return { type, group, defaults, urgent: URGENT_TYPES.has(type), ...defaults, ...set[type] };
  });
  return {
    email_cadence: s.email_cadence,
    digest_hour: s.digest_hour,
    timezone: s.timezone,
    paused_until: s.paused_until && Date.parse(s.paused_until) > Date.now() ? s.paused_until : null,
    email: user?.email ?? "",
    email_verified: user?.email_verified ?? false,
    delay_minutes: DEFAULT_PACING.delay_minutes,
    // opendesk Cloud's pacing (contracts/api.md "Notification preferences and emails").
    pacing: { ...DEFAULT_PACING, cooldown_minutes: [...DEFAULT_PACING.cooldown_minutes] },
    unsubscribed_at: ws.notificationUnsubscribes?.[uid]?.at ?? null,
    types,
    // The teams they lead or work in (observers never hear about new tickets).
    teams: ws.teams
      .filter((t) => t.members.some((m) => m.user_id === uid && m.role !== "observer"))
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((t) => ({ id: t.id, name: t.name, notify: !ws.mutedTeams?.[uid]?.includes(t.id) })),
  };
}

function validTimeZone(tz: string) {
  try {
    new Intl.DateTimeFormat("en", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

/** Turns every notification email from this workspace off for the user, keeping the choices from before once. */
export function unsubscribeAll(ws: WsData, uid: string) {
  const prefs = ((ws.notificationPrefs ??= {})[uid] ??= {});
  const unsubs = (ws.notificationUnsubscribes ??= {});
  if (!unsubs[uid]) {
    const previous: Partial<Record<NotificationType, boolean | undefined>> = {};
    for (const t of NOTIFICATION_ORDER) previous[t] = prefs[t]?.email;
    unsubs[uid] = { at: new Date().toISOString(), previous };
  }
  for (const t of NOTIFICATION_ORDER) prefs[t] = { ...prefs[t], email: false };
}

/** Restores each type's email choice from before the unsubscribe; unset ones follow the default again. */
export function resubscribe(ws: WsData, uid: string) {
  const u = ws.notificationUnsubscribes?.[uid];
  if (!u) return;
  const prefs = ((ws.notificationPrefs ??= {})[uid] ??= {});
  for (const t of NOTIFICATION_ORDER) {
    const { email: _off, ...rest } = prefs[t] ?? {};
    prefs[t] = u.previous[t] === undefined ? rest : { ...rest, email: u.previous[t] };
  }
  delete ws.notificationUnsubscribes![uid];
}

export function registerNotificationPrefs() {
  get("/v1/me/notification-preferences", (ctx) => preferencesOut(ctx.ws, me().id));

  patch("/v1/me/notification-preferences", (ctx) => {
    const uid = me().id;
    const b = ctx.body;
    const s = { ...settingsOf(uid) };
    if (b.email_cadence !== undefined) {
      if (!CADENCES.includes(b.email_cadence as EmailCadence)) throw invalid("email_cadence is not one of instant, hourly, daily, off");
      s.email_cadence = b.email_cadence as EmailCadence;
    }
    if (b.digest_hour !== undefined) {
      if (typeof b.digest_hour !== "number" || !Number.isInteger(b.digest_hour) || b.digest_hour < 0 || b.digest_hour > 23) throw invalid("digest_hour is 0 to 23");
      s.digest_hour = b.digest_hour;
    }
    if (b.timezone !== undefined) {
      if (typeof b.timezone !== "string" || !validTimeZone(b.timezone)) throw invalid("timezone is not a known time zone");
      s.timezone = b.timezone;
    }
    if (b.paused_until !== undefined) {
      if (b.paused_until === "" || b.paused_until === null) s.paused_until = null;
      else {
        const at = typeof b.paused_until === "string" ? Date.parse(b.paused_until) : NaN;
        if (!Number.isFinite(at)) throw invalid("paused_until is not a time");
        if (at > Date.now() + MAX_PAUSE_MS + 60_000) throw invalid("paused_until is at most 30 days ahead");
        s.paused_until = new Date(at).toISOString();
      }
    }
    const allowed = typesFor(ctx.ws, uid);
    const changes: [NotificationType, Partial<Record<(typeof CHANNELS)[number], boolean>>][] = [];
    if (b.types !== undefined) {
      if (!Array.isArray(b.types)) throw invalid("types is a list");
      for (const row of b.types) {
        if (!isRecord(row) || !allowed.includes(row.type as NotificationType)) throw invalid(`unknown notification type ${isRecord(row) ? String(row.type) : ""}`.trim());
        const c: Partial<Record<(typeof CHANNELS)[number], boolean>> = {};
        for (const ch of CHANNELS) if (typeof row[ch] === "boolean") c[ch] = row[ch] as boolean;
        changes.push([row.type as NotificationType, c]);
      }
    }
    const teamChanges: { id: string; notify: boolean }[] = [];
    if (b.teams !== undefined) {
      if (!Array.isArray(b.teams) || b.teams.length > 200) throw invalid("teams is a list of at most 200");
      for (const row of b.teams) {
        if (!isRecord(row) || typeof row.id !== "string" || typeof row.notify !== "boolean") throw invalid("each team is {id, notify}");
        teamChanges.push({ id: row.id, notify: row.notify });
      }
    }
    db.notificationSettings[uid] = s;
    // Another team's id changes nothing, as on the server.
    for (const { id, notify } of teamChanges) {
      if (!ctx.ws.teams.some((t) => t.id === id && t.members.some((m) => m.user_id === uid))) continue;
      const muted = new Set((ctx.ws.mutedTeams ??= {})[uid] ?? []);
      if (notify) muted.delete(id);
      else muted.add(id);
      ctx.ws.mutedTeams[uid] = [...muted];
    }
    if (changes.length) {
      const mine = ((ctx.ws.notificationPrefs ??= {})[uid] ??= {});
      for (const [type, c] of changes) mine[type] = { ...mine[type], ...c };
      // Turning an email back on by hand ends the unsubscribe.
      if (changes.some(([, c]) => c.email === true)) delete ctx.ws.notificationUnsubscribes?.[uid];
    }
    return preferencesOut(ctx.ws, uid);
  });

  post("/v1/me/notification-preferences/resubscribe", (ctx) => {
    resubscribe(ctx.ws, me().id);
    return preferencesOut(ctx.ws, me().id);
  });

  post("/v1/me/notification-preferences/test", () => {
    if (!me().email_verified) throw new HttpError(422, "email_unverified", "Confirm your email address first.");
    return respond(204);
  });

  // The mock takes any token: "expired" is refused, a workspace id names that workspace, anything else is the first one.
  post("/v1/notifications/unsubscribe", (ctx) => {
    const token = typeof ctx.body.token === "string" ? ctx.body.token : "";
    if (!token || token === "expired") throw new HttpError(400, "invalid_token", "This link is not valid any more.");
    const ws = db.workspaces.find((w) => w.workspace.id === token) ?? db.workspaces[0];
    const uid = db.meId || ws.members[0].user_id;
    unsubscribeAll(ws, uid);
    const email = db.users.find((u) => u.id === uid)?.email ?? "";
    return respond(200, { workspace: { id: ws.workspace.id, name: ws.workspace.name }, email });
  });
}
