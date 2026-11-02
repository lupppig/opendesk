import { afterAll, expect, it, vi } from "vitest";
import { router } from "@/router";

// Each entry URL gets its own module graph and jsdom, just like a fresh page.
const { replace } = vi.hoisted(() => {
  history.replaceState(null, "", "/portal/acme/reset#token=secret");
  return { replace: vi.spyOn(history, "replaceState") };
});

afterAll(() => {
  router.history.destroy();
  vi.restoreAllMocks();
  history.replaceState(null, "", "/");
});

it("retains a reset token across actual router loads without rewriting history again", async () => {
  expect(location.hash).toBe("");
  expect(router.history.location.href).toBe("/portal/acme/reset");
  expect(router.state.location.href).toBe("/portal/acme/reset");
  await router.load();
  expect(location.hash).toBe("");
  expect(router.state.matches.find((m) => m.routeId === "/portal/$slug/reset")?.context).toMatchObject({ portalResetToken: "secret" });
  const count = replace.mock.calls.length;
  await router.load();
  expect(replace.mock.calls.length).toBe(count);
  expect(router.state.matches.find((m) => m.routeId === "/portal/$slug/reset")?.context).toMatchObject({ portalResetToken: "secret" });
});
