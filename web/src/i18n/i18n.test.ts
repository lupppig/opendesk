import { afterEach, describe, expect, it, vi } from "vitest";
import { AGENT_LOCALES, i18n, inSentence, intlLocale, isPublicPath, loadAgentCatalogs, matchLocale, pickAgentLocale, pickLocale, resetLocale, RTL, setLocale, SUPPORTED } from "./index";
import { NAMESPACES } from "./resources";

const files = import.meta.glob<Record<string, unknown>>("./locales/*/*.json", { eager: true, import: "default" });
const PLURAL = /_(zero|one|two|few|many|other)$/;

function flatten(o: Record<string, unknown>, prefix = ""): string[] {
  return Object.entries(o).flatMap(([k, v]) => (v && typeof v === "object" ? flatten(v as Record<string, unknown>, `${prefix}${k}.`) : [`${prefix}${k}`]));
}
function read(lng: string, ns: string): Record<string, unknown> {
  const f = files[`./locales/${lng}/${ns}.json`];
  if (!f) throw new Error(`missing ${lng}/${ns}.json`);
  return f;
}
/** The keys a language must have: English's, with each plural expanded to that language's categories. */
function expected(lng: string, enKeys: string[]): string[] {
  const cats = new Intl.PluralRules(lng).resolvedOptions().pluralCategories;
  const out = new Set<string>();
  for (const k of enKeys) {
    if (PLURAL.test(k)) cats.forEach((c) => out.add(k.replace(PLURAL, `_${c}`)));
    else out.add(k);
  }
  return [...out].sort();
}

describe("matchLocale", () => {
  it("maps regional tags onto supported languages", () => {
    expect(matchLocale("pt-BR")).toBe("pt");
    expect(matchLocale("es-419")).toBe("es");
    expect(matchLocale("de_AT")).toBe("de");
    expect(matchLocale("zh")).toBe("zh-CN");
    expect(matchLocale("zh-Hans-CN")).toBe("zh-CN");
    expect(matchLocale("ZH-cn")).toBe("zh-CN");
  });
  it("refuses what it can't serve", () => {
    expect(matchLocale("zh-TW")).toBeNull();
    expect(matchLocale("zh-Hant")).toBeNull();
    expect(matchLocale("sv")).toBeNull();
    expect(matchLocale("")).toBeNull();
    expect(matchLocale(undefined)).toBeNull();
  });
});

describe("pickLocale", () => {
  it("prefers ?lang, then the browser, then the workspace default, then English", () => {
    expect(pickLocale({ explicit: "ja", browser: ["de-DE"], workspaceDefault: "fr" })).toBe("ja");
    expect(pickLocale({ explicit: "xx", browser: ["sv-SE", "de-DE"], workspaceDefault: "fr" })).toBe("de");
    expect(pickLocale({ browser: ["zh-TW", "sv"], workspaceDefault: "fr" })).toBe("fr");
    expect(pickLocale({ browser: ["sv"], workspaceDefault: null })).toBe("en");
    expect(pickLocale({ browser: [] })).toBe("en");
  });
  it("marks Arabic as right to left", () => {
    expect(RTL.has(pickLocale({ browser: ["ar-EG"] }))).toBe(true);
    expect(RTL.has("en")).toBe(false);
  });
});

describe("loading", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    resetLocale();
  });

  it("boots with common only and keeps the agent catalogs off public pages", async () => {
    expect(i18n.options.ns).toEqual(["common"]);
    await setLocale("fr", ["public", "common"]);
    expect(i18n.hasResourceBundle("fr", "public")).toBe(true);
    for (const ns of NAMESPACES.filter((ns) => ns !== "common" && ns !== "public")) expect(i18n.hasResourceBundle("fr", ns), ns).toBe(false);
  });

  it("tells public routes from the agent app's", () => {
    expect(isPublicPath("/csat")).toBe(true);
    expect(isPublicPath("/csat/abc")).toBe(true);
    expect(isPublicPath("/help/acme/articles/x")).toBe(true);
    expect(isPublicPath("/inbox/unassigned")).toBe(false);
    expect(isPublicPath("/helpers")).toBe(false);
    expect(isPublicPath("/login")).toBe(false);
  });

  it("loads every agent catalog in the agent language, and nothing for English", async () => {
    await loadAgentCatalogs("en");
    expect(i18n.hasResourceBundle("fr", "inbox")).toBe(false);
    for (const lng of AGENT_LOCALES.filter((l) => l !== "en")) {
      await loadAgentCatalogs(lng);
      for (const ns of NAMESPACES) expect(i18n.hasResourceBundle(lng, ns), `${lng}/${ns}`).toBe(true);
    }
  });

  it("falls back to English for a key French does not have yet", async () => {
    i18n.addResourceBundle("en", "fallbackProbe", { old: "Old", added: "Only in English" });
    i18n.addResourceBundle("fr", "fallbackProbe", { old: "Ancien" });
    await i18n.changeLanguage("fr");
    expect(i18n.t("old" as never, { ns: "fallbackProbe" })).toBe("Ancien");
    expect(i18n.t("added" as never, { ns: "fallbackProbe" })).toBe("Only in English");
  });
});

