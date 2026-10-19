// An account's automatic reply (contracts/collab.md "Account settings"): whether it is on
// now, and the form the mail account page edits it with (local day and time fields).

import { format, isValid } from "date-fns";
import type { MailAccount, MailOutOfOffice } from "@/lib/api/types";

/** On right now: turned on, and inside its dates when it has them. */
export function isAway(a: Pick<MailAccount, "out_of_office" | "out_of_office_sync">, now = Date.now()): boolean {
  const o = a.out_of_office;
  if (!o || !o.enabled || a.out_of_office_sync === "none") return false;
  if (o.start && Date.parse(o.start) > now) return false;
  if (o.end && Date.parse(o.end) <= now) return false;
  return true;
}

export type OutOfOfficeForm = {
  enabled: boolean;
  /** "yyyy-MM-dd" or "" (none), in local time. */
  startDay: string;
  startTime: string;
  endDay: string;
  endTime: string;
  subject: string;
  message_html: string;
  contacts_only: boolean;
  internal_only: boolean;
};

const split = (iso: string | null): [string, string] => {
  if (!iso) return ["", ""];
  const d = new Date(iso);
  return isValid(d) ? [format(d, "yyyy-MM-dd"), format(d, "HH:mm")] : ["", ""];
};

export function outOfOfficeForm(o: MailOutOfOffice | null): OutOfOfficeForm {
  const [startDay, startTime] = split(o?.start ?? null);
  const [endDay, endTime] = split(o?.end ?? null);
  return {
    enabled: o?.enabled ?? false,
    startDay,
    startTime: startTime || "09:00",
    endDay,
    endTime: endTime || "09:00",
    subject: o?.subject ?? "",
    message_html: o?.message_html ?? "",
    contacts_only: o?.contacts_only ?? false,
    internal_only: o?.internal_only ?? false,
  };
}

/** A local day and time as an ISO instant; null without a day. */
export function joinDay(day: string, time: string): string | null {
  if (!day) return null;
  const d = new Date(`${day}T${time || "00:00"}`);
  return isValid(d) ? d.toISOString() : null;
}

export function outOfOfficeBody(f: OutOfOfficeForm): MailOutOfOffice {
  return {
    enabled: f.enabled,
    start: joinDay(f.startDay, f.startTime),
    end: joinDay(f.endDay, f.endTime),
    subject: f.subject.trim(),
    message_html: f.message_html,
    contacts_only: f.contacts_only,
    internal_only: f.internal_only,
  };
}

/** What is wrong with the form for this provider, as a collab.json key; null when it can be saved. */
export function outOfOfficeProblem(f: OutOfOfficeForm, provider: MailAccount["provider"]): "detail.ooo.endBeforeStart" | "detail.ooo.needBoth" | null {
  const b = outOfOfficeBody(f);
  if (b.start && b.end && Date.parse(b.end) <= Date.parse(b.start)) return "detail.ooo.endBeforeStart";
  if (provider === "microsoft" && b.enabled && !!b.start !== !!b.end) return "detail.ooo.needBoth";
  return null;
}
