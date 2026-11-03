import { describe, expect, it } from "vitest";
import { __setDictForTests, en, matchLocale, pickLocale, SUPPORTED, t } from "./i18n";
import { catalogs } from "./locales/catalogs";

describe("widget locale", () => {
  it("matches BCP 47 tags onto supported locales", () => {
    expect(matchLocale("fr-CA")).toBe("fr");
    expect(matchLocale("pt_BR")).toBe("pt");
    expect(matchLocale("zh")).toBe("zh-CN");
    expect(matchLocale("zh-Hans-CN")).toBe("zh-CN");
    expect(matchLocale("zh-TW")).toBeNull();
    expect(matchLocale("ar-EG")).toBe("ar");
    expect(matchLocale("sv")).toBeNull();
    expect(matchLocale("")).toBeNull();
  });

  it("picks explicit, then browser, then workspace, then English", () => {
    expect(pickLocale({ explicit: "de", browser: ["ja"], workspace: "fr" })).toBe("de");
    expect(pickLocale({ explicit: null, browser: ["sv-SE", "ja-JP"], workspace: "fr" })).toBe("ja");
    expect(pickLocale({ browser: ["sv-SE"], workspace: "fr" })).toBe("fr");
    expect(pickLocale({ browser: ["sv-SE"], workspace: "xx" })).toBe("en");
    expect(pickLocale({ explicit: "klingon", browser: [] })).toBe("en");
  });

  it("has every key in every language, and nothing extra", () => {
    const keys = Object.keys(en).sort();
    expect(Object.keys(catalogs).sort()).toEqual([...SUPPORTED].sort());
    for (const [lng, dict] of Object.entries(catalogs)) {
      expect(Object.keys(dict).sort(), lng).toEqual(keys);
      for (const k of keys) {
        const v = (dict as Record<string, string>)[k];
        expect(v.trim(), `${lng}.${k}`).not.toBe("");
        // Placeholders survive translation.
        const want = (en[k as keyof typeof en].match(/\{\w+\}/g) ?? []).sort();
        expect((v.match(/\{\w+\}/g) ?? []).sort(), `${lng}.${k} placeholders`).toEqual(want);
      }
    }
  });

  it("writes French with the typographic apostrophe, never the typewriter one", () => {
    expect(Object.values(catalogs.fr).filter((v) => v.includes("'"))).toEqual([]);
  });

  it("interpolates and falls back to English", () => {
    __setDictForTests("fr", catalogs.fr);
    expect(t("typing", { name: "Priya" })).toBe("Priya est en train d’écrire…");
    __setDictForTests("de", { ...catalogs.de, retry: "" });
    expect(t("retry")).toBe("Retry");
    __setDictForTests("en", en);
  });
});
