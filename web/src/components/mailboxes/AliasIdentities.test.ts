import { describe, expect, it } from "vitest";
import { aliasSettingsPatch, type AliasDraft } from "./AliasIdentities";

const draft = (p: Partial<AliasDraft> = {}): AliasDraft => ({ display_name: "Lumen", signature_html: "<p>Lumen</p>", brand_id: null, send_as: "verified", ...p });

describe("aliasSettingsPatch", () => {
  const base = { "hello@lumen.studio": draft() };

  it("is null when nothing changed", () => {
    expect(aliasSettingsPatch({ "hello@lumen.studio": draft() }, ["hello@lumen.studio"], base)).toBeNull();
  });

  it("sends a signature only where it was edited", () => {
    const named = aliasSettingsPatch({ "hello@lumen.studio": draft({ display_name: "Lumen Studio" }) }, ["hello@lumen.studio"], base);
    expect(named?.["hello@lumen.studio"]).toEqual({ display_name: "Lumen Studio", brand_id: null });
    const signed = aliasSettingsPatch({ "hello@lumen.studio": draft({ signature_html: "" }) }, ["hello@lumen.studio"], base);
    expect(signed?.["hello@lumen.studio"]).toEqual({ display_name: "Lumen", brand_id: null, signature_html: "" });
  });
});
