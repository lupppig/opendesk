import { useMutation, useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { Check } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { Lockup } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/ui/empty";
import { Notice } from "@/components/auth/parts";
import { api } from "@/lib/api/endpoints";
import { isApiError } from "@/lib/api/client";
import { LanguageSwitcher, usePublicLocale } from "@/components/help/locale";
import "@/components/help/help.css";
import { cn } from "@/lib/cn";
import { q } from "@/lib/queries";
import { tone, type Tone } from "@/lib/tones";

/**
 * Email links land here as /csat#t=<token>&score=1..5: the token rides in the
 * fragment, which browsers never send to a server, so no log or analytics sees it.
 * Links sent before carried it in the path (/csat/<token>?score=); that page is
 * moved to the fragment form at once.
 */
function landing(pathToken: string | undefined): { token: string; score?: string } {
  const hash = new URLSearchParams(location.hash.replace(/^#/, ""));
  if (hash.get("t")) return { token: hash.get("t") ?? "", score: hash.get("score") ?? undefined };
  let score: string | undefined;
  try {
    const nav = performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming | undefined;
    score = new URL(nav?.name ?? location.href).searchParams.get("score") ?? undefined;
  } catch {
    score = undefined;
  }
  return { token: pathToken ?? "", score };
}

// One hue per score, from the sentiment family: frustrated red to positive green.
const HUES: Tone[] = ["red", "orange", "gray", "lime", "green"];
const hueOf = (score: number) => HUES[Math.min(Math.max(score, 1), 5) - 1];

/** A line face; the mouth curves from frown (1) to smile (5). */
function Face({ score, className, draw }: { score: number; className?: string; draw?: boolean }) {
  const curve = (score - 3) * 3.2; // -6.4 .. 6.4
  const d = `M13 ${26 - curve / 2} Q20 ${26 + curve} 27 ${26 - curve / 2}`;
  return (
    <svg viewBox="0 0 40 40" className={cn("size-9", className)} aria-hidden fill="none" stroke="currentColor" strokeLinecap="round">
      <circle cx="20" cy="20" r="16.5" strokeWidth="1.6" />
      {score === 1 ? (
        <path d="M12.6 14.6l3.4 1.6M27.4 14.6l-3.4 1.6" strokeWidth="1.8" />
      ) : (
        <>
          <circle cx="14.6" cy="16.4" r="1.75" fill="currentColor" stroke="none" />
          <circle cx="25.4" cy="16.4" r="1.75" fill="currentColor" stroke="none" />
        </>
      )}
      {draw ? (
        <motion.path d={d} strokeWidth="1.9" initial={{ pathLength: 0 }} animate={{ pathLength: 1 }} transition={{ duration: 0.45, delay: 0.15, ease: [0.16, 1, 0.3, 1] }} />
      ) : (
        <path d={d} strokeWidth="1.9" />
      )}
    </svg>
  );
}

export default function Csat() {
  const params = useParams({ strict: false }) as { token?: string };
  const [{ token, score: landingScore }] = useState(() => landing(params.token));
  useEffect(() => {
    // An old path link: keep the token out of the address from here on.
    if (params.token) history.replaceState(history.state, "", `/csat#t=${encodeURIComponent(token)}`);
  }, [params.token, token]);
  const { data, isLoading, isError, isPending } = useQuery(q.csat(token));
  const { locale, loaded } = usePublicLocale(data?.locale || undefined, !isPending);
  const { t } = useTranslation("public");
  const label = (n: number) => t(`csat.scores.${n as 1 | 2 | 3 | 4 | 5}`);
  const pre = Number(landingScore);
  const [score, setScore] = useState<number | null>(pre >= 1 && pre <= 5 ? pre : null);
  const [comment, setComment] = useState("");
  const [sentScore, setSentScore] = useState<number | null>(null);
  const [finished, setFinished] = useState(false);
  const auto = useRef(false);

  const submit = useMutation({
    mutationFn: (b: { score: number; comment?: string }) => api.public.submitCsat(token, b),
    onSuccess: (_d, b) => setSentScore(b.score),
  });

  // A score clicked in the email is recorded right away; the comment is optional after.
  useEffect(() => {
    if (auto.current || !data || data.score !== null || !(pre >= 1 && pre <= 5)) return;
    auto.current = true;
    submit.mutate({ score: pre });
  }, [data, pre, submit]);

  useEffect(() => {
    if (loaded) document.title = t("csat.page_title");
  }, [loaded, t]);

  const rated = data?.score != null && sentScore === null;
  const reduce = useReducedMotion();

  // A radio group moves with the arrow keys (and keeps one tab stop).
  const onRadioKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const rtl = getComputedStyle(e.currentTarget).direction === "rtl";
    const step = { ArrowRight: rtl ? -1 : 1, ArrowDown: 1, ArrowLeft: rtl ? 1 : -1, ArrowUp: -1 }[e.key];
    if (!step) return;
    e.preventDefault();
    const next = Math.min(5, Math.max(1, (score ?? (step > 0 ? 0 : 6)) + step));
    setScore(next);
    e.currentTarget.querySelectorAll<HTMLButtonElement>("[role=radio]")[next - 1]?.focus();
  };

  return (
    <div data-public-page className="flex min-h-dvh flex-col items-center bg-surface px-4">
      <main className="flex w-full max-w-[460px] flex-1 flex-col justify-center py-16">
        {isLoading || !loaded ? (
          <div className="flex flex-col items-center gap-3">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-7 w-72" />
            <Skeleton className="mt-6 h-16 w-full rounded-lg" />
          </div>
        ) : isError || !data ? (
          <EmptyState lit={2} title={t("csat.invalid_title")} description={t("csat.invalid_body")} />
        ) : rated ? (
          <Done score={data.score ?? 5} title={t("csat.already_title")} body={t("csat.already_body")} />
        ) : finished ? (
          <Done score={sentScore ?? score ?? 5} title={t("csat.thanks_title")} body={data.workspace_name ? t("csat.thanks_body", { name: data.workspace_name }) : t("csat.thanks_body_generic")} />
        ) : (
          <div className="animate-rise-in text-center motion-reduce:animate-none">
            {data.workspace_name && (
              <p className="label-caps mb-3">
                <bdi>{data.workspace_name}</bdi>
              </p>
            )}
            <h1 className="text-[24px] font-semibold leading-tight tracking-[-0.03em] text-fg">{t("csat.question")}</h1>
            {data.ticket?.subject && (
              <p className="mx-auto mt-2 max-w-[380px] truncate text-[13px] text-fg-3">
                {data.ticket.number ? (
                  <>
                    <bdi className="mono">#{data.ticket.number}</bdi>{" "}
                  </>
                ) : null}
                <bdi>{data.ticket.subject}</bdi>
              </p>
            )}
            <div role="radiogroup" aria-label={t("csat.rating")} className="mt-8 grid grid-cols-5 gap-2" onKeyDown={onRadioKey}>
              {[1, 2, 3, 4, 5].map((n) => {
                const on = score === n;
                const h = tone[hueOf(n)];
                return (
                  <button
                    key={n}
                    type="button"
                    role="radio"
                    aria-checked={on}
                    tabIndex={on || (score === null && n === 1) ? 0 : -1}
                    aria-label={t("csat.score_label", { score: n, label: label(n) })}
                    onClick={() => setScore(n)}
                    className={cn(
                      "group flex flex-col items-center gap-2 rounded-lg border px-1 pb-2.5 pt-3.5 outline-none transition-[border-color,background-color,color,transform] duration-150 focus-visible:shadow-[0_0_0_3px_var(--focus)] active:scale-[0.97]",
                      on ? cn(h.soft, h.line) : "border-line hover:border-line-strong hover:bg-sunken",
                    )}
                  >
                    <motion.span
                      className={cn("flex transition-colors duration-150", on ? h.text : "text-fg-4 group-hover:text-fg-2")}
                      animate={reduce ? undefined : { scale: on ? 1.08 : 1 }}
                      transition={{ type: "spring", stiffness: 520, damping: 26 }}
                    >
                      <Face score={n} />
                    </motion.span>
                    <span className={cn("text-[12px] leading-tight transition-colors", on ? cn("font-medium", h.text) : "text-fg-3")}>{label(n)}</span>
                  </button>
                );
              })}
            </div>
            <div className="mt-6 text-start">
              <label htmlFor="csat-comment" className="text-[12.5px] font-medium">
                {t("csat.comment_label")} <span className="font-normal text-fg-4">{t("csat.optional")}</span>
              </label>
              <Textarea id="csat-comment" className="mt-1.5 min-h-[96px]" value={comment} maxLength={2000} onChange={(e) => setComment(e.target.value)} placeholder={t("csat.comment_placeholder")} />
            </div>
            {submit.isError && (
              <Notice className="mt-4 text-start">{isApiError(submit.error, "network_error") ? t("csat.offline") : t("csat.error")}</Notice>
            )}
            {sentScore !== null && !submit.isPending && <p className="mt-4 text-[12.5px] text-fg-3" aria-live="polite">{t("csat.saved")}</p>}
            <Button
              variant="primary"
              size="lg"
              className="mt-5 w-full"
              disabled={!score}
              loading={submit.isPending}
              onClick={() => score && submit.mutate({ score, comment: comment.trim() || undefined }, { onSuccess: () => setFinished(true) })}
            >
              {sentScore !== null && sentScore === score ? t("csat.send_comment") : t("csat.send_feedback")}
            </Button>
          </div>
        )}
      </main>
      <footer className="flex h-14 items-center gap-3 text-xs text-fg-4">
        <LanguageSwitcher current={locale} />
        <span className="flex items-center gap-1.5">
          {t("footer.powered_by")} <Lockup className="h-[11px]" />
        </span>
      </footer>
    </div>
  );
}

function Done({ score, title, body }: { score: number; title: string; body: string }) {
  const reduce = useReducedMotion();
  const h = tone[hueOf(score)];
  const spring = { type: "spring", stiffness: 420, damping: 28 } as const;
  const rise = (delay: number) =>
    reduce ? {} : { initial: { opacity: 0, y: 6 }, animate: { opacity: 1, y: 0 }, transition: { duration: 0.42, delay, ease: [0.16, 1, 0.3, 1] as const } };
  return (
    <div className="flex flex-col items-center text-center" aria-live="polite">
      <motion.span
        className={cn("relative mb-6 flex size-16 items-center justify-center rounded-xl border", h.soft, h.line, h.text)}
        {...(reduce ? {} : { initial: { opacity: 0, scale: 0.86 }, animate: { opacity: 1, scale: 1 }, transition: spring })}
      >
        <Face score={score} className="size-10" draw={!reduce} />
        <motion.span
          className="absolute -bottom-1.5 -end-1.5 flex size-6 items-center justify-center rounded-md bg-inverse text-inverse-fg ring-[3px] ring-surface"
          {...(reduce ? {} : { initial: { opacity: 0, scale: 0.5 }, animate: { opacity: 1, scale: 1 }, transition: { ...spring, delay: 0.28 } })}
        >
          <Check className="size-3.5" strokeWidth={2.75} />
        </motion.span>
      </motion.span>
      <motion.h1 className="text-[22px] font-semibold tracking-[-0.03em] text-fg" {...rise(0.12)}>
        {title}
      </motion.h1>
      <motion.p className="mt-1.5 max-w-[340px] text-[13.5px] leading-5 text-fg-3" {...rise(0.18)}>
        {body}
      </motion.p>
    </div>
  );
}
