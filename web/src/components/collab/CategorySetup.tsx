// The guided set-up the mail page offers whoever manages an account whose inbox is not split
// (contracts/collab.md "Categories"): pick a start (Gmail's tabs, Ollie's proposal from this
// inbox, or nearly nothing), adjust it in the editor, and split the inbox. An admin may set it
// up for everyone instead.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Inbox, PenLine, Sparkles } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { CategoryEditor, CategoryPills, likeGmail, useCategoriesSaved, useCategoryProblem } from "@/components/collab/CategoryEditor";
import { ChoiceCard } from "@/components/collab/pickers";
import { Note } from "@/components/settings2/teams";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { MailAccount, MailCategory } from "@/lib/api/types";
import { PRIMARY, withKeys } from "@/lib/collab/categories";
import { storeAccount } from "@/lib/collab/hooks";
import { dur, ease } from "@/lib/motion";
import { q } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";

type Start = "gmail" | "ollie" | "scratch";

export function CategorySetupDialog({ account, open, onOpenChange }: { account: MailAccount; open: boolean; onOpenChange: (o: boolean) => void }) {
  // Each opening starts at the first step; closing keeps what is shown while it fades.
  const [session, setSession] = useState(0);
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setSession((n) => n + 1);
  }
  return <Setup key={session} account={account} open={open} onOpenChange={onOpenChange} />;
}

function Setup({ account, open, onOpenChange }: { account: MailAccount; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("mailCategories");
  const qc = useQueryClient();
  const saved = useCategoriesSaved();
  const reduce = useReducedMotion();
  const admin = atLeast(useRole(), "admin");
  const { data: settings } = useQuery(q.mailCategories());
  const [step, setStep] = useState<1 | 2>(1);
  const [start, setStart] = useState<Start>("gmail");
  const [about, setAbout] = useState("");
  const [list, setList] = useState<MailCategory[]>([]);
  const problem = useCategoryProblem(list);
  const gmail = likeGmail(settings?.presets);
  const primary = settings?.presets.find((p) => p.key === PRIMARY);
  const close = () => onOpenChange(false);

  const suggest = useMutation({
    mutationFn: () => api.collab.categories.suggest({ account_id: account.id, about: about.trim() || undefined }),
    onSuccess: (r) => {
      setList(r.categories);
      setStep(2);
    },
    onError: (e) => toast.error(e, t("editor.suggest.failed")),
  });
  const split = useMutation({
    mutationFn: () => api.collab.categories.setAccount(account.id, { mode: "custom", categories: withKeys(list), ai: account.categories.ai }),
    onSuccess: (a) => {
      storeAccount(qc, a);
      saved();
      toast.success(t("setup.saved"));
      close();
    },
    onError: (e) => toast.error(e, t("section.failed")),
  });

  const next = () => {
    if (start === "ollie") return suggest.mutate();
    if (start === "gmail") setList(gmail.map((c) => ({ ...c })));
    else if (primary) setList([{ ...primary }, { key: "", name: "", description: "", color: "green" }]);
    setStep(2);
  };

  const slide = (dir: number) =>
    reduce
      ? { initial: false as const, animate: { opacity: 1 }, exit: { opacity: 0, transition: { duration: 0 } } }
      : { initial: { opacity: 0, x: 12 * dir }, animate: { opacity: 1, x: 0, transition: { duration: dur.base, ease: ease.out } }, exit: { opacity: 0, x: -12 * dir, transition: { duration: dur.fast } } };

  const footer = (
    <>
      {admin && (
        <Button asChild variant="link" size="sm" className="mr-auto text-fg-3 hover:text-fg">
          <Link to="/settings/mail-categories" onClick={close}>
            {t("setup.everyone")}
          </Link>
        </Button>
      )}
      {step === 1 ? (
        <>
          <Button variant="ghost" onClick={close}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" disabled={!settings || account.categories.locked} loading={suggest.isPending} onClick={next}>
            {suggest.isPending ? t("setup.thinking") : t("setup.next")}
          </Button>
        </>
      ) : (
        <>
          <Button variant="ghost" onClick={() => setStep(1)}>
            {t("setup.back")}
          </Button>
          <Button variant="primary" disabled={!!problem} loading={split.isPending} onClick={() => split.mutate()}>
            {t("setup.save")}
          </Button>
        </>
      )}
    </>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange} size="lg" title={t("setup.title")} description={t("setup.description", { account: account.name || account.email })} footer={footer}>
      {account.categories.locked ? (
        <Note>{t("setup.locked")}</Note>
      ) : (
        <div className="flex flex-col gap-4 pb-2">
          <ol className="flex items-center gap-2 text-[11.5px] font-medium text-fg-4" aria-label={t("setup.steps")}>
            {[t("setup.step1"), t("setup.step2")].map((label, i) => (
              <li key={label} className="flex items-center gap-2" aria-current={step === i + 1 ? "step" : undefined}>
                {i > 0 && <span aria-hidden className="h-px w-5 bg-line" />}
                <span className={step === i + 1 ? "text-fg" : undefined}>
                  {i + 1}. {label}
                </span>
              </li>
            ))}
          </ol>

          <AnimatePresence mode="wait" initial={false}>
            {step === 1 ? (
              <motion.div key="start" {...slide(-1)} role="radiogroup" aria-label={t("setup.step1")} className="flex flex-col gap-2">
                <ChoiceCard checked={start === "gmail"} onSelect={() => setStart("gmail")} icon={<Inbox />} title={t("setup.gmail")} hint={<CategoryPills categories={gmail} className="mt-1" />} />
                {settings?.suggest && (
                  <ChoiceCard checked={start === "ollie"} onSelect={() => setStart("ollie")} icon={<Sparkles />} title={t("setup.ollie")} hint={t("setup.ollieHint")}>
                    <label htmlFor="setup-about" className="mb-1.5 block text-[12px] font-medium text-fg-2">
                      {t("setup.ollieAbout")}
                    </label>
                    <Textarea id="setup-about" rows={2} maxLength={500} value={about} onChange={(e) => setAbout(e.target.value)} placeholder={t("editor.suggest.aboutPlaceholder")} className="text-[12.5px]" />
                  </ChoiceCard>
                )}
                <ChoiceCard checked={start === "scratch"} onSelect={() => setStart("scratch")} icon={<PenLine />} title={t("setup.scratch")} hint={t("setup.scratchHint")} />
              </motion.div>
            ) : (
              <motion.div key="edit" {...slide(1)} className="flex flex-col gap-2">
                <p className="text-[12px] leading-[18px] text-fg-3">{t("setup.step2Hint")}</p>
                <CategoryEditor value={list} onChange={setList} accountId={account.id} />
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      )}
    </Dialog>
  );
}
