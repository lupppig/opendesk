import { describe, expect, it, vi } from "vitest";
import { deliver, parseRetryAfter, signInbound, TemporaryFailure, type Env, type InboundMessage } from "../src/index";

const RAW = "From: a@example.com\r\nTo: t0k3n@in.opendesk.sh\r\nSubject: Hi\r\n\r\nHello\r\n";
const env: Env = { INBOUND_URL: "https://api.opendesk.sh/v1/inbound/email", INBOUND_SECRET: "s3cret" };

function message(to = "t0k3n+x@in.opendesk.sh") {
  const bytes = new TextEncoder().encode(RAW);
  return {
    from: "sender@example.com",
    to,
    rawSize: bytes.byteLength,
    raw: new Response(bytes).body!,
    setReject: vi.fn(),
  } satisfies InboundMessage;
}

type Step = Response | Error;
function fakeFetch(steps: Step[]) {
  const calls: { url: string; init: RequestInit; body: string }[] = [];
  const fn = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const body = new TextDecoder().decode(init!.body as ArrayBuffer);
    calls.push({ url: String(url), init: init!, body });
    const step = steps.shift();
    if (!step) throw new Error("no more steps");
    if (step instanceof Error) throw step;
    return step;
  });
  return { fetch: fn as unknown as typeof fetch, calls };
}

const opts = (f: typeof fetch, sleep = vi.fn(async () => {})) => ({ fetch: f, sleep, random: () => 0.5, log: () => {} });

describe("deliver", () => {
  it("posts the raw message with the secret on the first try", async () => {
    const { fetch, calls } = fakeFetch([new Response(null, { status: 202 })]);
    const m = message();
    const out = await deliver(m, env, opts(fetch));
    expect(out).toEqual({ kind: "delivered", status: 202, attempts: 1 });
    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe("https://api.opendesk.sh/v1/inbound/email?to=t0k3n%2Bx%40in.opendesk.sh");
    expect(calls[0]!.body).toBe(RAW);
    const h = calls[0]!.init.headers as Record<string, string>;
    expect(h["OD-Inbound-Secret"]).toBeUndefined();
    expect(h["OD-Inbound-Signature"]).toMatch(/^t=\d+,v1=[0-9a-f]{64}$/);
    expect(h["Content-Type"]).toBe("message/rfc822");
    expect(h["OD-Envelope-From"]).toBe("sender@example.com");
    expect(calls[0]!.init.method).toBe("POST");
    expect(calls[0]!.init.signal).toBeInstanceOf(AbortSignal);
    expect(m.setReject).not.toHaveBeenCalled();
  });

  it("retries a 503 and sends the same bytes again", async () => {
    const { fetch, calls } = fakeFetch([new Response(null, { status: 503 }), new Response(null, { status: 200 })]);
    const sleep = vi.fn(async () => {});
    const out = await deliver(message(), env, opts(fetch, sleep));
    expect(out).toMatchObject({ kind: "delivered", attempts: 2 });
    expect(calls.map((c) => c.body)).toEqual([RAW, RAW]);
    expect(sleep).toHaveBeenCalledTimes(1);
    expect(sleep).toHaveBeenCalledWith(250);
  });

  it("retries a network error", async () => {
    const { fetch } = fakeFetch([new TypeError("fetch failed"), new Response(null, { status: 200 })]);
    const out = await deliver(message(), env, opts(fetch));
    expect(out).toMatchObject({ kind: "delivered", attempts: 2 });
  });

  it("rejects without retrying on 400", async () => {
    const { fetch, calls } = fakeFetch([new Response(null, { status: 400 })]);
    const m = message();
    const out = await deliver(m, env, opts(fetch));
    expect(out).toEqual({ kind: "rejected", status: 400, reason: "Message could not be parsed" });
    expect(calls).toHaveLength(1);
    expect(m.setReject).toHaveBeenCalledWith("Message could not be parsed");
  });

  it("rejects unknown forwarding addresses on 404", async () => {
    const { fetch } = fakeFetch([new Response(null, { status: 404 })]);
    const m = message();
    await deliver(m, env, opts(fetch));
    expect(m.setReject).toHaveBeenCalledWith("Unknown forwarding address");
  });

  it("fails temporarily without retrying on other 4xx", async () => {
    const { fetch, calls } = fakeFetch([new Response(null, { status: 401 })]);
    const m = message();
    await expect(deliver(m, env, opts(fetch))).rejects.toBeInstanceOf(TemporaryFailure);
    expect(calls).toHaveLength(1);
    expect(m.setReject).not.toHaveBeenCalled();
  });

  it("refuses to run without a secret", async () => {
    const { fetch, calls } = fakeFetch([]);
    await expect(deliver(message(), { INBOUND_URL: env.INBOUND_URL }, opts(fetch))).rejects.toBeInstanceOf(
      TemporaryFailure,
    );
    expect(calls).toHaveLength(0);
  });

  it("gives up after four attempts and throws", async () => {
    const { fetch, calls } = fakeFetch([500, 502, 503, 504].map((s) => new Response(null, { status: s })));
    const sleep = vi.fn(async () => {});
    const m = message();
    await expect(deliver(m, env, opts(fetch, sleep))).rejects.toThrow(/status 504/);
    expect(calls).toHaveLength(4);
    expect(sleep.mock.calls.map((c) => (c as unknown as [number])[0])).toEqual([250, 1000, 3000]);
    expect(m.setReject).not.toHaveBeenCalled();
  });

  it("waits for a short Retry-After on 429", async () => {
    const { fetch } = fakeFetch([
      new Response(null, { status: 429, headers: { "Retry-After": "2" } }),
      new Response(null, { status: 200 }),
    ]);
    const sleep = vi.fn(async () => {});
    await deliver(message(), env, opts(fetch, sleep));
    expect(sleep).toHaveBeenCalledWith(2000);
  });

  it("gives up early on a long Retry-After so the sender retries", async () => {
    const { fetch, calls } = fakeFetch([new Response(null, { status: 503, headers: { "Retry-After": "120" } })]);
    const sleep = vi.fn(async () => {});
    await expect(deliver(message(), env, opts(fetch, sleep))).rejects.toBeInstanceOf(TemporaryFailure);
    expect(calls).toHaveLength(1);
    expect(sleep).not.toHaveBeenCalled();
  });
});

describe("parseRetryAfter", () => {
  it("reads seconds and dates", () => {
    expect(parseRetryAfter("3")).toBe(3000);
    expect(parseRetryAfter(new Date(10_000).toUTCString(), 4_000)).toBe(6000);
    expect(parseRetryAfter("soon")).toBeNull();
    expect(parseRetryAfter(null)).toBeNull();
  });
});

describe("signInbound", () => {
  it("matches the API's HMAC-SHA256 over the timestamp, recipient, sender and message", async () => {
    const body = new TextEncoder().encode("Subject: hi\r\n\r\nhello").buffer as ArrayBuffer;
    // The same HMAC computed outside JavaScript (internal/email SignInbound signs alike).
    const want = "c030b4033566ca50f8d5b887d26b405893d76b89ebaac855375d1978999d9e47";
    expect(await signInbound("s3cret", 1700000000, "t0k3n@in.opendesk.sh", "sender@example.com", body)).toBe(`t=1700000000,v1=${want}`);
  });
});
