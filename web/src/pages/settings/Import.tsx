// Settings, Import (contracts/imports.md): bring a team's history over from Zendesk, Help
// Scout, Freshdesk, Intercom, Front or a CSV file. One import runs at a time; its card follows
// it live, and earlier imports stay listed with what they brought.

import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ChevronDown, EyeOff, History, Undo2, UsersRound } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Section, SettingsPage } from "@/components/layout";
import { ListSkeleton, StatePill } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { ImportCard } from "@/components/imports/Progress";
import { ImportWizard } from "@/components/imports/Wizard";
import { DOCS, SOURCES, SourceMark, isActive, sourceName, statusState } from "@/components/imports/sources";
import type { Import, ImportSource } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { number, shortDate } from "@/lib/format";
import { dur, ease } from "@/lib/motion";
import { q } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";

function SourceCard({ source, disabled, onPick }: { source: ImportSource; disabled: boolean; onPick: () => void }) {
  const { t } = useTranslation("imports");
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onPick}
      className="group flex flex-col gap-3 rounded-lg border border-line bg-surface p-3.5 text-left shadow-xs outline-none transition-[border-color,box-shadow,transform] duration-150 hover:border-line-strong focus-visible:shadow-[0_0_0_3px_var(--focus)] active:scale-[0.99] disabled:pointer-events-none disabled:opacity-50 motion-reduce:active:scale-100"
    >
      <div className="flex items-center gap-2.5">
        <SourceMark source={source} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-[13px] font-medium text-fg">{sourceName[source]}</div>
          <div className="text-[11.5px] text-fg-4">{t(`sources.${source}.with`)}</div>
        </div>
      </div>
      <p className="line-clamp-2 min-h-[36px] text-[12px] leading-[18px] text-fg-3">{t(`sources.${source}.blurb`)}</p>
    </button>
  );
}

/** A past import: one line that opens into its card. */
function PastRow({ x, open, onToggle, admin }: { x: Import; open: boolean; onToggle: () => void; admin: boolean }) {
  const { t } = useTranslation("imports");
  const reduce = useReducedMotion();
  const brought = x.counts.tickets + x.counts.articles;
  return (
    <MotionRow>
      <div id={`import-${x.id}`}>
      <button type="button" onClick={onToggle} aria-expanded={open} className="flex w-full items-center gap-3 px-4 py-2.5 text-left outline-none transition-colors hover:bg-hover/60">
        <SourceMark source={x.source} size="sm" />
        <div className="min-w-0 flex-1">
          <span className="truncate text-[12.5px] font-medium text-fg">{x.account.name || sourceName[x.source]}</span>
          <span className="ml-2 text-[11.5px] text-fg-4">{shortDate(x.created_at)}</span>
        </div>
        <span className="hidden shrink-0 text-[11.5px] tabular-nums text-fg-3 sm:inline">
          {x.status === "undone" ? t("history.undone") : t("history.brought", { tickets: number(x.counts.tickets), articles: number(x.counts.articles), count: brought })}
        </span>
        <StatePill state={statusState[x.status]}>{t(`state.${x.status}`)}</StatePill>
        <ChevronDown className={cn("size-3.5 shrink-0 text-fg-4 transition-transform duration-150", open && "rotate-180")} aria-hidden />
      </button>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            initial={reduce ? false : { height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            transition={{ duration: dur.base, ease: ease.out }}
            className="overflow-hidden border-t border-line-soft bg-canvas"
          >
            <div className="p-3">
              <ImportCard x={x} admin={admin} />
            </div>
          </motion.div>
        )}
      </AnimatePresence>
      </div>
    </MotionRow>
  );
}

