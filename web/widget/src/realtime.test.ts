import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Live, type RtChannelLike, type RtSocketLike } from "./realtime";

// A token whose payload carries wid (the socket only reads it, the gateway verifies it).
const token = `x.${btoa(JSON.stringify({ wid: "w1" })).replace(/=+$/, "")}.y`;

function fakeSocket() {
  const pushes: [string, object][] = [];
  const channel: RtChannelLike = {
    join: () => ({ receive: () => undefined }),
    on: () => undefined,
    push: (event, payload) => pushes.push([event, payload]),
    leave: () => undefined,
  };
  const socket: RtSocketLike = {
    connect: () => undefined,
    disconnect: () => undefined,
    onOpen: () => undefined,
    onError: () => undefined,
    onClose: () => undefined,
    channel: () => channel,
  };
  return { socket, pushes };
}

describe("Live.typing", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("tells the team through the API at most every 3 s, never through the socket", () => {
    const { socket, pushes } = fakeSocket();
    const sendTyping = vi.fn(() => Promise.resolve());
    const live = new Live(() => socket, "wss://gw.test/socket/widget", token, "c1", () => Promise.resolve(token), {
      onEvent: () => undefined,
      onTyping: () => undefined,
      sendTyping,
    });
    live.start();

    live.typing("t1");
    live.typing("t1");
    expect(sendTyping).toHaveBeenCalledTimes(1);
    expect(sendTyping).toHaveBeenCalledWith("t1");

    vi.advanceTimersByTime(3000);
    live.typing("t1");
    expect(sendTyping).toHaveBeenCalledTimes(2);

    // A sent message ends the burst: the next keystroke signals again.
    live.typingDone();
    live.typing("t2");
    expect(sendTyping).toHaveBeenLastCalledWith("t2");
    expect(pushes).toEqual([]);
  });

  it("swallows a failed request", async () => {
    const { socket } = fakeSocket();
    const sendTyping = vi.fn(() => Promise.reject(new Error("offline")));
    const live = new Live(() => socket, "wss://gw.test/socket/widget", token, "c1", () => Promise.resolve(token), {
      onEvent: () => undefined,
      onTyping: () => undefined,
      sendTyping,
    });
    live.start();
    live.typing("t1");
    await vi.runAllTimersAsync();
    expect(sendTyping).toHaveBeenCalledTimes(1);
  });
});
