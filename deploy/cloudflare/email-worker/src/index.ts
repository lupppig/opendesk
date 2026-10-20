// opendesk inbound email worker.
//
// Email Routing hands every message sent to <token>@in.opendesk.sh to this
// worker, which POSTs the raw RFC 822 bytes to the API:
//
//   POST {INBOUND_URL}?to=<rcpt>
//   OD-Inbound-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256>
//   OD-Envelope-From: <envelope sender>
//   Content-Type: message/rfc822
//
// The signature is HMAC-SHA256, keyed with INBOUND_SECRET, over
// "<t>.<rcpt>.<envelope sender>." followed by the raw message; the API refuses one
// older than five minutes, so each attempt signs again (CASA 7.2).
//
// Body handling: the message is read once into memory and the same bytes are
// sent on every attempt. A ReadableStream can be consumed only once, so a
// retry needs the bytes anyway, and tee() would buffer the whole message
// regardless. Email Routing caps a message at 25 MiB, well inside a Worker's
// memory, so buffering is bounded. It also lets fetch send a Content-Length
// instead of a chunked body, which the API and the ALB handle best.
//
// Failure handling, and what Email Routing does with each outcome:
//   - setReject(reason): the sending server gets a permanent SMTP rejection
//     with that reason (a bounce). Used only when the API says the message can
//     never be accepted: 400, 404 (unknown forwarding address), 413 (too big).
//   - throwing: the delivery fails as a temporary error, so the sending server
//     keeps the message and retries later. Used when the API is unreachable,
//     answers 5xx/408/429 after every attempt, when the worker is not
//     configured, or for any other unexpected status.

/** The OD-Inbound-Signature value for a message at unix time t. */
export async function signInbound(secret: string, t: number, to: string, from: string, body: ArrayBuffer): Promise<string> {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const prefix = enc.encode(`${t}.${to}.${from}.`);
  const data = new Uint8Array(prefix.byteLength + body.byteLength);
  data.set(prefix, 0);
  data.set(new Uint8Array(body), prefix.byteLength);
  const mac = new Uint8Array(await crypto.subtle.sign("HMAC", key, data));
  return `t=${t},v1=${Array.from(mac, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

export interface Env {
  INBOUND_URL: string;
  INBOUND_SECRET?: string;
}

/** The subset of ForwardableEmailMessage this worker uses. */
export interface InboundMessage {
  readonly from: string;
  readonly to: string;
  readonly raw: ReadableStream<Uint8Array>;
  readonly rawSize: number;
  setReject(reason: string): void;
}

export interface DeliverOptions {
  fetch?: typeof fetch;
  sleep?: (ms: number) => Promise<void>;
  random?: () => number;
  log?: (line: Record<string, unknown>) => void;
  maxAttempts?: number;
  timeoutMs?: number;
  /** Milliseconds since the epoch, for the signature's timestamp (tests). */
  now?: () => number;
}

export const MAX_ATTEMPTS = 4;
export const ATTEMPT_TIMEOUT_MS = 30_000;
/** Base backoff before attempts 2, 3 and 4. */
export const BACKOFF_MS = [250, 1_000, 3_000];
/** A Retry-After longer than this is not waited for; the worker gives up and the sender retries. */
export const MAX_RETRY_AFTER_MS = 10_000;

const REJECT_REASONS: Record<number, string> = {
  400: "Message could not be parsed",
  404: "Unknown forwarding address",
  413: "Message too large",
};

export class TemporaryFailure extends Error {
  override name = "TemporaryFailure";
}

export type Outcome =
  | { kind: "delivered"; status: number; attempts: number }
  | { kind: "rejected"; status: number; reason: string };

const defaultSleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

function domainOf(addr: string): string {
  const at = addr.lastIndexOf("@");
  return at >= 0 ? addr.slice(at + 1).toLowerCase() : "";
}

/** Parses Retry-After (seconds or an HTTP date) into milliseconds, or null. */
export function parseRetryAfter(value: string | null, now = Date.now()): number | null {
  if (!value) return null;
  const v = value.trim();
  if (/^\d+$/.test(v)) return Number(v) * 1000;
  const at = Date.parse(v);
  if (Number.isNaN(at)) return null;
  return Math.max(0, at - now);
}

/** Backoff before the given attempt (2-based), with up to 25% jitter either way. */
export function backoff(attempt: number, random: () => number): number {
  const base = BACKOFF_MS[Math.min(attempt - 2, BACKOFF_MS.length - 1)] ?? 3_000;
  return Math.round(base * (0.75 + random() * 0.5));
}

function retryable(status: number): boolean {
  return status === 408 || status === 429 || status >= 500;
}

export async function deliver(message: InboundMessage, env: Env, opts: DeliverOptions = {}): Promise<Outcome> {
  const doFetch = opts.fetch ?? fetch;
  const sleep = opts.sleep ?? defaultSleep;
  const random = opts.random ?? Math.random;
  const log = opts.log ?? ((line) => console.log(JSON.stringify(line)));
  const maxAttempts = opts.maxAttempts ?? MAX_ATTEMPTS;
  const timeoutMs = opts.timeoutMs ?? ATTEMPT_TIMEOUT_MS;

  const meta = { rcpt_domain: domainOf(message.to), size: message.rawSize };

  if (!env.INBOUND_SECRET || !env.INBOUND_URL) {
    log({ msg: "inbound worker is not configured", ...meta });
    throw new TemporaryFailure("inbound worker is not configured");
  }

  const body = await new Response(message.raw).arrayBuffer();
  const url = `${env.INBOUND_URL}?to=${encodeURIComponent(message.to)}`;

  let lastError = "";
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    let res: Response;
    try {
      const signature = await signInbound(env.INBOUND_SECRET, Math.floor((opts.now?.() ?? Date.now()) / 1000), message.to, message.from, body);
      res = await doFetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "message/rfc822",
          "OD-Inbound-Signature": signature,
          "OD-Envelope-From": message.from,
        },
        body,
        signal: AbortSignal.timeout(timeoutMs),
      });
    } catch (err) {
      lastError = err instanceof Error ? err.name : "network error";
      log({ msg: "inbound post failed", attempt, error: lastError, ...meta });
      if (attempt < maxAttempts) await sleep(backoff(attempt + 1, random));
      continue;
    }

    // The body is never needed; release the connection.
    await res.body?.cancel().catch(() => {});

    if (res.ok) {
      log({ msg: "inbound delivered", attempt, status: res.status, ...meta });
      return { kind: "delivered", status: res.status, attempts: attempt };
    }

    const reason = REJECT_REASONS[res.status];
    if (reason) {
      log({ msg: "inbound rejected", attempt, status: res.status, ...meta });
      message.setReject(reason);
      return { kind: "rejected", status: res.status, reason };
    }

    lastError = `status ${res.status}`;
    log({ msg: "inbound post failed", attempt, status: res.status, ...meta });
    if (!retryable(res.status)) {
      throw new TemporaryFailure(`inbound API answered ${res.status}`);
    }
    if (attempt < maxAttempts) {
      const after = parseRetryAfter(res.headers.get("Retry-After"));
      if (after !== null && after > MAX_RETRY_AFTER_MS) break;
      await sleep(after !== null ? Math.max(after, backoff(attempt + 1, random)) : backoff(attempt + 1, random));
    }
  }
  throw new TemporaryFailure(`inbound delivery failed: ${lastError}`);
}

export default {
  async email(message: ForwardableEmailMessage, env: Env, _ctx: ExecutionContext): Promise<void> {
    await deliver(message, env);
  },
} satisfies ExportedHandler<Env>;
