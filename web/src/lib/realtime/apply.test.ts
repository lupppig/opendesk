import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { inWorkspace } from "../api/client";
import type { Activity, Member, Message, PhoneConnection, SupportChannelConnection, SupportChannelDestination, Thread, User, Workspace } from "../api/types";
import { qk } from "../queries";
import { useSession } from "../session";
import { env, ticket } from "../test/fixtures";
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

  it("keeps what Ollie may do current", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.supportChannelOllie, { actions: ["reply", "note"] });
    applyEnvelope(qc, env(9, { type: "support_channel_ollie.updated", data: { support_channel_ollie: { actions: ["note"] } } }), { me: "u1" });
    expect(qc.getQueryData(qk.supportChannelOllie)).toEqual({ actions: ["note"] });
  });
});

describe("phone events", () => {
  it("keeps connections current and a call entry following its call", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.phoneConnections, []);
    const connection = { id: "p1", provider: "aircall", name: "Support line" } as PhoneConnection;
    applyEnvelope(qc, env(4, { type: "phone_connection.created", data: { phone_connection: connection } }), { me: "u1" });
    expect(qc.getQueryData<PhoneConnection[]>(qk.phoneConnections)).toEqual([connection]);
    applyEnvelope(qc, env(5, { type: "phone_connection.deleted", data: { phone_connection_id: "p1" } }), { me: "u1" });
    expect(qc.getQueryData<PhoneConnection[]>(qk.phoneConnections)).toEqual([]);

    const ringing: Activity = { id: "a1", ticket_id: "t1", actor: { type: "system", user: null }, type: "call", data: { stage: "ringing" }, created_at: "2026-10-27T10:00:00Z" };
    qc.setQueryData(qk.activities("t1"), []);
    applyEnvelope(qc, env(6, { type: "activity.created", data: { ticket_id: "t1", activity: ringing } }), { me: "u1" });
    applyEnvelope(qc, env(7, { type: "activity.updated", data: { ticket_id: "t1", activity: { ...ringing, data: { stage: "ended", outcome: "answered" } } } }), { me: "u1" });
    const list = qc.getQueryData<Activity[]>(qk.activities("t1"));
    expect(list).toHaveLength(1);
    expect(list?.[0].data).toEqual({ stage: "ended", outcome: "answered" });
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

describe("sidebar counts", () => {
  // Each test has a workspace of its own: the coalescing timers are per workspace.
  const updated = (wid: string, seq: number) => env(seq, { workspace_id: wid, data: { ticket: ticket({ id: `t${seq}` }) } });

  it("refetches the counts every two seconds at most through a burst, and after its last event", async () => {
    vi.useFakeTimers();
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    const keys = inWorkspace("w-burst", () => [qk.counts, qk.of("tickets", "everywhere"), qk.overview]);
    const refetches = () => spy.mock.calls.filter((c) => JSON.stringify(c[0]?.queryKey) === JSON.stringify(keys[0])).length;
    const burst = async (from: number) => {
      for (let seq = from; seq < from + 10; seq++) {
        applyEnvelope(qc, updated("w-burst", seq), { me: "u1" });
        await vi.advanceTimersByTimeAsync(150);
      }
    };
    await burst(0); // 1.5 s of events
    expect(refetches()).toBe(0);
    await vi.advanceTimersByTimeAsync(500);
    expect(refetches()).toBe(1);
    for (const queryKey of keys) expect(spy).toHaveBeenCalledWith({ queryKey }, { cancelRefetch: false });
    await burst(10);
    await vi.advanceTimersByTimeAsync(500);
    expect(refetches()).toBe(2);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(refetches()).toBe(2);
    vi.useRealTimers();
  });

  it("lets a counts read in flight finish, then reads again", async () => {
    vi.useFakeTimers();
    const qc = new QueryClient();
    const answers: ((n: number) => void)[] = [];
    const signals: AbortSignal[] = [];
    const observer = new QueryObserver(qc, {
      queryKey: inWorkspace("w-slow", () => qk.counts),
      queryFn: ({ signal }) => {
        signals.push(signal);
        return new Promise<number>((resolve) => answers.push(resolve));
      },
    });
    const stop = observer.subscribe(() => {});
    await vi.advanceTimersByTimeAsync(0);
    expect(answers).toHaveLength(1);

    applyEnvelope(qc, updated("w-slow", 30), { me: "u1" });
    await vi.advanceTimersByTimeAsync(2000);
    // Still running: neither abandoned nor joined by a second read.
    expect(answers).toHaveLength(1);
    expect(signals[0].aborted).toBe(false);

    answers[0](1);
    await vi.advanceTimersByTimeAsync(2000);
    expect(answers).toHaveLength(2);
    answers[1](2);
    await vi.advanceTimersByTimeAsync(0);
    expect(observer.getCurrentResult().data).toBe(2);
    stop();
    vi.useRealTimers();
  });
});

describe("ticket message events", () => {
  it("removes a deleted note from the ticket cache", () => {
    const qc = new QueryClient();
    qc.setQueryData<Message[]>(qk.messages("t1"), [{ id: "m1", ticket_id: "t1" } as Message, { id: "m2", ticket_id: "t1" } as Message]);
    applyEnvelope(qc, env(13, { type: "message.deleted", data: { ticket_id: "t1", message_id: "m1" } }), { me: "u1" });
    expect(qc.getQueryData<Message[]>(qk.messages("t1"))?.map((m) => m.id)).toEqual(["m2"]);
  });

  it("keeps a deleted chat reply's marker, without its action card, when it leaves the visitor's widget", () => {
    const qc = new QueryClient();
    const card = { id: "a1", status: "pending" } as unknown as Message["ai_action"];
    qc.setQueryData<Message[]>(qk.messages("t1"), [
      { id: "m1", ticket_id: "t1", kind: "reply", body_text: "Wrong answer", ai_action: card } as Message,
      { id: "m2", ticket_id: "t1" } as Message,
    ]);
    const marker = { id: "m1", ticket_id: "t1", kind: "reply", body_html: "", body_text: "", attachments: [], ai_action: null, deleted_at: "2026-10-23T10:00:00Z", deleted_by: { id: "u2", name: "Ana" } };
    applyEnvelope(qc, env(14, { type: "message.updated", data: { ticket_id: "t1", message: marker } }), { me: "u1" });
    applyEnvelope(qc, env(15, { type: "message.withdrawn", data: { ticket_id: "t1", message_id: "m1" }, visibility: "public" }), { me: "u1" });
    const list = qc.getQueryData<Message[]>(qk.messages("t1"));
    expect(list?.map((m) => m.id)).toEqual(["m1", "m2"]);
    expect(list?.[0]).toMatchObject({ deleted_at: "2026-10-23T10:00:00Z", body_text: "", ai_action: null });
  });

  it("turns a reply taken back into a note where it was", () => {
    const qc = new QueryClient();
    qc.setQueryData<Message[]>(qk.messages("t1"), [
      { id: "m1", ticket_id: "t1", kind: "reply" } as Message,
      { id: "m2", ticket_id: "t1", kind: "reply" } as Message,
    ]);
    applyEnvelope(qc, env(16, { type: "message.withdrawn", data: { ticket_id: "t1", message_id: "m1" }, visibility: "public" }), { me: "u1" });
    applyEnvelope(qc, env(17, { type: "message.updated", data: { ticket_id: "t1", message: { id: "m1", ticket_id: "t1", kind: "note" } } }), { me: "u1" });
    expect(qc.getQueryData<Message[]>(qk.messages("t1"))?.map((m) => [m.id, m.kind])).toEqual([
      ["m1", "note"],
      ["m2", "reply"],
    ]);
  });
});

describe("third-party threads", () => {
  const thread = (over: Partial<Thread> = {}): Thread => ({
    id: "th1",
    ticket_id: "t1",
    subject: "Payout for order 1182",
    include_history: false,
    to: ["ops@acquirer.test"],
    cc: [],
    mailbox_id: "mb1",
    status: "open",
    created_by: null,
    message_count: 1,
    last_message_at: "2026-10-19T10:00:00Z",
    created_at: "2026-10-19T10:00:00Z",
    updated_at: "2026-10-19T10:00:00Z",
    ...over,
  });

  it("keeps the ticket's threads current", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.threads("t1"), []);
    applyEnvelope(qc, env(1, { type: "thread.created", data: { ticket_id: "t1", thread: thread() } }), { me: "u1" });
    applyEnvelope(qc, env(2, { type: "thread.updated", data: { ticket_id: "t1", thread: thread({ status: "closed", message_count: 2 }) } }), { me: "u1" });
    const list = qc.getQueryData<Thread[]>(qk.threads("t1"));
    expect(list).toHaveLength(1);
    expect(list?.[0]).toMatchObject({ status: "closed", message_count: 2 });
  });

  it("does not take a plain note's optimistic place with a thread message", () => {
    const qc = new QueryClient();
    const me = { id: "u1", name: "Me", email: "me@example.com", avatar_url: null };
    const base = {
      ticket_id: "t1",
      kind: "note" as const,
      direction: "outbound" as const,
      author: { type: "user" as const, user: me, contact: null },
      body_text: "",
      has_quoted: false,
      subject: "",
      from_email: "",
      from_name: "Me",
      to: [],
      cc: [],
      bcc: [],
      attachments: [],
      delivery_error: "",
      ai: false,
      created_at: "2026-10-19T10:00:00Z",
      edited_at: null,
    };
    const temp: Message = { ...base, id: "tmp-1", body_html: "<p>note</p>", delivery_status: "none" };
    qc.setQueryData(qk.messages("t1"), [temp]);
    const threadMsg: Message = { ...base, id: "m2", body_html: "<p>to the partner</p>", to: ["ops@acquirer.test"], delivery_status: "pending", thread_id: "th1" };
    applyEnvelope(qc, env(3, { type: "message.created", data: { ticket_id: "t1", message: threadMsg } }), { me: "u1" });
    const ids = qc.getQueryData<Message[]>(qk.messages("t1"))?.map((m) => m.id);
    expect(ids).toEqual(["tmp-1", "m2"]);
  });
});
