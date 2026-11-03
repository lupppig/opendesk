import { afterEach, describe, expect, it, vi } from "vitest";
import { i18n, inSentence, intlLocale, listOf, LOCALES, matchLocale, pickLocale } from "./index";
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
const get = (o: Record<string, unknown>, path: string) => String(path.split(".").reduce<unknown>((x, k) => (x as Record<string, unknown>)?.[k], o));
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
const vars = (t: string) => [...new Set((t.match(/\{\{\s*\w+\s*\}\}/g) ?? []).map((v) => v.replace(/\s/g, "")))].filter((v) => v !== "{{count}}").sort();
const tags = (t: string) => (t.match(/<\/?[A-Za-z0-9]+\s*\/?>/g) ?? []).map((v) => v.replace(/\s/g, "")).sort();

describe("language", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    void i18n.changeLanguage("en");
  });

  it("follows the browser's first translated language", () => {
    expect(pickLocale(["fr-BE", "en-US"])).toBe("fr");
    expect(pickLocale(["de-CH"])).toBe("de");
    expect(pickLocale(["nl-BE", "fr-FR"])).toBe("fr");
    expect(pickLocale(["it-IT"])).toBe("en");
    expect(pickLocale([])).toBe("en");
    expect(matchLocale("DE_at")).toBe("de");
    expect(matchLocale("")).toBeNull();
  });

  it("formats in the browser's region when it speaks the panel's language", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["fr-CA", "en-US"]);
    await i18n.changeLanguage("fr");
    expect(intlLocale()).toBe("fr-CA");
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["de-DE"]);
    expect(intlLocale()).toBe("fr");
  });

  it("translates, and falls back to English for a key a language lacks", async () => {
    await i18n.changeLanguage("fr");
    expect(i18n.t("shell:nav.users")).toBe("Utilisateurs");
    expect(i18n.t("common:table.shown", { count: 2 })).toBe("2 affichés");
    i18n.addResourceBundle("en", "probe", { only: "Only in English" });
    expect(i18n.t("only" as never, { ns: "probe" })).toBe("Only in English");
    await i18n.changeLanguage("de");
    expect(i18n.t("shell:nav.users")).toBe("Benutzer");
  });

  it("puts labels in a sentence and lists them in the language's way", async () => {
    await i18n.changeLanguage("de");
    expect(inSentence("Wissensdatenbank")).toBe("Wissensdatenbank");
    await i18n.changeLanguage("fr");
    expect(inSentence("Tickets")).toBe("tickets");
    expect(listOf(["a", "b", "c"])).toBe("a, b et c");
  });
});

describe("catalogs", () => {
  it("ship every namespace in every language", () => {
    for (const lng of LOCALES) for (const ns of NAMESPACES) expect(() => read(lng, ns), `${lng}/${ns}`).not.toThrow();
  });

  for (const lng of LOCALES.filter((l) => l !== "en")) {
    for (const ns of NAMESPACES) {
      it(`${lng}/${ns}.json has exactly the English keys, with its plural forms`, () => {
        expect(flatten(read(lng, ns)).sort()).toEqual(expected(lng, flatten(read("en", ns))));
      });
      it(`${lng}/${ns}.json keeps every variable and tag, and leaves nothing empty`, () => {
        const en = read("en", ns);
        const cat = read(lng, ns);
        const enKeys = new Set(flatten(en));
        for (const k of flatten(cat)) {
          const source = get(en, enKeys.has(k) ? k : `${k.replace(PLURAL, "")}_other`);
          const text = get(cat, k);
          expect(text.trim(), `${ns}.${k}`).not.toBe("");
          expect(vars(text), `${ns}.${k} variables`).toEqual(vars(source));
          expect(tags(text), `${ns}.${k} tags`).toEqual(tags(source));
        }
      });
    }
  }
});