export default function ImportPage() {
  const { t } = useTranslation("imports");
  const admin = atLeast(useRole(), "admin");
  const navigate = useNavigate();
  const search = useSearch({ from: "/app/settings/import" });
  const { data: list = [], isLoading } = useQuery(q.imports());
  const [wizard, setWizard] = useState<ImportSource | null>(null);
  const [open, setOpen] = useState<string | null>(search.id ?? null);
  const focused = useRef(false);

  const active = list.find((x) => isActive(x.status));
  // The newest import stays on top until another starts: its outcome is what people come back for.
  const current = active ?? (list[0] && Date.now() - Date.parse(list[0].updated_at) < 3 * 86_400_000 && list[0].status !== "undone" ? list[0] : undefined);
  const past = list.filter((x) => x.id !== current?.id);

  // A link to one import (a notification) opens it.
  useEffect(() => {
    if (!search.id || focused.current || !list.length) return;
    focused.current = true;
    if (search.id !== current?.id) setOpen(search.id);
    requestAnimationFrame(() => document.getElementById(`import-${search.id}`)?.scrollIntoView({ block: "center", behavior: "smooth" }));
  }, [search.id, list.length, current?.id]);

  if (wizard)
    return (
      <ImportWizard
        source={wizard}
        onClose={() => setWizard(null)}
        onStarted={(x) => {
          setWizard(null);
          void navigate({ to: "/settings/import", search: { id: x.id }, replace: true });
          window.scrollTo({ top: 0 });
        }}
      />
    );

  return (
    <SettingsPage title={t("title")} description={t("description")}>
      {current && (
        <section id={`import-${current.id}`} className="animate-rise-in">
          <div className="mb-3">
            <h2 className="text-[13px] font-semibold tracking-[-0.01em]">{isActive(current.status) ? t("current.running") : t("current.latest")}</h2>
            {isActive(current.status) && <p className="mt-0.5 text-[12px] leading-[18px] text-fg-3">{t("current.runningHint")}</p>}
          </div>
          <ImportCard x={current} admin={admin} />
        </section>
      )}

      <section className="animate-rise-in">
        <div className="mb-3">
          <h2 className="text-[13px] font-semibold tracking-[-0.01em]">{current ? t("pick.again") : t("pick.title")}</h2>
          <p className="mt-0.5 text-[12px] leading-[18px] text-fg-3">
            {!admin ? t("pick.adminsOnly") : active ? t("pick.oneAtATime") : t("pick.hint")}{" "}
            <a href={DOCS} target="_blank" rel="noopener noreferrer" className="text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
              {t("pick.guide")}
            </a>
          </p>
        </div>
        <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-3">
          {SOURCES.map((s) => (
            <SourceCard key={s} source={s} disabled={!admin || !!active} onPick={() => setWizard(s)} />
          ))}
        </div>
        {!current && !isLoading && (
          <ul className="mt-5 grid gap-3 text-[12px] leading-[18px] text-fg-3 sm:grid-cols-3">
            {(
              [
                ["quiet", <EyeOff key="i" />],
                ["people", <UsersRound key="i" />],
                ["undo", <Undo2 key="i" />],
              ] as const
            ).map(([k, icon]) => (
              <li key={k} className="flex gap-2.5">
                <span className="mt-0.5 flex text-fg-4 [&_svg]:size-3.5">{icon}</span>
                <span>
                  <span className="font-medium text-fg-2">{t(`promise.${k}.title`)}</span> {t(`promise.${k}.body`)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      {(isLoading || past.length > 0) && (
        <Section flush title={t("history.title")} description={t("history.hint")}>
          {isLoading ? (
            <ListSkeleton rows={2} />
          ) : (
            <MotionList>
              {past.map((x) => (
                <PastRow key={x.id} x={x} admin={admin} open={open === x.id} onToggle={() => setOpen(open === x.id ? null : x.id)} />
              ))}
            </MotionList>
          )}
        </Section>
      )}
      {!isLoading && !list.length && (
        <p className="-mt-4 flex items-center gap-1.5 text-[11.5px] text-fg-4">
          <History className="size-3" aria-hidden />
          {t("history.none")}
        </p>
      )}
    </SettingsPage>
  );
}
