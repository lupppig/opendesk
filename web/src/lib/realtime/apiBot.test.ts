import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { Message } from "../api/types";
import { qk } from "../queries";
import { OLLIE_LOOK } from "../botlook";
import { env } from "../test/fixtures";
import { applyEnvelope } from "./apply";

const message: Message = {
  id: "api-message", ticket_id: "chat", kind: "reply", direction: "outbound",
  author: { type: "api", user: null, contact: null, bot: { id: "julia", name: "Julia", avatar_url: "", look: OLLIE_LOOK }, api_key: null },
  body_html: "<p>Hello</p>", body_text: "Hello", has_quoted: false, subject: "", from_email: "", from_name: "Julia",
  to: [], cc: [], bcc: [], attachments: [], delivery_status: "none", delivery_error: "", ai: false, ai_action: null, ai_rating: null,
  created_at: "2026-10-30T12:00:00Z", edited_at: null,
};
const full: Message = { ...message, author: { ...message.author, api_key: { id: "key", name: "Replai" } } };
const key = qk.messages("chat");

describe("API bot attribution in the agent timeline", () => {
  for (const type of ["message.created", "message.updated"]) {
    it(`${type} refetches attribution from a customer-safe event`, () => {
      const qc = new QueryClient();
      qc.setQueryData(key, []);
      applyEnvelope(qc, env(1, { type, visibility: "public", data: { ticket_id: "chat", message } }), { me: "agent" });
      expect(qc.getQueryState(key)?.isInvalidated).toBe(true);
      expect(qc.getQueryData<Message[]>(key)?.[0].author.bot?.name).toBe("Julia");
      qc.clear();
    });

    it(`${type} preserves a known key while applying the updated body`, () => {
      const qc = new QueryClient();
      qc.setQueryData(key, [full]);
      applyEnvelope(qc, env(2, { type, visibility: "public", data: { ticket_id: "chat", message: { ...message, body_text: "Updated" } } }), { me: "agent" });
      expect(qc.getQueryData<Message[]>(key)?.[0]).toMatchObject({ body_text: "Updated", author: { api_key: full.author.api_key }, ai: false });
      expect(qc.getQueryData<Message[]>(key)).toHaveLength(1);
      qc.clear();
    });
  }

  it("refetches an existing incomplete API author even without a bot", () => {
    const qc = new QueryClient();
    const plain = { ...message, author: { ...message.author, bot: null } };
    qc.setQueryData(key, [plain]);
    applyEnvelope(qc, env(3, { type: "message.updated", data: { ticket_id: "chat", message: plain } }), { me: "agent" });
    expect(qc.getQueryState(key)?.isInvalidated).toBe(true);
    qc.clear();
  });

  it("uses a protected event's current key name without a refetch", () => {
    const qc = new QueryClient();
    qc.setQueryData(key, [full]);
    const renamed = { ...full, author: { ...full.author, api_key: { id: "key", name: "Renamed" } } };
    applyEnvelope(qc, env(4, { type: "message.updated", data: { ticket_id: "chat", message: renamed } }), { me: "agent" });
    expect(qc.getQueryData<Message[]>(key)?.[0].author.api_key?.name).toBe("Renamed");
    expect(qc.getQueryState(key)?.isInvalidated).toBe(false);
    qc.clear();
  });

  it("completes an active timeline from its protected query and survives another public event", async () => {
    const qc = new QueryClient();
    qc.setQueryData(key, []);
    const observer = new QueryObserver(qc, { queryKey: key, queryFn: async () => [full], staleTime: Infinity });
    const stop = observer.subscribe(() => {});
    try {
      applyEnvelope(qc, env(5, { type: "message.created", visibility: "public", data: { ticket_id: "chat", message } }), { me: "agent" });
      await vi.waitFor(() => expect(observer.getCurrentResult().data?.[0].author.api_key?.name).toBe("Replai"));
      applyEnvelope(qc, env(6, { type: "message.updated", visibility: "public", data: { ticket_id: "chat", message } }), { me: "agent" });
      expect(observer.getCurrentResult().data?.[0].author.api_key?.name).toBe("Replai");
    } finally { stop(); qc.clear(); }
  });

  it("leaves human and built-in AI messages complete without API refetches", () => {
    const qc = new QueryClient();
    qc.setQueryData(key, []);
    const authorTypes: Message["author"]["type"][] = ["user", "system"];
    for (const authorType of authorTypes) {
      const other: Message = { ...message, id: authorType, author: { ...message.author, type: authorType }, ai: authorType === "system" };
      applyEnvelope(qc, env(7, { type: "message.created", data: { ticket_id: "chat", message: other } }), { me: "agent" });
    }
    expect(qc.getQueryState(key)?.isInvalidated).toBe(false);
    qc.clear();
  });
});
