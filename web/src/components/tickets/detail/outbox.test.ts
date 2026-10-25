import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { queryClient } from "@/lib/queryClient";

const send = vi.fn();
const editMessage = vi.fn();
vi.mock("@/lib/api/endpoints", () => ({
  api: { tickets: { send: (...a: unknown[]) => send(...a), editMessage: (...a: unknown[]) => editMessage(...a) } },
}));
const info = vi.fn();
const error = vi.fn();
vi.mock("@/components/ui/toast", () => ({ toast: { held: () => "toast", dismiss: () => {}, info: (...a: unknown[]) => info(...a), error: (...a: unknown[]) => error(...a) } }));

const { asNote, hold, release, undo, undoSeconds, useHeld, useUnsent } = await import("./outbox");
const { inWorkspace, setActiveAccount, setWorkspaceId } = await import("@/lib/api/client");
const { runBeforeSignOut, useSession } = await import("@/lib/session");

/** What a request sent with no account or workspace pinned carries. */
const plain = { keepalive: false, as: undefined, workspace: undefined };

const temp = (id: string): Message =>
  ({
    id,
    ticket_id: "t1",
    kind: "reply",
    direction: "outbound",
    author: { type: "user", user: { id: "u1", name: "Maya", email: "maya@x.test", avatar_url: null }, contact: null },
    body_html: "<p>hi</p>",
    to: ["c@x.test"],
    cc: ["cc@x.test"],
    bcc: [],
    attachments: [],
    delivery_status: "pending",
    created_at: new Date().toISOString(),
  }) as unknown as Message;

const body = { kind: "reply" as const, body_html: "<p>hi</p>", cc: ["cc@x.test"], status: "solved" as const, mailbox_id: "mb2" };
const unsent = { html: "<p>hi</p>", cc: ["cc@x.test"], bcc: [], uploads: [] };
const list = () => queryClient.getQueryData<Message[]>(qk.messages("t1")) ?? [];

