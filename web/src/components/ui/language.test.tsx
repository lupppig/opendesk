import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeAll, expect, it } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import { Calendar, DateField } from "./date-picker";
import { SearchInput } from "./input";

// The agent app loads its French catalogs before rendering (the root route's loader).
beforeAll(() => loadAgentCatalogs("fr"));

afterEach(async () => { await i18n.changeLanguage("en"); });

it("uses French calendar names and accessible controls without changing ISO date values", async () => {
  await i18n.changeLanguage("fr");
  const calendar = renderToStaticMarkup(<Calendar value={new Date(2026, 2, 4)} onChange={() => {}} />);
  expect(calendar).toContain("mars 2026");
  expect(calendar).toContain('aria-label="Mois suivant"');
  const field = renderToStaticMarkup(<DateField value="2026-03-04" onChange={() => {}} />);
  expect(field).toContain("4 mars 2026");
  expect(renderToStaticMarkup(<SearchInput value="query" onChange={() => {}} />)).toContain('aria-label="Effacer la recherche"');
});

it("writes German dates with German month names and day order", async () => {
  await i18n.changeLanguage("de");
  expect(renderToStaticMarkup(<Calendar value={new Date(2026, 2, 4)} onChange={() => {}} />)).toContain("März 2026");
  expect(renderToStaticMarkup(<DateField value="2026-03-04" onChange={() => {}} />)).toContain("4. März 2026");
});

it("keeps the English calendar and search labels", async () => {
  await i18n.changeLanguage("en");
  const calendar = renderToStaticMarkup(<Calendar value={new Date(2026, 2, 4)} onChange={() => {}} />);
  expect(calendar).toContain("March 2026");
  expect(calendar).toContain('aria-label="Next month"');
  expect(renderToStaticMarkup(<SearchInput value="query" onChange={() => {}} />)).toContain('aria-label="Clear search"');
});
