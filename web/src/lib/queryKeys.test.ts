// Every query key that names workspace data carries its workspace (lib/queries.ts): a
// literal key array in the app may only start with a user-level root. Anything else
// goes through qk, which puts the workspace first.

import { describe, expect, it } from "vitest";
import { USER_ROOTS } from "./queries";

const sources = import.meta.glob(["../**/*.{ts,tsx}", "!../**/*.test.{ts,tsx}", "!../mock/**"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

describe("query keys", () => {
  it("reads the app's sources", () => {
    expect(Object.keys(sources).length).toBeGreaterThan(100);
  });

  it("never builds a workspace key from a bare literal", () => {
    const bad: string[] = [];
    for (const [file, text] of Object.entries(sources)) {
      for (const m of text.matchAll(/(?:queryKey:\s*|QueryData(?:<[^>]*>)?\(\s*|Queries(?:<[^>]*>)?\(\s*)\[\s*"([^"]+)"/g)) {
        if (!USER_ROOTS.has(m[1])) bad.push(`${file}: ${m[0]}`);
      }
      // A key kept in a module constant would freeze the workspace it was built for.
      for (const m of text.matchAll(/^(?:export )?const (\w+) = (?:qk\.|\[\s*"([^"]+)")/gm)) {
        const keyName = m[1] === "key" || m[1].endsWith("Key");
        if (m[0].includes("qk.") || (keyName && m[2] && !USER_ROOTS.has(m[2]))) bad.push(`${file}: ${m[0]}`);
      }
    }
    expect(bad).toEqual([]);
  });
});
