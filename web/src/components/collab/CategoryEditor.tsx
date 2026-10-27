// The editor for a set of inbox categories (contracts/collab.md "Categories"): Primary first and
// fixed (it may be renamed), the others reordered by their grip, each with a colour, a name and a
// description Ollie sorts by. Presets not in the list yet are a click away, and Ollie can propose a
// whole set, which can be undone. Controlled: the page or dialog around it saves.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Pin, Plus, Sparkles, X } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ColorSwatchPicker } from "@/components/settings2/ColorSwatchPicker";
import { SortableList } from "@/components/settings2/Sortable";
import { OllieAvatar } from "@/components/tickets/ollie";
import { Button } from "@/components/ui/button";
import { Textarea, TextInput } from "@/components/ui/input";
import { Popover } from "@/components/ui/popover";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import type { MailCategory, MailCategorySettings, MailColor } from "@/lib/api/types";
import { categoryProblem, categoryTone, MAX_CATEGORIES, MIN_CATEGORIES, PRIMARY } from "@/lib/collab/categories";
import { LABEL_TONES } from "@/lib/collab/labelTones";
import { cn } from "@/lib/cn";
import { dur, springs } from "@/lib/motion";
import { q, qk } from "@/lib/queries";
import { AI_TONE, tone, type Tone } from "@/lib/tones";

const NAME_MAX = 40;
const DESCRIPTION_MAX = 300;
/** Colours a new category takes, in turn, so two new ones never look alike. */
const NEW_TONES: MailColor[] = ["blue", "green", "amber", "pink", "teal", "indigo", "orange", "red"];

/** The set Gmail's tabs make: Primary, Updates, Newsletters and Promotions, from the presets. */
export const GMAIL_KEYS = [PRIMARY, "updates", "newsletters", "promotions"];
export function likeGmail(presets: MailCategory[] | undefined): MailCategory[] {
  return GMAIL_KEYS.map((k) => presets?.find((p) => p.key === k)).filter((c): c is MailCategory => !!c);
}

/**
 * After categories change anywhere: the settings as the server returned them, then the
 * accounts' tabs, the counts and the lists, since mail is sorted again.
 */
export function useCategoriesSaved() {
  const qc = useQueryClient();
  return (settings?: MailCategorySettings) => {
    if (settings) qc.setQueryData(qk.mailCategories, settings);
    for (const queryKey of [qk.mailAccounts, qk.mailCounts, qk.mailThreadLists]) void qc.invalidateQueries({ queryKey });
  };
}

/** What is wrong with a set, as a sentence, or null. */
export function useCategoryProblem(list: MailCategory[]): string | null {
  const { t } = useTranslation("mailCategories");
  const p = categoryProblem(list);
  return p ? t(`editor.problem.${p}`, { min: MIN_CATEGORIES, max: MAX_CATEGORIES }) : null;
}

/** A category as a soft pill: its dot and its name. */
export function CategoryPill({ category, className }: { category: Pick<MailCategory, "name" | "color">; className?: string }) {
  const hue = categoryTone(category);
  return (
    <span className={cn("inline-flex h-5 max-w-[160px] shrink-0 items-center gap-1.5 rounded-sm px-1.5 text-[11px] font-medium", tone[hue].chip, className)}>
      <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", tone[hue].solid)} />
      <span className="truncate">{category.name}</span>
    </span>
  );
}

/** A set as a row of pills, Primary first. */
export function CategoryPills({ categories, className }: { categories: Pick<MailCategory, "key" | "name" | "color">[]; className?: string }) {
  return (
    <span className={cn("flex flex-wrap items-center gap-1", className)}>
      {categories.map((c) => (
        <CategoryPill key={c.key || c.name} category={c} />
      ))}
    </span>
  );
}

