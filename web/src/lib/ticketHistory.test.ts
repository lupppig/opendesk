import { afterEach, describe, expect, it } from "vitest";
import type { Activity } from "@/lib/api/types";
import { setTicketHistoryShown, ticketHistoryShown, visibleActivities } from "./ticketHistory";

const act = (type: string): Activity => ({ id: type, ticket_id: "t", actor: { type: "user", user: null }, type: type as Activity["type"], data: {}, created_at: "2026-10-30T10:00:00Z" });

afterEach(() => setTicketHistoryShown(true));

describe("ticket history", () => {
  it("is shown by default and remembered per browser", () => {
    expect(ticketHistoryShown()).toBe(true);
    setTicketHistoryShown(false);
    expect(ticketHistoryShown()).toBe(false);
    expect(localStorage.getItem("od-ticket-history")).toBe("hidden");
    setTicketHistoryShown(true);
    expect(localStorage.getItem("od-ticket-history")).toBeNull();
  });

  it("hidden, keeps only the calls, which are part of the conversation", () => {
    const all = [act("status_changed"), act("call"), act("tagged"), act("assigned")];
    expect(visibleActivities(all, true)).toEqual(all);
    expect(visibleActivities(all, false)?.map((a) => a.type)).toEqual(["call"]);
    expect(visibleActivities(undefined, false)).toBeUndefined();
  });
});