describe("intlLocale", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    resetLocale();
  });

  it("keeps the browser's region when it speaks the active language", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["fr-CA", "en-US"]);
    await i18n.changeLanguage("fr");
    expect(intlLocale()).toBe("fr-CA");
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["de-DE", "fr-CH"]);
    expect(intlLocale()).toBe("fr-CH");
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["de-DE"]);
    expect(intlLocale()).toBe("fr");
  });

  it("uses the page language on public pages, with a matching region only", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["zh-TW", "pt-BR"]);
    await setLocale("zh-CN", ["common"]);
    expect(intlLocale()).toBe("zh-CN");
    await setLocale("pt", ["common"]);
    expect(intlLocale()).toBe("pt-BR");
  });

  it("formats an English agent app in the browser's locale", async () => {
    vi.spyOn(navigator, "language", "get").mockReturnValue("de-DE");
    await i18n.changeLanguage("en");
    expect(intlLocale()).toBe("de-DE");
  });
});

describe("agent language", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    resetLocale();
  });

  it("selects French and German regional variants and falls back to English for other languages", () => {
    expect(pickAgentLocale(["fr-FR", "en-US"])).toBe("fr");
    expect(pickAgentLocale(["fr-CA"])).toBe("fr");
    expect(pickAgentLocale(["de-DE"])).toBe("de");
    expect(pickAgentLocale(["de-CH", "fr-CH"])).toBe("de");
    expect(pickAgentLocale(["en-US", "de-DE"])).toBe("en");
    expect(pickAgentLocale(["it-IT"])).toBe("en");
    expect(pickAgentLocale(["ar-EG"])).toBe("en");
    expect(pickAgentLocale([])).toBe("en");
  });

  it("loads French agent catalogs and restores them after a public page", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["fr-FR"]);
    await loadAgentCatalogs();
    await i18n.changeLanguage(pickAgentLocale());
    expect(i18n.t("nav.inbox", { ns: "shell" })).toBe("Boîte de réception");
    await setLocale("ar", ["public", "common"]);
    expect(document.documentElement.dir).toBe("rtl");
    resetLocale();
    expect(i18n.language).toBe("fr");
    expect(document.documentElement.lang).toBe("fr");
    expect(document.documentElement.dir).toBe("ltr");
    expect(i18n.t("nav.inbox", { ns: "shell" })).toBe("Boîte de réception");
  });

  it("does not apply a public language that finishes loading after leaving the page", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["fr-FR"]);
    await i18n.changeLanguage("fr");
    let finish!: () => void;
    vi.spyOn(i18n, "loadLanguages").mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
    const pending = setLocale("ar", ["public", "common"]);
    resetLocale();
    finish();
    await pending;
    expect(i18n.language).toBe("fr");
    expect(document.documentElement.lang).toBe("fr");
    expect(document.documentElement.dir).toBe("ltr");
  });
});

describe("inSentence", () => {
  afterEach(() => resetLocale());

  it("lowercases a label inside a sentence, except in German, whose nouns keep their capital", async () => {
    await i18n.changeLanguage("fr");
    expect(inSentence("Contacts")).toBe("contacts");
    await i18n.changeLanguage("de");
    expect(inSentence("Kontakte")).toBe("Kontakte");
    await i18n.changeLanguage("en");
    expect(inSentence("Tickets")).toBe("tickets");
  });
});