export function CategoryEditor({
  value,
  onChange,
  accountId,
  disabled,
  autoFocusNew = true,
}: {
  value: MailCategory[];
  onChange: (next: MailCategory[]) => void;
  /** Ollie's suggestions also read this account's latest inbox mail. */
  accountId?: string;
  disabled?: boolean;
  /** Focus the name of a category when it is added. */
  autoFocusNew?: boolean;
}) {
  const { t } = useTranslation("mailCategories");
  const { data: settings } = useQuery(q.mailCategories());
  const reduce = useReducedMotion();
  // Rows keep an id across edits (keys are empty until saved), so they animate and keep focus.
  const ids = useRef(new WeakMap<MailCategory, string>());
  const seq = useRef(0);
  const idOf = (c: MailCategory) => {
    let id = ids.current.get(c);
    if (!id) {
      id = `row-${++seq.current}`;
      ids.current.set(c, id);
    }
    return id;
  };
  const [touched, setTouched] = useState<Set<string>>(() => new Set());
  const [focusId, setFocusId] = useState<string | null>(null);
  // Ollie's proposal replaced the list: the list before it, until something else changes.
  const [undo, setUndo] = useState<MailCategory[] | null>(null);

  const change = (next: MailCategory[]) => {
    setUndo(null);
    onChange(next);
  };
  const update = (i: number, p: Partial<MailCategory>) => {
    const prev = value[i];
    const next = { ...prev, ...p };
    ids.current.set(next, idOf(prev));
    change(value.map((c, j) => (j === i ? next : c)));
  };
  const add = (c: MailCategory) => {
    if (value.length >= MAX_CATEGORIES) return;
    const id = idOf(c);
    if (autoFocusNew) setFocusId(id);
    change([...value, c]);
  };
  const addBlank = () => {
    const used = new Set(value.map((c) => c.color));
    add({ key: "", name: "", description: "", color: NEW_TONES.find((x) => !used.has(x)) ?? "gray" });
  };
  const remove = (i: number) => change(value.filter((_, j) => j !== i));

  const primary = value[0];
  const rest = value.slice(1).map((c) => ({ id: idOf(c), c }));
  const reorder = (order: string[]) => {
    const byId = new Map(rest.map((r) => [r.id, r.c]));
    change([primary, ...order.map((id) => byId.get(id)!).filter(Boolean)]);
  };

  // A name repeated or left empty marks its row once it was left.
  const names = new Map<string, number>();
  for (const c of value) {
    const n = c.name.trim().toLowerCase();
    if (n) names.set(n, (names.get(n) ?? 0) + 1);
  }
  const nameInvalid = (c: MailCategory) => {
    const n = c.name.trim().toLowerCase();
    return (!n && touched.has(idOf(c))) || (names.get(n) ?? 0) > 1;
  };
  const problem = useCategoryProblem(value);
  // An empty name is only a problem once its row was left.
  const showProblem = !!problem && (categoryProblem(value) !== "name" || value.some((c) => !c.name.trim() && touched.has(idOf(c))));

  const keys = new Set(value.map((c) => c.key).filter(Boolean));
  const presets = (settings?.presets ?? []).filter((p) => p.key !== PRIMARY && !keys.has(p.key) && !names.has(p.name.toLowerCase()));
  const full = value.length >= MAX_CATEGORIES;

  const row = (c: MailCategory, i: number, handle: React.ReactNode) => {
    const isPrimary = i === 0;
    const id = idOf(c);
    return (
      <div className="group relative flex items-start gap-2 py-2 pl-5 pr-2">
        {isPrimary ? (
          <Tooltip content={t("editor.primaryHint")}>
            <span className="absolute left-1 top-[15px] flex size-3.5 items-center justify-center text-fg-4" aria-label={t("editor.primaryHint")} role="img">
              <Pin className="size-3" />
            </span>
          </Tooltip>
        ) : (
          handle
        )}
        <ColorButton value={categoryTone(c)} disabled={disabled} name={c.name || t("editor.untitled")} onChange={(color) => update(i, { color })} />
        <div className="grid min-w-0 flex-1 gap-1.5 sm:grid-cols-[minmax(0,190px)_minmax(0,1fr)]">
          <TextInput
            inputSize="sm"
            value={c.name}
            maxLength={NAME_MAX}
            disabled={disabled}
            autoFocus={focusId === id}
            invalid={nameInvalid(c)}
            placeholder={isPrimary ? t("editor.primaryPlaceholder") : t("editor.namePlaceholder")}
            aria-label={t("editor.name")}
            onChange={(e) => update(i, { name: e.target.value })}
            onBlur={() => {
              if (focusId === id) setFocusId(null);
              setTouched((s) => (s.has(id) ? s : new Set(s).add(id)));
            }}
          />
          <TextInput
            inputSize="sm"
            value={c.description}
            maxLength={DESCRIPTION_MAX}
            disabled={disabled}
            placeholder={isPrimary ? t("editor.primaryDescription") : t("editor.descriptionPlaceholder")}
            aria-label={t("editor.description", { name: c.name || t("editor.untitled") })}
            onChange={(e) => update(i, { description: e.target.value })}
            className="text-fg-2"
          />
        </div>
        {isPrimary ? (
          <span className="size-7 shrink-0" aria-hidden />
        ) : (
          <Tooltip content={t("editor.remove", { name: c.name || t("editor.untitled") })}>
            <Button
              variant="ghost"
              size="sm"
              square
              disabled={disabled}
              aria-label={t("editor.remove", { name: c.name || t("editor.untitled") })}
              onClick={() => remove(i)}
              icon={<X />}
              className="text-fg-4 hover:text-fg"
            />
          </Tooltip>
        )}
      </div>
    );
  };

  return (
    <div className="flex flex-col gap-2.5">
      <AnimatePresence initial={false}>
        {undo && (
          <motion.div
            key="undo"
            initial={reduce ? false : { opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: "auto" }}
            exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, height: 0, transition: { duration: dur.fast } }}
            transition={reduce ? { duration: 0 } : springs.row}
            className="overflow-hidden"
          >
            <div role="status" className={cn("flex items-center gap-2 rounded-md px-3 py-2 text-[12px]", tone[AI_TONE].soft, tone[AI_TONE].text)}>
              <OllieAvatar size={16} />
              <span className="min-w-0 flex-1">{t("editor.suggest.applied", { count: value.length })}</span>
              <Button
                size="xs"
                variant="ghost"
                className={tone[AI_TONE].text}
                onClick={() => {
                  onChange(undo);
                  setUndo(null);
                }}
              >
                {t("editor.suggest.undo")}
              </Button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      <div className="overflow-hidden rounded-md border border-line bg-surface">
        {primary && <div className="border-b border-line-soft">{row(primary, 0, null)}</div>}
        <SortableList
          items={rest}
          disabled={disabled}
          onReorder={reorder}
          handleLabel={(r) => t("editor.reorder", { name: r.c.name || t("editor.untitled") })}
          renderRow={(r, handle, index) => row(r.c, index + 1, handle)}
        />
      </div>

      {showProblem && (
        <p role="alert" className="text-[12px] text-danger-fg">
          {problem}
        </p>
      )}

      {!disabled && (
        <div className="flex flex-wrap items-center gap-1.5">
          <Button size="sm" icon={<Plus />} disabled={full} onClick={addBlank}>
            {t("editor.add")}
          </Button>
          {!full &&
            presets.map((p) => (
              <button
                key={p.key}
                type="button"
                onClick={() => add({ ...p })}
                title={p.description}
                className="inline-flex h-7 items-center gap-1.5 rounded-md border border-dashed border-line-strong px-2 text-[12px] font-medium text-fg-2 transition-colors hover:border-fg-4 hover:bg-hover hover:text-fg"
              >
                <span aria-hidden className={cn("size-1.5 rounded-full", tone[categoryTone(p)].solid)} />
                {p.name}
              </button>
            ))}
          {settings?.suggest && (
            <SuggestButton
              accountId={accountId}
              onSuggest={(list) => {
                const before = value;
                onChange(list);
                setUndo(before);
              }}
            />
          )}
          <span className="ml-auto text-[11.5px] tabular-nums text-fg-4">{t("editor.count", { count: value.length, max: MAX_CATEGORIES })}</span>
        </div>
      )}
    </div>
  );
}