describe("outbox", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    send.mockReset();
    editMessage.mockReset();
    info.mockReset();
    error.mockReset();
    setActiveAccount(null);
    setWorkspaceId(null);
    send.mockImplementation((_t: string, b: { kind: string }) => Promise.resolve({ ...temp("m1"), kind: b.kind, delivery_status: "sent" }));
    queryClient.setQueryData<Message[]>(qk.messages("t1"), []);
    useUnsent.setState({}, true);
  });
  afterEach(() => vi.useRealTimers());

  it("holds a reply and sends nothing until its window ends", async () => {
    hold(temp("tmp-a"), body, unsent, "u1", 10);
    expect(list()[0].delivery_status).toBe("scheduled");
    expect(useHeld.getState()["tmp-a"]).toBeGreaterThan(Date.now());
    vi.advanceTimersByTime(9_000);
    expect(send).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1_000);
    expect(send).toHaveBeenCalledWith("t1", body, plain);
    await vi.advanceTimersByTimeAsync(0);
    expect(list().map((m) => m.id)).toEqual(["m1"]);
    expect(useHeld.getState()["tmp-a"]).toBeUndefined();
  });

  it("takes an undone reply back to its composer, unsent", () => {
    hold(temp("tmp-b"), body, unsent, "u1", 10);
    undo("tmp-b");
    vi.advanceTimersByTime(20_000);
    expect(send).not.toHaveBeenCalled();
    expect(list()).toEqual([]);
    expect(useUnsent.getState().t1).toEqual(unsent);
  });

  it("adds a held reply as a note, without recipients, address or status", () => {
    hold(temp("tmp-c"), body, unsent, "u1", 10);
    asNote("tmp-c");
    expect(send).toHaveBeenCalledWith("t1", { kind: "note", body_html: "<p>hi</p>", attachment_ids: undefined }, plain);
    expect(list()[0]).toMatchObject({ kind: "note", delivery_status: "none", cc: [], to: [] });
    vi.advanceTimersByTime(20_000);
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("sends at once with no window, and on demand", () => {
    hold(temp("tmp-d"), body, unsent, "u1", 0);
    expect(send).toHaveBeenCalledTimes(1);
    hold(temp("tmp-e"), body, unsent, "u1", 30);
    release("tmp-e");
    expect(send).toHaveBeenCalledTimes(2);
    release("tmp-e");
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("keeps the note a reply was made from when the reply becomes a note, and sends its id with the reply", async () => {
    queryClient.setQueryData<Message[]>(qk.messages("t1"), [{ ...temp("n1"), kind: "note" }]);
    const fromNote = { ...unsent, fromNote: "n1" };
    hold(temp("tmp-f"), body, fromNote, "u1", 10);
    asNote("tmp-f");
    // The words are the note's own, unchanged: nothing is posted or edited.
    expect(send).not.toHaveBeenCalled();
    expect(editMessage).not.toHaveBeenCalled();
    expect(list().map((m) => m.id)).toEqual(["n1"]);
    hold(temp("tmp-g"), body, fromNote, "u1", 10);
    undo("tmp-g");
    expect(useUnsent.getState().t1.fromNote).toBe("n1");
    const withNote = { ...body, from_note_id: "n1" };
    hold(temp("tmp-h"), withNote, fromNote, "u1", 10);
    vi.advanceTimersByTime(10_000);
    await vi.advanceTimersByTimeAsync(0);
    expect(send).toHaveBeenCalledWith("t1", withNote, plain);
    expect(list().map((m) => [m.id, m.sent_as_reply_id ?? null])).toEqual([
      ["n1", "m1"],
      ["m1", null],
    ]);
  });

  it("puts edited words back into the note a reply was made from, instead of adding another note", async () => {
    queryClient.setQueryData<Message[]>(qk.messages("t1"), [{ ...temp("n1"), kind: "note" }]);
    editMessage.mockImplementation((_t: string, id: string, b: { body_html: string }) => Promise.resolve({ ...temp(id), kind: "note", body_html: b.body_html }));
    const edited = { ...body, body_html: "<p>hi, edited</p>", from_note_id: "n1" };
    hold(temp("tmp-i"), edited, { ...unsent, fromNote: "n1" }, "u1", 10);
    asNote("tmp-i");
    await vi.advanceTimersByTimeAsync(0);
    expect(send).not.toHaveBeenCalled();
    expect(editMessage).toHaveBeenCalledWith("t1", "n1", { body_html: "<p>hi, edited</p>" });
    expect(list().map((m) => [m.id, m.body_html])).toEqual([["n1", "<p>hi, edited</p>"]]);
  });

  it("waits five seconds for an email reply and none for a chat reply, unless the workspace says otherwise", () => {
    expect(undoSeconds(undefined, "email")).toBe(5);
    expect(undoSeconds(undefined, "chat")).toBe(0);
    expect(undoSeconds({ undo_send_seconds: 0, undo_send_chat_seconds: 10 }, "email")).toBe(0);
    expect(undoSeconds({ undo_send_seconds: 0, undo_send_chat_seconds: 10 }, "chat")).toBe(10);
    expect(undoSeconds({}, "portal")).toBe(5);
  });

  it("sends a held reply at once, where it was written, when the workspace changes", async () => {
    setActiveAccount("u1");
    setWorkspaceId("w1");
    useSession.setState({ workspaceId: "w1" });
    queryClient.setQueryData<Message[]>(qk.messages("t1"), []);
    hold(temp("tmp-j"), body, unsent, "u1", 30);
    expect(send).not.toHaveBeenCalled();
    setWorkspaceId("w2");
    useSession.setState({ workspaceId: "w2" });
    expect(send).toHaveBeenCalledWith("t1", body, { keepalive: false, as: "u1", workspace: "w1" });
    expect(useHeld.getState()["tmp-j"]).toBeUndefined();
    // What it sent lands in the workspace it was written in, not the one in focus.
    await vi.advanceTimersByTimeAsync(0);
    expect(inWorkspace("w1", list).map((m) => m.id)).toEqual(["m1"]);
    expect(queryClient.getQueryData(qk.messages("t1"))).toBeUndefined();
  });

  it("sends held replies before signing out, and warns when one fails", async () => {
    setActiveAccount("u1");
    setWorkspaceId("w1");
    send.mockImplementation(() => Promise.reject(new Error("gone")));
    hold(temp("tmp-k"), body, unsent, "u1", 30);
    await runBeforeSignOut();
    expect(send).toHaveBeenCalledWith("t1", body, { keepalive: false, as: "u1", workspace: "w1" });
    expect(error).toHaveBeenCalledTimes(1);
  });

  it("sends at once a reply too long to carry out of a closing tab", () => {
    const long = { ...body, body_html: `<p>${"x".repeat(70_000)}</p>` };
    hold(temp("tmp-l"), long, unsent, "u1", 10);
    expect(send).toHaveBeenCalledWith("t1", long, plain);
    expect(info).toHaveBeenCalledTimes(1);
    expect(useHeld.getState()["tmp-l"]).toBeUndefined();
  });
});