describe("catalogs", () => {
  it("ships a folder for every supported language", () => {
    const langs = new Set(Object.keys(files).map((k) => k.split("/")[2]));
    expect([...langs].sort()).toEqual([...SUPPORTED].sort());
  });

  // Customer-facing namespaces are fully translated, with each language's plural forms.
  for (const ns of ["public", "common"]) {
    const en = flatten(read("en", ns));
    for (const lng of SUPPORTED.filter((l) => l !== "en")) {
      it(`${lng}/${ns}.json has exactly the English keys`, () => {
        const keys = flatten(read(lng, ns)).sort();
        expect(keys).toEqual(expected(lng, en));
      });
      it(`${lng}/${ns}.json keeps every interpolation`, () => {
        const enCat = read("en", ns);
        const cat = read(lng, ns);
        const get = (o: Record<string, unknown>, path: string) => path.split(".").reduce<unknown>((x, k) => (x as Record<string, unknown>)?.[k], o);
        for (const k of en) {
          const base = k.replace(PLURAL, "");
          const vars = new Set((String(get(enCat, k)).match(/\{\{\s*\w+\s*\}\}/g) ?? []).map((v) => v.replace(/\s/g, "")));
          vars.delete("{{count}}"); // a plural form may spell the number out ("مقالة واحدة")
          for (const tk of flatten(cat).filter((x) => x.replace(PLURAL, "") === base)) {
            const text = String(get(cat, tk));
            for (const v of vars) expect(text, `${lng} ${tk}`).toContain(v);
          }
        }
      });
    }
  }

  // The agent app's translations are partial by design: an English string added
  // without a translation falls back to English (fallbackLng). What they have must be sound.
  for (const lng of AGENT_LOCALES.filter((l) => l !== "en")) {
    for (const ns of NAMESPACES.filter((ns) => ns !== "common" && ns !== "public")) {
      it(`${lng}/${ns}.json has only English keys, valid plurals, and keeps variables, tags and Ollie`, () => {
        const en = read("en", ns);
        const cat = read(lng, ns);
        const enKeys = new Set(flatten(en));
        const enPlurals = new Set([...enKeys].filter((k) => PLURAL.test(k)).map((k) => k.replace(PLURAL, "")));
        const cats = new Intl.PluralRules(lng).resolvedOptions().pluralCategories;
        const get = (o: Record<string, unknown>, path: string): string => String(path.split(".").reduce<unknown>((x, k) => (x as Record<string, unknown>)?.[k], o));
        const vars = (t: string) => (t.match(/\{\{[^}]+\}\}/g) ?? []).map((v) => v.replace(/\s/g, "")).sort();
        const tags = (t: string) => (t.match(/<\/?[A-Za-z0-9]+\s*\/?>/g) ?? []).map((v) => v.replace(/\s/g, "")).sort();
        const catKeys = flatten(cat);
        for (const k of catKeys) {
          const base = k.replace(PLURAL, "");
          const plural = PLURAL.test(k) && enPlurals.has(base);
          if (plural) {
            expect(cats, `${ns}.${k} is not a ${lng} plural form`).toContain(k.match(PLURAL)![1]);
            for (const c of cats) expect(catKeys, `${ns}.${k} lacks its ${c} form`).toContain(`${base}_${c}`);
          } else {
            expect(enKeys.has(k), `${ns}.${k} is not an English key`).toBe(true);
          }
          const source = get(en, enKeys.has(k) ? k : `${base}_other`);
          const translated = get(cat, k);
          if (source.trim()) expect(translated.trim(), `${ns}.${k}`).not.toBe("");
          expect(vars(translated), `${ns}.${k} variables`).toEqual(vars(source));
          expect(tags(translated), `${ns}.${k} tags`).toEqual(tags(source));
          expect(translated.match(/Ollie/g) ?? [], `${ns}.${k}`).toEqual(source.match(/Ollie/g) ?? []);
        }
      });
    }
  }

  it("writes French with the typographic apostrophe (l’appel), never the typewriter one", () => {
    const strings = (o: unknown): string[] => (typeof o === "string" ? [o] : o && typeof o === "object" ? Object.values(o).flatMap(strings) : []);
    const straight = Object.entries(files)
      .filter(([f]) => f.startsWith("./locales/fr/"))
      .flatMap(([f, cat]) => strings(cat).filter((s) => s.includes("'")).map((s) => `${f}: ${s}`));
    expect(straight).toEqual([]);
  });
});
