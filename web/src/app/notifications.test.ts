import { describe as group, expect, it } from "vitest";
import { i18n } from "@/i18n";
import type { Notification } from "@/lib/api/types";
import { describe, joinedTeam, notificationPath } from "./Notifications";

const t = i18n.getFixedT("en", "shell");
const joined = (data: Record<string, unknown>): Notification => ({ id: "n1", type: "team_joined", ticket: null, data, read_at: null, created_at: "2026-10-13T09:00:00Z" });

group("team_joined notifications", () => {
  it("names who joined which team", () => {
    const n = joined({ team_id: "t1", team: { id: "t1", name: "Legal", private: false }, user: { id: "u1", name: "Maya Chen" }, by: "u1" });
    expect(describe(n, t)).toBe("Maya Chen joined Legal");
    expect(notificationPath(n)).toBe("/settings/teams/t1");
  });

  it("says a private team's new member can now see its tickets", () => {
    const n = joined({ team_id: "t2", team: { id: "t2", name: "Security", private: true }, user: { id: "u1", name: "Jonas Weber" }, by: "u1" });
    expect(describe(n, t)).toBe("Jonas Weber joined the private team Security and can now see its tickets");
  });

  it("copes with a payload missing names", () => {
    expect(describe(joined({ team_id: "t3" }), t)).toBe("Someone joined a team you lead");
    expect(joinedTeam(joined({ team_id: "t3" }))).toEqual({ id: "t3", name: null, private: false, who: null });
    expect(notificationPath(joined({}))).toBe("/settings/teams");
    expect(describe(joined({ team: { name: "Billing" } }), t)).toBe("Someone joined Billing");
  });

  it("never falls back to the generic line", () => {
    expect(describe(joined({ team: { name: "Billing" }, user: { name: "Lea" } }), t)).not.toBe(t("notifications.type.other"));
  });
});

group("Collab notifications", () => {
  const collab = (type: "collab_assigned" | "collab_mention", data: Record<string, unknown>): Notification => ({ id: "n2", type, ticket: null, data, read_at: null, created_at: "2026-10-13T09:00:00Z" });

  it("leads to the mail conversation", () => {
    expect(notificationPath(collab("collab_assigned", { thread_id: "th1", account_id: "a1" }))).toBe("/mail/assigned/th1");
    expect(notificationPath(collab("collab_mention", { thread_id: "th1", comment_id: "c1" }))).toBe("/mail/all/th1");
  });

  it("names who assigned or mentioned, or says it plainly", () => {
    expect(describe(collab("collab_assigned", { thread_id: "th1", actor: { name: "Maya Chen" } }), t)).toBe("Maya Chen assigned you a conversation");
    expect(describe(collab("collab_mention", { thread_id: "th1" }), t)).toBe("You were mentioned in a comment");
  });
});
