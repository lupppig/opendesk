import { describe, expect, it } from "vitest";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";
import type { Mailbox } from "@/lib/api/types";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, {
    method,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    headers: ws ? { "OD-Workspace": ws } : {},
  });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}
const mailboxes = async (): Promise<Mailbox[]> => (await call("GET", "/v1/mailboxes")).body.data;

describe("mock alias identities", () => {
  installMock();

  it("seeds Lumen as an alias of Support with its own identity and Gmail's list", async () => {
    const support = (await mailboxes()).find((m) => m.email === "support@margin.app")!;
    const lumen = support.alias_settings?.["hello@lumen.studio"];
    expect(lumen).toMatchObject({ display_name: "Lumen", send_as: "verified", send_as_by: "provider" });
    expect(lumen?.brand_id).toBeTruthy();
    expect(support.alias_settings?.["hello@margin.app"]?.send_as).toBe("unverified");
    expect(support.provider_send_as?.readable).toBe(true);
    const tickets = (await call("GET", "/v1/tickets?received_at=hello@lumen.studio")).body.data as { brand_id: string }[];
    expect(tickets.length).toBeGreaterThan(0);
    expect(tickets.every((t) => t.brand_id === lumen?.brand_id)).toBe(true);
  });

  it("saves names, signatures and brands, and lets Gmail's list decide send_as", async () => {
    const support = (await mailboxes()).find((m) => m.email === "support@margin.app")!;
    const url = `/v1/mailboxes/${support.id}`;
    const next = await call("PATCH", url, {
      aliases: [...support.aliases, "sales@margin.app"],
      alias_settings: { "sales@margin.app": { display_name: "Margin Sales", signature_html: "<p>Sales</p>" } },
    });
    expect(next.status).toBe(200);
    expect(next.body.alias_settings["sales@margin.app"]).toMatchObject({ display_name: "Margin Sales", signature_html: "<p>Sales</p>", send_as: "verified" });
    // A signature left out is kept.
    const kept = await call("PATCH", url, { alias_settings: { "sales@margin.app": { display_name: "Margin Sales" } } });
    expect(kept.body.alias_settings["sales@margin.app"].signature_html).toBe("<p>Sales</p>");
    // Left out of the map, an alias keeps no name of its own.
    expect(next.body.alias_settings["hello@lumen.studio"].display_name).toBe("");
    expect((await call("PATCH", url, { alias_settings: { "hello@margin.app": { send_as: "verified" } } })).status).toBe(422);
    expect((await call("PATCH", url, { alias_settings: { "nobody@margin.app": {} } })).status).toBe(422);
    expect((await call("POST", `${url}/send-as/check`)).status).toBe(200);
  });

  it("takes an admin's word on a send-only Google connection", async () => {
    const security = (await mailboxes()).find((m) => m.email === "security@margin.app")!;
    const url = `/v1/mailboxes/${security.id}`;
    expect((await call("POST", `${url}/send-as/check`)).status).toBe(422);
    const r = await call("PATCH", url, { aliases: ["report@margin.app"], alias_settings: { "report@margin.app": { send_as: "verified" } } });
    expect(r.body.provider_send_as.readable).toBe(false);
    expect(r.body.alias_settings["report@margin.app"]).toMatchObject({ send_as: "verified", send_as_by: "admin" });
  });
});
