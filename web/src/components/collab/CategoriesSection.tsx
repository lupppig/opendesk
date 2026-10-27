// An account's inbox categories in its settings (contracts/collab.md "Categories"): follow the
// team's or workspace's set, choose its own, or leave the inbox whole; whether Ollie sorts its
// new mail; and its sender rules. While the workspace locks categories only Ollie's switch and
// the rules are its manager's.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Ban, Inbox, Lock, Plus, SlidersHorizontal, X } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Row, Section } from "@/components/layout";
import { CategoryEditor, CategoryPill, CategoryPills, likeGmail, useCategoriesSaved, useCategoryProblem } from "@/components/collab/CategoryEditor";
import { ChoiceCard } from "@/components/collab/pickers";
import { Note } from "@/components/settings2/teams";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import type { MailAccount, MailAccountCategories, MailCategory, MailCategoryRule } from "@/lib/api/types";
import { categoryTone, PRIMARY, withKeys } from "@/lib/collab/categories";
import { storeAccount } from "@/lib/collab/hooks";
import { cn } from "@/lib/cn";
import { q, qk } from "@/lib/queries";
import { tone } from "@/lib/tones";

type Form = { mode: MailAccountCategories["mode"]; categories: MailCategory[]; ai: boolean };

/** Where an account's tabs come from, in a few words. */
export function sourceText(t: TFunction<"mailCategories">, c: MailAccountCategories): string {
  if (c.source === "account") return c.enabled ? t("source.account") : t("source.off");
  if (c.source === "team") return t("source.team", { team: c.team?.name ?? "" });
  if (c.source === "workspace") return c.enabled ? t("source.workspace") : t("source.workspaceOff");
  return t("source.none");
}

/** An address, or @ and a domain, as a rule names a sender. */
export const senderOk = (s: string) => /^(@[^\s@]+\.[^\s@]+|[^\s@]+@[^\s@]+\.[^\s@]+)$/.test(s.trim());