/** A category's colour as a dot that opens the tones. */
function ColorButton({ value, onChange, name, disabled }: { value: Tone; onChange: (c: MailColor) => void; name: string; disabled?: boolean }) {
  const { t } = useTranslation("mailCategories");
  const [open, setOpen] = useState(false);
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      className="p-2.5"
      trigger={
        <button
          type="button"
          disabled={disabled}
          aria-label={t("editor.color", { name })}
          className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-md outline-none transition-colors hover:bg-hover focus-visible:shadow-[0_0_0_3px_var(--focus)] disabled:pointer-events-none"
        >
          <span className={cn("size-2.5 rounded-full", tone[value].solid)} />
        </button>
      }
    >
      {/* A click picks and closes; arrow keys move through the tones and leave it open. */}
      <div onClick={(e) => (e.target as HTMLElement).closest("[role=radio]") && setOpen(false)}>
        <ColorSwatchPicker size="sm" tones={[...LABEL_TONES]} value={value} onChange={(c) => onChange(c as MailColor)} label={t("editor.color", { name })} className="max-w-[150px]" />
      </div>
    </Popover>
  );
}

/** "Suggest with Ollie": what the inbox is for, then a whole set from Ollie. */
function SuggestButton({ accountId, onSuggest }: { accountId?: string; onSuggest: (list: MailCategory[]) => void }) {
  const { t } = useTranslation("mailCategories");
  const [open, setOpen] = useState(false);
  const [about, setAbout] = useState("");
  const run = useMutation({
    mutationFn: () => api.collab.categories.suggest({ account_id: accountId, about: about.trim() || undefined }),
    onSuccess: (r) => {
      if (r.categories.length < MIN_CATEGORIES) {
        toast.error(t("editor.suggest.empty"));
        return;
      }
      onSuggest(r.categories);
      setOpen(false);
    },
    onError: (e) => toast.error(e, t("editor.suggest.failed")),
  });
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      align="end"
      className="w-[300px] p-3"
      trigger={
        <Button size="sm" variant="ghost" icon={<Sparkles />} className={cn(tone[AI_TONE].text, "hover:text-tone-violet")}>
          {t("editor.suggest.button")}
        </Button>
      }
    >
      <form
        className="flex flex-col gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          run.mutate();
        }}
      >
        <label htmlFor="cat-about" className="flex items-center gap-1.5 text-[12.5px] font-medium">
          <OllieAvatar size={16} />
          {t("editor.suggest.about")}
        </label>
        <Textarea
          id="cat-about"
          rows={3}
          maxLength={500}
          value={about}
          autoFocus
          placeholder={t("editor.suggest.aboutPlaceholder")}
          onChange={(e) => setAbout(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              run.mutate();
            }
          }}
          className="text-[12.5px]"
        />
        <p className="text-[11.5px] leading-4 text-fg-4">{accountId ? t("editor.suggest.accountHint") : t("editor.suggest.hint")}</p>
        <div className="flex justify-end">
          <Button type="submit" size="sm" variant="primary" loading={run.isPending}>
            {t("editor.suggest.run")}
          </Button>
        </div>
      </form>
    </Popover>
  );
}
