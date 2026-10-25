// The cache holds several workspaces at once (Together): a key of one workspace is
// never read for another, a query fetches from the workspace its key names, and a
// realtime event lands in its own workspace's keys whichever one has the focus.

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { __resetClientForTests, http, inWorkspace, setAccessToken, setTransport, setWorkspaceId } from "./api/client";
import type { Tag } from "./api/types";
import { keyBody, keyWorkspace, q, qk } from "./queries";
import { WorkspaceQueryClient } from "./queryClient";
import { applyEnvelope } from "./realtime/apply";
import { env, FOCUS } from "./test/fixtures";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";

/** Answers every request with one tag named after the OD-Workspace header it got. */
function serveTags(seen: (string | null)[]) {
  setTransport(async (_url, init) => {
    const ws = new Headers(init.headers).get("OD-Workspace");
    seen.push(ws);
    await Promise.resolve();
    return new Response(JSON.stringify({ data: [{ id: `tag-${ws}`, name: `from ${ws}` }] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  });
}

const tag = (id: string): Tag => ({ id, name: id }) as Tag;

describe("workspace-scoped query keys", () => {
  beforeEach(() => {
    __resetClientForTests();
    setAccessToken("t");
  });
  afterEach(() => setWorkspaceId(FOCUS));

  it("starts every workspace key with its workspace, user keys with none", () => {
    setWorkspaceId(A);
    expect(qk.tags[0]).toBe(`ws:${A}`);
    expect(keyWorkspace(qk.ticket("t1"))).toBe(A);
    expect(keyBody(qk.ticket("t1"))).toEqual(["tickets", "detail", "t1"]);
    expect(keyWorkspace(qk.authConfig)).toBeNull();
    expect(keyWorkspace(qk.workspacePrefs)).toBeNull();
    expect(inWorkspace(B, () => qk.tags[0])).toBe(`ws:${B}`);
    // Out of the scope again: the focus.
    expect(qk.tags[0]).toBe(`ws:${A}`);
  });

  it("never reads a key of workspace A for workspace B", () => {
    const qc = new WorkspaceQueryClient();
    setWorkspaceId(A);
    qc.setQueryData(qk.tags, [tag("a-only")]);
    qc.setQueryData(qk.ticket("t1"), { id: "t1", subject: "A's" });
    setWorkspaceId(B);
    expect(qc.getQueryData(qk.tags)).toBeUndefined();
    expect(qc.getQueryData(qk.ticket("t1"))).toBeUndefined();
    // A prefix of B's matches none of A's.
    expect(qc.getQueriesData({ queryKey: qk.of("tickets") })).toEqual([]);
    expect(qc.getQueriesData({ queryKey: qk.scope() })).toEqual([]);
    setWorkspaceId(A);
    expect(qc.getQueryData<Tag[]>(qk.tags)?.[0].id).toBe("a-only");
  });

  it("fetches a query from the workspace its key names, even after the focus moved", async () => {
    const seen: (string | null)[] = [];
    serveTags(seen);
    const qc = new WorkspaceQueryClient();
    setWorkspaceId(A);
    const optsA = q.tags();
    setWorkspaceId(B);
    // A's key, fetched while B has the focus: the request still goes to A.
    const rows = await qc.fetchQuery(optsA);
    expect(seen).toEqual([A]);
    expect(rows[0].id).toBe(`tag-${A}`);
    expect(qc.getQueryData(q.tags().queryKey)).toBeUndefined();
    await qc.fetchQuery(q.tags());
    expect(seen).toEqual([A, B]);
    // A refetch of A's key goes to A again, never to the focus.
    await qc.refetchQueries({ queryKey: optsA.queryKey });
    expect(seen).toEqual([A, B, A]);
    expect(qc.getQueryData<Tag[]>(optsA.queryKey)?.[0].id).toBe(`tag-${A}`);
  });

  it("sends a mutation to the workspace its component rendered in", async () => {
    const seen: (string | null)[] = [];
    serveTags(seen);
    const qc = new WorkspaceQueryClient();
    setWorkspaceId(A);
    const opts = qc.defaultMutationOptions({
      mutationFn: () => http.get("/v1/tags"),
      onSuccess: () => {
        seen.push(qk.tags[0]);
      },
    });
    setWorkspaceId(B);
    await (opts.mutationFn as () => Promise<unknown>)();
    (opts.onSuccess as () => void)();
    expect(seen).toEqual([A, `ws:${A}`]);
  });

  it("applies a realtime event to its own workspace's keys, not the focus's", () => {
    const qc = new WorkspaceQueryClient();
    setWorkspaceId(A);
    qc.setQueryData(qk.tags, [tag("a1")]);
    const bTags = inWorkspace(B, () => qk.tags);
    qc.setQueryData(bTags, [tag("b1")]);
    applyEnvelope(qc, { ...env(1, { type: "tag.created", data: { tag: tag("b2") } }), workspace_id: B }, { me: "u1" });
    expect(qc.getQueryData<Tag[]>(qk.tags)?.map((x) => x.id)).toEqual(["a1"]);
    expect(qc.getQueryData<Tag[]>(bTags)?.map((x) => x.id)).toEqual(["b1", "b2"]);
  });
});