export function CategoriesSection({ account }: { account: MailAccount }) {
  const { t } = useTranslation("mailCategories");
  const qc = useQueryClient();
  const saved = useCategoriesSaved();
  const { data: settings } = useQuery(q.mailCategories());
  const c = account.categories;
  const base: Form = useMemo(
    () => ({ mode: c.mode, categories: c.own.length ? c.own : c.enabled ? c.categories : likeGmail(settings?.presets), ai: c.ai }),
    [c, settings?.presets],
  );
  const [form, setForm] = useState(base);
  // A change from elsewhere (another tab, the mail page) replaces the form while it is not being edited.
  const [prevBase, setPrevBase] = useState(base);
  if (prevBase !== base) {
    setPrevBase(base);
    if (sameForm(form, prevBase)) setForm(base);
  }
  const dirty = !sameForm(form, base);
  const problem = useCategoryProblem(form.categories);
  const locked = c.locked;

  const save = useMutation({
    mutationFn: () =>
      api.collab.categories.setAccount(
        account.id,
        // Locked: only Ollie's switch is the manager's, so the mode goes back as it is.
        locked ? { mode: c.mode, ai: form.ai } : { mode: form.mode, categories: form.mode === "custom" ? withKeys(form.categories) : undefined, ai: form.ai },
      ),
    onSuccess: (a) => {
      storeAccount(qc, a);
      saved();
      // The saved set has its keys now: the form takes it, so it is no longer a change.
      setForm({ mode: a.categories.mode, categories: a.categories.own.length ? a.categories.own : form.categories, ai: a.categories.ai });
      const sorted = form.mode !== base.mode || (form.mode === "custom" && !sameList(form.categories, base.categories));
      toast.success(sorted ? t("section.saved") : t("section.savedAi"));
    },
    onError: (e) => toast.error(e, t("section.failed")),
  });

  const ws = settings?.workspace;
  // What following gives: the account's own tabs while it follows, else the workspace's.
  const following = c.mode === "follow";
  const followTitle =
    following && c.source === "team" && c.team ? t("section.followTeam", { team: c.team.name }) : (following ? c.source === "workspace" : !!ws) ? t("section.followWorkspace") : t("section.followNone");
  const followed = following ? (c.enabled ? c.categories : null) : ws?.enabled ? ws.categories : null;
  const followHint = followed ? (
    <CategoryPills categories={followed} className="mt-1" />
  ) : (following ? c.source === "none" : !ws) ? (
    t("section.followHintNone")
  ) : (
    t("section.followHintOff")
  );
  const set = (p: Partial<Form>) => setForm((f) => ({ ...f, ...p }));

  return (
    <>
      <Section title={t("section.title")} description={t("section.description")}>
        {locked && (
          <div className="pt-3.5">
            <Note icon={<Lock className="mt-0.5 size-3 shrink-0 text-fg-4" />}>{t("section.locked")}</Note>
          </div>
        )}
        <div role="radiogroup" aria-label={t("section.title")} className="flex flex-col gap-2 py-3.5">
          <ChoiceCard checked={form.mode === "follow"} disabled={locked} onSelect={() => set({ mode: "follow" })} icon={<Inbox />} title={followTitle} hint={followHint} />
          <ChoiceCard checked={form.mode === "custom"} disabled={locked} onSelect={() => set({ mode: "custom" })} icon={<SlidersHorizontal />} title={t("section.custom")} hint={t("section.customHint")}>
            <CategoryEditor value={form.categories} onChange={(categories) => set({ categories })} accountId={account.id} disabled={locked} />
          </ChoiceCard>
          <ChoiceCard checked={form.mode === "off"} disabled={locked} onSelect={() => set({ mode: "off" })} icon={<Ban />} title={t("section.off")} hint={t("section.offHint")} />
        </div>
        <Row label={t("section.ai")} description={settings?.sorting === "rules" ? `${t("section.aiHint")} ${t("section.aiUnavailable")}` : t("section.aiHint")} htmlFor={`cat-ai-${account.id}`}>
          <Switch id={`cat-ai-${account.id}`} checked={form.ai} onCheckedChange={(ai) => set({ ai })} aria-label={t("section.ai")} />
        </Row>
        <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line-soft py-3">
          {dirty && form.mode === "custom" && problem && (
            <p role="alert" className="mr-auto min-w-0 text-[12px] text-danger-fg">
              {problem}
            </p>
          )}
          {dirty && (
            <Button size="sm" variant="ghost" onClick={() => setForm(base)}>
              {t("common.discard")}
            </Button>
          )}
          <Button size="sm" variant="primary" disabled={!dirty || (form.mode === "custom" && !!problem)} loading={save.isPending} onClick={() => save.mutate()}>
            {t("common.save")}
          </Button>
        </div>
      </Section>
      {c.enabled && <RulesSection account={account} />}
    </>
  );
}

function sameList(a: MailCategory[], b: MailCategory[]) {
  return JSON.stringify(a) === JSON.stringify(b);
}
function sameForm(a: Form, b: Form) {
  return a.mode === b.mode && a.ai === b.ai && (a.mode !== "custom" || sameList(a.categories, b.categories));
}

