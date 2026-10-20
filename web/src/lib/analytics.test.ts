import { describe, expect, it } from "vitest";
import { hasSecretParams, sanitize } from "./analytics";

describe("analytics never sees tokens", () => {
  it("knows which URLs carry secrets", () => {
    expect(hasSecretParams("?token=abc")).toBe(true);
    expect(hasSecretParams("?code=x&state=y")).toBe(true);
    expect(hasSecretParams("?ws=123&tab=open")).toBe(false);
  });
  it("drops queries from URL properties, person properties included", () => {
    const out = sanitize({
      $current_url: "https://app.test/reset?token=secret",
      $session_entry_url: "https://app.test/invite?token=secret#x",
      $set_once: { $initial_current_url: "https://app.test/verify?token=secret" },
      plan: "pro",
    });
    expect(JSON.stringify(out)).not.toContain("secret");
    expect(out.plan).toBe("pro");
  });
});
