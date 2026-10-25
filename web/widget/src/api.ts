import type { Attachment, Conversation, Message, Rating, SessionIdentity, WidgetConfig, WidgetSession } from "./types";

export type Fetcher = (url: string, init: RequestInit) => Promise<Response>;

export class WidgetError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

const enc = encodeURIComponent;

function rows<T>(v: T[] | { data: T[] } | null | undefined): T[] {
  if (!v) return [];
  return Array.isArray(v) ? v : v.data ?? [];
}

export class WidgetApi {
  visitorToken: string | null = null;

  constructor(
    private base: string,
    private key: string,
    private fetcher: Fetcher,
  ) {}

  private async req<T>(method: string, path: string, body?: unknown, auth = true): Promise<T> {
    const headers: Record<string, string> = { Accept: "application/json" };
    let payload: BodyInit | undefined;
    if (body instanceof FormData) payload = body;
    else if (body !== undefined) {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(body);
    }
    if (auth && this.visitorToken) headers.Authorization = `Visitor ${this.visitorToken}`;
    let res: Response;
    try {
      res = await this.fetcher(this.base + path, { method, headers, body: payload, credentials: "omit" });
    } catch {
      throw new WidgetError(0, "network_error", "Can't reach the server");
    }
    if (!res.ok) {
      let code = "error";
      let detail = `Request failed (${res.status})`;
      try {
        const p = (await res.json()) as { code?: string; detail?: string; title?: string };
        code = p.code ?? code;
        detail = p.detail || p.title || detail;
      } catch {
        /* not JSON */
      }
      throw new WidgetError(res.status, code, detail);
    }
    if (res.status === 204 || !(res.headers.get("content-type") ?? "").includes("json")) return undefined as T;
    return (await res.json()) as T;
  }

  config() {
    return this.req<WidgetConfig>("GET", `/v1/widget/config?key=${encodeURIComponent(this.key)}`, undefined, false);
  }

  async session(identity: SessionIdentity = {}) {
    const s = await this.req<WidgetSession>(
      "POST",
      "/v1/widget/session",
      { key: this.key, visitor_token: this.visitorToken ?? undefined, ...identity },
      false,
    );
    this.visitorToken = s.visitor_token;
    return s;
  }

  /** Ends the session on the server: the visitor token stops working everywhere. */
  async endSession() {
    if (!this.visitorToken) return;
    await this.req<void>("DELETE", "/v1/widget/session");
  }

  async conversations() {
    return rows(await this.req<Conversation[] | { data: Conversation[] }>("GET", "/v1/widget/conversations"));
  }

  /** "conversation + first message": accept `{conversation, message}` or a bare conversation. */
  async start(body: string, attachment_ids?: string[], page_url?: string, topic_id?: string) {
    const r = await this.req<{ conversation?: Conversation; message?: Message } & Partial<Conversation>>(
      "POST",
      "/v1/widget/conversations",
      { body, attachment_ids, page_url, topic_id },
    );
    const conversation = (r.conversation ?? r) as Conversation;
    return { conversation, message: r.message ?? null };
  }

  async messages(id: string) {
    return rows(await this.req<Message[] | { data: Message[] }>("GET", `/v1/widget/conversations/${enc(id)}/messages`));
  }

  send(id: string, body: string, attachment_ids?: string[], page_url?: string) {
    return this.req<Message>("POST", `/v1/widget/conversations/${enc(id)}/messages`, { body, attachment_ids, page_url });
  }

  /** The visitor has the conversation on screen: an agent's reply is not also emailed. */
  seen(id: string) {
    return this.req<unknown>("POST", `/v1/widget/conversations/${enc(id)}/seen`, {});
  }

  /** The visitor is writing: agents on the conversation see them typing. */
  typing(id: string) {
    return this.req<unknown>("POST", `/v1/widget/conversations/${enc(id)}/typing`, {});
  }

  /** The visitor wants a person instead of the AI agent. */
  handoff(id: string) {
    return this.req<unknown>("POST", `/v1/widget/conversations/${enc(id)}/handoff`, {});
  }

  /** Confirm or decline an action the agent asked for (contracts/agent.md "Visitor actions"). */
  decide(id: string, actionId: string, decision: "confirm" | "decline") {
    return this.req<unknown>("POST", `/v1/widget/conversations/${enc(id)}/actions/${enc(actionId)}`, { decision });
  }

  /** Rate an agent reply; null clears the rating. */
  rate(id: string, messageId: string, rating: Rating | null) {
    return this.req<unknown>("POST", `/v1/widget/conversations/${enc(id)}/messages/${enc(messageId)}/rating`, { rating });
  }

  upload(file: File) {
    const fd = new FormData();
    fd.append("file", file);
    return this.req<Attachment>("POST", "/v1/widget/attachments", fd);
  }
}
