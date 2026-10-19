import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { Member, SupportChannelConnection, SupportChannelDestination, User, Workspace } from "../api/types";
import { qk } from "../queries";
import { useSession } from "../session";
import { env } from "../test/fixtures";
import { applyEnvelope } from "./apply";

const member = (over: Partial<Member["user"]> = {}, role: Member["role"] = "agent"): Member => ({
  user: { id: "u1", name: "Me", email: "me@example.com", avatar_url: null, avatar_source: null, ...over },
  role,
  availability: "online",
  presence: "online",
  max_open_tickets: null,
  open_tickets: 0,
  last_seen_at: null,
  created_at: "2026-01-01T00:00:00Z",
});

describe("picture and workspace events", () => {
  it("updates your picture in place without reloading the session", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.members, [member()]);
    useSession.setState({ status: "authed", user: { id: "u1", name: "Me", email: "me@example.com", avatar_url: null, avatar_source: null } as User });
    const reload = vi.fn();
    const next = member({ avatar_url: "https://api.test/v1/images/a", avatar_source: "upload" });
    applyEnvelope(qc, env(1, { type: "member.updated", data: { member: next } }), { me: "u1", onSelfMembershipChanged: reload });
    expect(reload).not.toHaveBeenCalled();
    expect(useSession.getState().user?.avatar_url).toBe("https://api.test/v1/images/a");
    expect(qc.getQueryData<Member[]>(qk.members)?.[0].user.avatar_source).toBe("upload");

    applyEnvelope(qc, env(2, { type: "member.updated", data: { member: { ...next, role: "admin" } } }), { me: "u1", onSelfMembershipChanged: reload });
    expect(reload).toHaveBeenCalledOnce();
  });

  it("applies workspace.updated to the cache and the session", () => {
    const qc = new QueryClient();
    const w = { id: "w1", name: "Acme", avatar_url: "https://api.test/v1/images/logo" } as Workspace;
    useSession.setState({ memberships: [{ workspace: { ...w, avatar_url: null }, role: "admin" }], workspaceId: "w1" });
    applyEnvelope(qc, env(3, { type: "workspace.updated", data: { workspace: w } }), { me: "u1" });
    expect(qc.getQueryData<Workspace>(qk.workspace)?.avatar_url).toBe(w.avatar_url);
    expect(useSession.getState().memberships[0].workspace.avatar_url).toBe(w.avatar_url);
  });
});

describe("support channel events", () => {
  it("keeps connection and destination lists current", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.supportChannelConnections, []);
    qc.setQueryData(qk.supportChannelDestinations, []);
    const connection = { id: "c1", provider: "slack", name: "Support" } as SupportChannelConnection;
    const destination = { id: "d1", connection_id: "c1", external_channel_id: "C1" } as SupportChannelDestination;

    applyEnvelope(qc, env(4, { type: "support_channel_connection.created", data: { support_channel_connection: connection } }), { me: "u1" });
    applyEnvelope(qc, env(5, { type: "support_channel_destination.created", data: { support_channel_destination: destination } }), { me: "u1" });
    expect(qc.getQueryData<SupportChannelConnection[]>(qk.supportChannelConnections)).toEqual([connection]);
    expect(qc.getQueryData<SupportChannelDestination[]>(qk.supportChannelDestinations)).toEqual([destination]);

    applyEnvelope(qc, env(6, { type: "support_channel_connection.deleted", data: { support_channel_connection_id: "c1" } }), { me: "u1" });
    expect(qc.getQueryData<SupportChannelConnection[]>(qk.supportChannelConnections)).toEqual([]);
    expect(qc.getQueryData<SupportChannelDestination[]>(qk.supportChannelDestinations)).toEqual([]);

    applyEnvelope(qc, env(7, { type: "support_channel_destination.created", data: { support_channel_destination: destination } }), { me: "u1" });
    applyEnvelope(qc, env(8, { type: "support_channel_destination.deleted", data: { support_channel_destination_id: "d1" } }), { me: "u1" });
    expect(qc.getQueryData<SupportChannelDestination[]>(qk.supportChannelDestinations)).toEqual([]);
  });
});

describe("Collab events", () => {
  it("refetches what collab.changed names, once per burst, and leaves the stand-in alone", async () => {
    vi.useFakeTimers();
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEnvelope(qc, env(10, { type: "event.hidden", data: {} }), { me: "u1" });
    expect(spy).not.toHaveBeenCalled();
    applyEnvelope(qc, env(11, { type: "collab.changed", data: { account_id: "a1", thread_ids: ["t1"], folders: true } }), { me: "u1", viaUser: true });
    applyEnvelope(qc, env(12, { type: "collab.changed", data: { account_id: "a1", thread_ids: ["t2"], folders: false } }), { me: "u1", viaUser: true });
    expect(spy).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(250);
    const keys = spy.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey));
    expect(keys).toContain(JSON.stringify(qk.mailThreadLists));
    expect(keys).toContain(JSON.stringify(qk.mailCounts));
    expect(keys).toContain(JSON.stringify(qk.mailThread("t1")));
    expect(keys).toContain(JSON.stringify(qk.mailThread("t2")));
    expect(keys).toContain(JSON.stringify(qk.mailFolders("a1")));
    expect(keys.filter((k) => k === JSON.stringify(qk.mailThreadLists))).toHaveLength(1);
    vi.useRealTimers();
  });

  it("refreshes accounts on collab.accounts", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEnvelope(qc, env(13, { type: "collab.accounts", data: { account_id: "a1" } }), { me: "u1", viaUser: true });
    expect(spy.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey))).toContain(JSON.stringify(qk.mailAccounts));
  });
});
