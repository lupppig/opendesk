import { format } from "date-fns";
import { expect, it } from "vitest";
import { dateLocale, datePattern } from "./dates";

const day = new Date(2026, 9, 6);
const write = (lng: string, p: Parameters<typeof datePattern>[0]) => format(day, datePattern(p, lng), { locale: dateLocale(lng) });

it("writes short dates in each agent language's order and names", () => {
  expect(write("en", "dayMonth")).toBe("Oct 6");
  expect(write("en", "weekdayDayMonth")).toBe("Tue, Oct 6");
  expect(write("fr", "dayMonthYear")).toBe("6 oct. 2026");
  expect(write("de", "dayMonth")).toBe("6. Okt.");
  expect(write("de", "weekdayDayMonth")).toBe("Di., 6. Okt.");
  expect(write("de-CH", "dayMonthYear")).toBe("6. Okt. 2026");
});

it("uses English for a language the agent app is not translated into", () => {
  expect(dateLocale("it")).toBeUndefined();
  expect(write("it", "dayMonthYear")).toBe("Oct 6, 2026");
});
