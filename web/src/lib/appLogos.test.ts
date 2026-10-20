import { describe, expect, it } from "vitest";
import { appForUrl, appLogo } from "./appLogos";

describe("app logos", () => {
  it("ships a logo for every catalog provider and import source", () => {
    const keys = ["linear", "github", "atlassian", "sentry", "notion", "asana", "monday", "slack", "intercom", "close", "stripe", "paypal", "square", "box", "zapier"];
    for (const k of [...keys, "zendesk", "helpscout", "freshdesk", "discord", "telegram"]) expect(appLogo(k), k).toBe(`/logos/apps/${k}.svg`);
    for (const k of ["custom", "link", "csv", "", null, undefined]) expect(appLogo(k)).toBeNull();
  });

  it("knows a product by its host or a subdomain of it", () => {
    expect(appForUrl("https://github.com/acme/app/issues/12")).toBe("github");
    expect(appForUrl("https://acme.atlassian.net/browse/OPS-4")).toBe("atlassian");
    expect(appForUrl("https://app.asana.com/0/1/2")).toBe("asana");
    expect(appForUrl("https://notgithub.com/x")).toBeNull();
    expect(appForUrl("https://github.com.evil.io/x")).toBeNull();
    expect(appForUrl("not a url")).toBeNull();
  });
});