/** Sender rules: mail from an address, or a whole domain, always goes to one tab. */
function RulesSection({ account }: { account: MailAccount }) {
  const { t } = useTranslation("mailCategories");
  const qc = useQueryClient();
  const saved = useCategoriesSaved();
  const key = qk.mailCategoryRules(account.id);
  const { data: rules, isLoading } = useQuery(q.mailCategoryRules(account.id));
  const categories = account.categories.categories;
  const [sender, setSender] = useState("");
  const [category, setCategory] = useState<string | undefined>(categories.find((x) => x.key !== PRIMARY)?.key);
  const [touched, setTouched] = useState(false);
  const valid = senderOk(sender);
  const pick = categories.some((x) => x.key === category) ? category : undefined;

  const add = useMutation({
    mutationFn: () => api.collab.categories.addRule(account.id, { sender: sender.trim().toLowerCase(), category: pick! }),
    onSuccess: (rule) => {
      // One rule per sender: a second replaces the first.
      qc.setQueryData<MailCategoryRule[]>(key, (prev) => [rule, ...(prev ?? []).filter((r) => r.sender !== rule.sender && r.id !== rule.id)]);
      saved();
      setSender("");
      setTouched(false);
      toast.success(t("rules.added", { sender: rule.sender, category: categories.find((x) => x.key === rule.category)?.name ?? rule.category }));
    },
    onError: (e) => toast.error(e, t("rules.failed")),
  });
  const remove = useMutation({
    mutationFn: (r: MailCategoryRule) => api.collab.categories.removeRule(account.id, r.id),
    onMutate: (r) => {
      const prev = qc.getQueryData<MailCategoryRule[]>(key);
      qc.setQueryData<MailCategoryRule[]>(key, (p) => p?.filter((x) => x.id !== r.id));
      return { prev };
    },
    onError: (e, _r, ctx) => {
      if (ctx?.prev) qc.setQueryData(key, ctx.prev);
      toast.error(e, t("rules.failed"));
    },
  });

  const sorted = [...(rules ?? [])].sort((a, b) => a.sender.replace(/^@/, "").localeCompare(b.sender.replace(/^@/, "")));
  const submit = () => {
    setTouched(true);
    if (valid && pick) add.mutate();
  };

  return (
    <Section title={t("rules.title")} description={t("rules.hint")} flush>
      {isLoading ? (
        <div className="h-12" />
      ) : sorted.length ? (
        <ul>
          {sorted.map((r) => {
            const cat = categories.find((x) => x.key === r.category) ?? { key: r.category, name: r.category, color: "gray" as const };
            return (
              <li key={r.id} className="group flex min-h-10 items-center gap-2.5 border-b border-line-soft px-4 py-1.5 last:border-0">
                <span className="mono min-w-0 truncate text-[12px] text-fg">{r.sender}</span>
                <ArrowRight className="size-3 shrink-0 text-fg-4" aria-hidden />
                <CategoryPill category={cat} />
                <Tooltip content={t("rules.remove", { sender: r.sender })}>
                  <Button
                    size="xs"
                    variant="ghost"
                    square
                    className="ml-auto text-fg-4 opacity-0 transition-opacity hover:text-fg focus-visible:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100"
                    aria-label={t("rules.remove", { sender: r.sender })}
                    icon={<X />}
                    onClick={() => remove.mutate(r)}
                  />
                </Tooltip>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="px-4 py-3 text-[12px] text-fg-3">{t("rules.empty")}</p>
      )}
      <form
        className="flex flex-wrap items-start gap-2 border-t border-line-soft bg-sunken/50 px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <div className="min-w-[200px] flex-1">
          <TextInput
            inputSize="sm"
            value={sender}
            onChange={(e) => setSender(e.target.value)}
            onBlur={() => sender && setTouched(true)}
            invalid={touched && !!sender && !valid}
            placeholder={t("rules.senderPlaceholder")}
            aria-label={t("rules.sender")}
            className="mono"
          />
          {touched && !!sender && !valid && <p className="mt-1 text-[11.5px] text-danger-fg">{t("rules.invalid")}</p>}
        </div>
        <Select size="sm" className="w-[160px]" value={pick} onChange={setCategory} aria-label={t("rules.category")} placeholder={t("rules.category")} options={categories.map((x) => ({ value: x.key, label: x.name, icon: <CategoryDot category={x} /> }))} />
        <Button type="submit" size="sm" icon={<Plus />} disabled={!sender.trim() || !pick} loading={add.isPending}>
          {t("rules.add")}
        </Button>
      </form>
    </Section>
  );
}

function CategoryDot({ category }: { category: Pick<MailCategory, "color"> }) {
  return <span aria-hidden className={cn("size-2 shrink-0 rounded-full", tone[categoryTone(category)].solid)} />;
}
