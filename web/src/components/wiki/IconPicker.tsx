// The emoji a wiki page wears beside its title. A popover with search: typing narrows
// the grid, Enter takes the first match, Escape or a click away closes it.

import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Popover } from "@/components/ui/popover";
import { SearchInput } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { EMOJI, searchEmoji } from "./emoji";

export function IconPicker({ value, onChange, trigger, open, onOpenChange }: { value: string; onChange: (icon: string) => void; trigger: ReactNode; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("wiki");
  const [q, setQ] = useState("");
  const found = q.trim() ? searchEmoji(q) : null;
  const pick = (e: string) => {
    onChange(e);
    onOpenChange(false);
    setQ("");
  };
  const cell = (e: string) => (
    <button
      key={e}
      type="button"
      onClick={() => pick(e)}
      aria-label={e}
      aria-pressed={e === value}
      className={cn("flex size-8 items-center justify-center rounded-md text-[18px] leading-none transition-colors hover:bg-hover focus-visible:bg-hover focus-visible:outline-none", e === value && "bg-active")}
    >
      {e}
    </button>
  );
  return (
    <Popover open={open} onOpenChange={(o) => (onOpenChange(o), o || setQ(""))} trigger={trigger} className="w-[300px] p-0">
      <div className="flex items-center gap-1.5 border-b border-line p-2">
        <SearchInput
          autoFocus
          value={q}
          onChange={setQ}
          placeholder={t("icon.search")}
          className="flex-1"
          onKeyDown={(e) => {
            if (e.key === "Enter" && found?.[0]) {
              e.preventDefault();
              pick(found[0]);
            }
          }}
        />
        {value && (
          <Button variant="ghost" size="sm" onClick={() => pick("")}>
            {t("icon.remove")}
          </Button>
        )}
      </div>
      <div className="max-h-[280px] overflow-y-auto p-2" role="listbox" aria-label={t("icon.label")}>
        {found ? (
          found.length ? (
            <div className="grid grid-cols-8 gap-0.5">{found.map(cell)}</div>
          ) : (
            <p className="px-2 py-6 text-center text-[12.5px] text-fg-3">{t("icon.none")}</p>
          )
        ) : (
          EMOJI.map((g) => (
            <div key={g.id} className="mb-2 last:mb-0">
              <div className="label-caps px-1 pb-1 pt-0.5">{t(`icon.groups.${g.id}`)}</div>
              <div className="grid grid-cols-8 gap-0.5">{g.emoji.map(([e]) => cell(e))}</div>
            </div>
          ))
        )}
      </div>
    </Popover>
  );
}
