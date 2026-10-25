import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { typing } = vi.hoisted(() => ({ typing: vi.fn(() => Promise.resolve()) }));
vi.mock("../api/endpoints", () => ({
  api: {
    // A token for w1: the manager joins what its claims cover.
    realtimeToken: () => Promise.resolve({ token: `x.${btoa(JSON.stringify({ wid: "w1" }))}.y`, url: "", expires_at: "" }),
    events: () => Promise.resolve({ data: [], latest_seq: 0, resync: false }),
    tickets: { typing },
  },
}));
vi.mock(import("../api/client"), async (importOriginal) => ({ ...(await importOriginal()), getWorkspaceId: () => "w1" }));

import { RealtimeManager } from "./manager";
import { setSocketFactory, type RtChannel, type RtSocket } from "./transport";

const pushes: { topic: string; event: string; payload: object }[] = [];

function fakeSocket(): RtSocket {
  const reply = { receive: () => reply };
  return {
    connect: () => undefined,
    disconnect: () => undefined,
    onOpen: () => undefined,
    onClose: () => undefined,
    onError: () => undefined,
    isConnected: () => true,
    channel: (topic: string): RtChannel => ({
      join: () => reply,
      leave: () => reply,
      on: () => 0,
      off: () => undefined,
      push: (event: string, payload: object) => {
        pushes.push({ topic, event, payload });
        return reply;
      },
      onError: () => undefined,
      onClose: () => undefined,
    }),
  };
}

describe("RealtimeManager.typing", () => {
  let m: RealtimeManager;

  beforeEach(async () => {
    vi.useFakeTimers();
    pushes.length = 0;
    typing.mockClear();
    setSocketFactory(() => fakeSocket());
    m = new RealtimeManager({ qc: new QueryClient(), workspaceId: "w1", userId: "u1" });
    await m.start();
    m.setActiveTicket("t1");
  });
  afterEach(() => {
    m.stop();
    vi.useRealTimers();
  });

  const typingPushes = () => pushes.filter((p) => p.event === "typing");

  it("tells the agents in the room, and the chat's visitor through the API, at most every 2.5 s", () => {
    m.typing("c1");
    m.typing("c1");
    expect(typingPushes()).toEqual([{ topic: "ticket:w1:t1", event: "typing", payload: {} }]);
    expect(typing).toHaveBeenCalledTimes(1);
    expect(typing).toHaveBeenCalledWith("t1", { workspace: "w1" });

    vi.advanceTimersByTime(2500);
    m.typing("c1");
    expect(typingPushes()).toHaveLength(2);
    expect(typing).toHaveBeenCalledTimes(2);
  });

  it("without a contact only tells the room", () => {
    m.typing(null);
    expect(typingPushes()).toHaveLength(1);
    expect(typing).not.toHaveBeenCalled();
  });
});
