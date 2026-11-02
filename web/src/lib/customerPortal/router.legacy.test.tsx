import { afterAll, expect, it, vi } from "vitest";
import { router } from "@/router";

vi.hoisted(() => {
  history.replaceState(null, "", "/portal/acme/reset?token=legacy&lang=fr");
});

afterAll(() => {
  router.history.destroy();
  history.replaceState(null, "", "/");
});

it("accepts legacy query tokens before the router subscribes", async () => {
  expect(location.search).toBe("?lang=fr");
  expect(router.history.location.href).toBe("/portal/acme/reset?lang=fr");
  expect(router.state.location.href).toBe("/portal/acme/reset?lang=fr");
  await router.load();
  expect(location.search).toBe("?lang=fr");
  expect(router.state.matches.find((m) => m.routeId === "/portal/$slug/reset")?.context).toMatchObject({ portalResetToken: "legacy" });
});
