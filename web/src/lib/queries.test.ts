import { QueryClient } from "@tanstack/react-query";
import { afterEach, expect, it } from "vitest";
import { __resetClientForTests, configureClient, setTransport } from "./api/client";
import { q } from "./queries";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

afterEach(() => __resetClientForTests());

it("loads every members page", async () => {
  configureClient({ baseUrl: "https://api.test" });
  setTransport(async (raw) => {
    const url = new URL(raw);
    if (url.searchParams.get("cursor") === "second") {
      return json({ data: [{ user: { id: "second" } }], next_cursor: null });
    }
    return json({ data: [{ user: { id: "first" } }], next_cursor: "second" });
  });

  const client = new QueryClient();
  const members = await client.fetchQuery(q.members());

  expect(members.map((member) => member.user.id)).toEqual(["first", "second"]);
});
