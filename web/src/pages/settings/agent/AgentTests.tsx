// Settings > AI agent > Tests (contracts/agent.md "Tests"): saved test
// conversations with what Ollie's answer must do, run against the saved
// settings or the Behaviour page's unsaved edits. Runs are background jobs;
// progress and results arrive live (ai_agent_test_run.*, ai_agent_test_result.*).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, ChevronRight, ClipboardCheck, FlaskConical, MoreHorizontal, Pencil, Play, Plus, Square, Trash2, Wand2 } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { NoAccess, Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { ListSkeleton, SummaryStrip } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { useListMutation } from "@/components/settings2/optimistic";
import { AgentOffNotice } from "@/components/agent/bits";
import { agentSettings, playgroundSettings, useAgentDraft } from "@/components/agent/settings";
import { CheckList, expectationText, TestEditor, TestStatePill, useToolOptions } from "@/components/agent/quality";
import { api } from "@/lib/api/endpoints";
import type { AgentTest, AgentTestRun } from "@/lib/api/types";
import { activeRun, MAX_TESTS, newlyFailing, passRate, runProgress, testState, type TestState } from "@/lib/agentTests";
import { useAI } from "@/lib/ai";
import { formatUsd, number, percent, relShort } from "@/lib/format";
import { dur, springs } from "@/lib/motion";
import { q, qk, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { AI_TONE, tone } from "@/lib/tones";
import { cn } from "@/lib/cn";
import { sanitizeHtml } from "@/lib/sanitize";

type Filter = "all" | "failing" | "passing" | "never";
type Editing = { test: AgentTest | null } | null;

export default function AgentTests() {
  const { t } = useTranslation("agentQuality");
  const admin = atLeast(useRole(), "admin");
  const ai = useAI();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const { data: tests, isLoading } = useQuery({ ...q.agentTests(), enabled: admin });
  const { data: runs } = useQuery({ ...q.agentTestRuns(), enabled: admin });
  const draft = useAgentDraft((s) => s.draft);
  const [target, setTarget] = useState<"draft" | "saved">("draft");
  const [filter, setFilter] = useState<Filter>("all");
  const [editing, setEditing] = useState<Editing>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const tools = useToolOptions();

  const list = useMemo(() => tests ?? [], [tests]);
  const run = activeRun(runs);
  const last = runs?.find((r) => r.status !== "queued" && r.status !== "running") ?? null;
  const rate = passRate(list);
  const failingNew = list.filter((x) => newlyFailing(x.last_result));
  const states = new Map(list.map((x) => [x.id, testState(x, run)] as const));
  const count = (f: (s: TestState) => boolean) => list.filter((x) => f(states.get(x.id) ?? "never")).length;
  const shown = list.filter((x) => {
    const s = states.get(x.id) ?? "never";
    return filter === "all" || (filter === "failing" ? s === "failed" || s === "error" : filter === "passing" ? s === "passed" : s === "never");
  });
  const useDraft = !!draft && target === "draft";

  const start = useMutation({
    mutationFn: (ids?: string[]) => api.ai.agent.runs.start({ test_ids: ids, settings: useDraft ? playgroundSettings(draft) : undefined }),
    onSuccess: (r) => upsertInList(qc, qk.agentTestRuns, r, { prepend: true }),
    onError: (e) => toast.error(e, t("run.startError")),
  });
  const cancel = useMutation({
    mutationFn: (id: string) => api.ai.agent.runs.cancel(id),
    onMutate: (id) => qc.setQueryData<AgentTestRun[]>(qk.agentTestRuns, (old) => old?.map((r) => (r.id === id ? { ...r, status: "cancelled" } : r))),
    onSuccess: (r) => upsertInList(qc, qk.agentTestRuns, r),
    onError: (e) => {
      toast.error(e, t("run.stopError"));
      void qc.invalidateQueries({ queryKey: qk.agentTestRuns });
    },
  });
  const remove = useListMutation<AgentTest, string>(qk.agentTests, (id) => api.ai.agent.tests.remove(id), (rows, id) => rows.filter((r) => r.id !== id), {
    errorText: t("deleteError"),
  });

  const askDelete = (x: AgentTest) =>
    confirm.show(<>{t("deleteBody", { name: x.name })}</>, () => remove.mutateAsync(x.id), { title: t("deleteTitle"), confirmLabel: t("delete"), danger: true });

  if (!admin)
    return (
      <SettingsPage title={t("title")}>
        <NoAccess what={t("what")} />
      </SettingsPage>
    );

  const busy = !!run || start.isPending;
  const actions = (
    <div className="flex items-center gap-2">
      <Button icon={<Plus className="size-3.5" />} onClick={() => setEditing({ test: null })} disabled={list.length >= MAX_TESTS}>
        {t("new")}
      </Button>
      {run ? (
        <Button icon={<Square className="size-3 fill-current" />} onClick={() => cancel.mutate(run.id)} loading={cancel.isPending}>
          {t("run.stop")}
        </Button>
      ) : (
        <Button variant="primary" icon={<Play className="size-3.5" />} onClick={() => start.mutate(undefined)} loading={start.isPending} disabled={!list.length || !ai.enabled}>
          {t("run.all")}
        </Button>
      )}
    </div>
  );

  return (
    <SettingsPage wide title={t("title")} description={t("description")} actions={actions}>
      {ai.config && !agentSettings(ai.features).widget_answers && <AgentOffNotice className="-mb-3" />}
      {draft && list.length > 0 && (
        <div className={cn("flex animate-rise-in flex-wrap items-center gap-3 rounded-lg border px-4 py-2.5", tone[AI_TONE].soft, tone[AI_TONE].line)} role="status">
          <FlaskConical className={cn("size-4 shrink-0", tone[AI_TONE].text)} aria-hidden />
          <div className="min-w-0 flex-1 text-[12.5px] leading-[18px]">
            <span className="font-medium text-fg">{t("draft.title")}</span> <span className="text-fg-3">{t("draft.body")}</span>
          </div>
          <Segmented<"draft" | "saved">
            size="xs"
            value={target}
            onChange={setTarget}
            options={[
              { value: "draft", label: t("draft.unsaved") },
              { value: "saved", label: t("draft.saved") },
            ]}
          />
        </div>
      )}

      {isLoading ? (
        <Section flush>
          <ListSkeleton rows={4} />
        </Section>
      ) : !list.length ? (
        <Section flush>
          <SettingsEmpty
            art="ai"
            title={t("empty.title")}
            description={t("empty.description")}
            action={
              <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEditing({ test: null })}>
                {t("new")}
              </Button>
            }
            secondary={
              <Button asChild>
                <Link to="/settings/ai/playground">
                  <FlaskConical className="size-3.5" />
                  {t("empty.playground")}
                </Link>
              </Button>
            }
            tips={[
              { label: t("empty.tipPlayground"), icon: <ClipboardCheck /> },
              { label: t("empty.tipImprove"), icon: <Wand2 /> },
            ]}
          />
        </Section>
      ) : (
        <>
          <SummaryStrip
            items={[
              {
                label: t("summary.passRate"),
                value: rate.rate === null ? "–" : percent(rate.rate),
                tone: rate.rate === null ? undefined : rate.rate === 1 ? "green" : rate.rate >= 0.8 ? "amber" : "red",
                hint: rate.ran ? t("summary.passing", { passed: number(rate.passed), ran: number(rate.ran) }) : t("summary.notRun"),
              },
              {
                label: t("summary.newlyFailing"),
                value: number(failingNew.length),
                tone: failingNew.length ? "red" : undefined,
                hint: t("summary.newlyFailingHint"),
              },
              {
                label: t("summary.lastRun"),
                value: last ? relShort(last.finished_at ?? last.created_at) : "–",
                hint: last ? (last.draft ? t("summary.againstDraft") : t("summary.againstSaved")) : t("summary.noRuns"),
              },
              { label: t("summary.tests"), value: number(list.length), hint: t("summary.testsHint", { n: MAX_TESTS }) },
              { label: t("summary.spent"), value: last ? formatUsd(last.usd, { precise: true }) : "–", hint: t("summary.spentHint") },
            ]}
          />
          <AnimatePresence initial={false}>{run && <RunBanner key={run.id} run={run} />}</AnimatePresence>
          {!run && last?.status === "failed" && last.error && (
            <div className={cn("flex animate-rise-in items-center gap-2.5 rounded-lg border px-4 py-2.5 text-[12.5px]", tone.amber.soft, tone.amber.line)} role="status">
              <AlertTriangle className={cn("size-4 shrink-0", tone.amber.text)} aria-hidden />
              <span className="text-fg">{last.error}</span>
            </div>
          )}
          {failingNew.length > 0 && !run && (
            <div className={cn("flex animate-rise-in flex-col gap-1.5 rounded-lg border px-4 py-3", tone.red.soft, tone.red.line)} role="status">
              <div className="text-[12.5px] font-medium text-fg">{t("newlyFailing.title", { count: failingNew.length })}</div>
              <ul className="flex flex-wrap gap-1.5">
                {failingNew.map((x) => (
                  <li key={x.id}>
                    <button
                      type="button"
                      onClick={() => {
                        setFilter("all");
                        setOpenId(x.id);
                        requestAnimationFrame(() => document.getElementById(`test-${x.id}`)?.scrollIntoView({ block: "center", behavior: "smooth" }));
                      }}
                      className="inline-flex h-6 max-w-[280px] items-center rounded-sm border border-line bg-surface px-2 text-[12px] text-fg-2 transition-colors hover:border-line-strong hover:text-fg"
                    >
                      <span className="truncate">{x.name}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          <Section
            flush
            title={t("count", { count: list.length })}
            actions={
              <Segmented<Filter>
                size="xs"
                value={filter}
                onChange={setFilter}
                options={[
                  { value: "all", label: t("filter.all") },
                  { value: "failing", label: `${t("filter.failing")} ${count((s) => s === "failed" || s === "error") || ""}`.trim() },
                  { value: "passing", label: `${t("filter.passing")} ${count((s) => s === "passed") || ""}`.trim() },
                  { value: "never", label: `${t("filter.never")} ${count((s) => s === "never") || ""}`.trim() },
                ]}
              />
            }
          >
            {!shown.length ? (
              <div className="px-4 py-10 text-center text-[12.5px] text-fg-3">{t("noneInFilter")}</div>
            ) : (
              <MotionList>
                {shown.map((x) => (
                  <MotionRow key={x.id}>
                    <TestRow
                      test={x}
                      state={states.get(x.id) ?? "never"}
                      open={openId === x.id}
                      onToggle={() => setOpenId((v) => (v === x.id ? null : x.id))}
                      onRun={() => start.mutate([x.id])}
                      runDisabled={busy || !ai.enabled}
                      onEdit={() => setEditing({ test: x })}
                      onDelete={() => askDelete(x)}
                      toolName={tools.name}
                    />
                  </MotionRow>
                ))}
              </MotionList>
            )}
          </Section>
          <p className="-mt-3 text-[12px] leading-[18px] text-fg-3">{t("note", { cost: formatUsd(ai.cost("playground") ?? 0.03, { precise: true }) })}</p>
        </>
      )}
      {editing && <TestEditor test={editing.test} onClose={() => setEditing(null)} />}
    </SettingsPage>
  );
}

function RunBanner({ run }: { run: AgentTestRun }) {
  const { t } = useTranslation("agentQuality");
  const reduce = useReducedMotion();
  const done = run.passed + run.failed + run.errored;
  return (
    <motion.div
      initial={reduce ? false : { opacity: 0, y: -4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, transition: { duration: dur.fast } }}
      transition={springs.row}
      className={cn("flex flex-col gap-2 rounded-lg border px-4 py-3", tone[AI_TONE].soft, tone[AI_TONE].line)}
      role="status"
      aria-live="polite"
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]">
        <span className="font-medium text-fg">{run.status === "queued" ? t("run.queued") : t("run.running", { done: number(done), total: number(run.total) })}</span>
        {run.draft && <span className={cn("inline-flex h-5 items-center rounded-sm px-1.5 text-[11px] font-medium", tone[AI_TONE].chip)}>{t("run.draft")}</span>}
        <span className="ml-auto flex items-center gap-3 text-fg-3">
          <span className="inline-flex items-center gap-1">
            <span className={cn("size-1.5 rounded-full", tone.green.solid)} aria-hidden />
            {t("run.passed", { n: number(run.passed) })}
          </span>
          <span className="inline-flex items-center gap-1">
            <span className={cn("size-1.5 rounded-full", tone.red.solid)} aria-hidden />
            {t("run.failed", { n: number(run.failed + run.errored) })}
          </span>
          <span className="mono tabular-nums">{formatUsd(run.usd, { precise: true })}</span>
        </span>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-surface" role="progressbar" aria-valuemin={0} aria-valuemax={run.total} aria-valuenow={done} aria-label={t("run.progress")}>
        <div className={cn("h-full rounded-full transition-[width] duration-500 ease-out motion-reduce:transition-none", tone[AI_TONE].solid)} style={{ width: `${Math.max(3, runProgress(run) * 100)}%` }} />
      </div>
    </motion.div>
  );
}

function TestRow({
  test,
  state,
  open,
  onToggle,
  onRun,
  runDisabled,
  onEdit,
  onDelete,
  toolName,
}: {
  test: AgentTest;
  state: TestState;
  open: boolean;
  onToggle: () => void;
  onRun: () => void;
  runDisabled: boolean;
  onEdit: () => void;
  onDelete: () => void;
  toolName: (k: string) => string;
}) {
  const { t } = useTranslation("agentQuality");
  const reduce = useReducedMotion();
  const r = test.last_result;
  const fresh = newlyFailing(r);
  const first = test.messages.find((m) => m.role === "visitor")?.text ?? "";
  return (
    <div id={`test-${test.id}`} className={cn(fresh && "bg-[color-mix(in_srgb,var(--tone-red-soft)_55%,transparent)]")}>
      <div
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={onToggle}
        onKeyDown={(e) => {
          if ((e.key === "Enter" || e.key === " ") && e.target === e.currentTarget) {
            e.preventDefault();
            onToggle();
          }
        }}
        className="group flex cursor-pointer items-center gap-3 px-4 py-2.5 outline-none transition-colors hover:bg-sunken focus-visible:bg-sunken"
      >
        <ChevronRight className={cn("size-3.5 shrink-0 text-fg-4 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-90")} aria-hidden />
        <TestStatePill state={state} className="w-[76px] justify-center" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-[12.5px] font-medium text-fg">{test.name}</span>
            {fresh && <span className={cn("inline-flex h-[18px] shrink-0 items-center rounded-sm px-1.5 text-[10.5px] font-medium", tone.red.chip)}>{t("newBadge")}</span>}
          </div>
          {first !== test.name && (
            <div className="truncate text-[12px] text-fg-3" data-ph-mask>
              {first}
            </div>
          )}
        </div>
        <span className="hidden shrink-0 text-[11.5px] text-fg-4 md:inline">{t("expectations", { count: test.expectations.length })}</span>
        <span className="hidden w-16 shrink-0 text-right text-[11.5px] text-fg-4 md:inline">{r?.finished_at ? relShort(r.finished_at) : ""}</span>
        <div className="flex shrink-0 items-center gap-0.5" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
          <Tooltip content={t("runOne")}>
            <Button square size="xs" variant="ghost" aria-label={t("runOne")} onClick={onRun} disabled={runDisabled}>
              <Play className="size-3" />
            </Button>
          </Tooltip>
          <Menu>
            <MenuTrigger asChild>
              <Button square size="xs" variant="ghost" aria-label={t("more")}>
                <MoreHorizontal className="size-3.5" />
              </Button>
            </MenuTrigger>
            <MenuContent>
              <MenuItem icon={<Pencil />} onSelect={onEdit}>
                {t("edit")}
              </MenuItem>
              <MenuSeparator />
              <MenuItem icon={<Trash2 />} danger onSelect={onDelete}>
                {t("delete")}
              </MenuItem>
            </MenuContent>
          </Menu>
        </div>
      </div>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            initial={reduce ? false : { height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { height: 0, opacity: 0, transition: { duration: dur.fast } }}
            transition={springs.row}
            className="overflow-hidden"
          >
            <TestDetail test={test} toolName={toolName} />
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

function TestDetail({ test, toolName }: { test: AgentTest; toolName: (k: string) => string }) {
  const { t } = useTranslation("agentQuality");
  const r = test.last_result;
  return (
    <div className="grid grid-cols-1 gap-5 border-t border-line-soft bg-sunken/50 px-4 py-4 pl-11 lg:grid-cols-2">
      <div className="flex min-w-0 flex-col gap-3">
        <div className="label-caps">{t("detail.conversation")}</div>
        <ol className="flex flex-col gap-1.5">
          {test.messages.map((m, i) => (
            <li key={i} className={cn("flex", m.role === "visitor" ? "justify-end" : "justify-start")}>
              <span
                className={cn(
                  "max-w-[90%] whitespace-pre-wrap break-words rounded-xl px-3 py-1.5 text-[12.5px] leading-[18px]",
                  m.role === "visitor" ? "rounded-br-sm bg-inverse text-inverse-fg" : cn("rounded-tl-sm border text-fg", tone[AI_TONE].soft, tone[AI_TONE].line),
                )}
                data-ph-mask
              >
                {m.text}
              </span>
            </li>
          ))}
        </ol>
        <div className="label-caps mt-1">{t("detail.expects")}</div>
        <ul className="flex flex-wrap gap-1.5">
          {test.expectations.map((e, i) => (
            <li key={i} className="inline-flex max-w-full items-center rounded-sm border border-line bg-surface px-2 py-0.5 text-[12px] text-fg-2">
              <span className="truncate">{expectationText(e, t, toolName)}</span>
            </li>
          ))}
        </ul>
        {test.visitor?.verified && <span className="text-[11.5px] text-fg-3">{t("detail.verified")}</span>}
      </div>
      <div className="flex min-w-0 flex-col gap-3">
        <div className="flex items-center gap-2">
          <span className="label-caps flex-1">{t("detail.latest")}</span>
          {r?.draft && <span className={cn("inline-flex h-[18px] items-center rounded-sm px-1.5 text-[10.5px] font-medium", tone[AI_TONE].chip)}>{t("run.draft")}</span>}
          {r && <span className="mono text-[10.5px] tabular-nums text-fg-4">{formatUsd(r.usd, { precise: true })}</span>}
        </div>
        {!r ? (
          <p className="text-[12.5px] text-fg-3">{t("detail.notRun")}</p>
        ) : (
          <>
            {r.error && (
              <p className={cn("flex items-start gap-1.5 rounded-md px-2.5 py-2 text-[12px]", tone.amber.chip)} role="alert">
                <AlertTriangle className="mt-px size-3.5 shrink-0" aria-hidden />
                {r.error}
              </p>
            )}
            {r.answer_html ? (
              <div className="prose-od rounded-lg border border-line bg-surface px-3 py-2 text-[12.5px] leading-[18px] [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_p]:my-1.5" data-ph-mask dangerouslySetInnerHTML={{ __html: sanitizeHtml(r.answer_html) }} />
            ) : (
              !r.error && <p className="text-[12.5px] text-fg-3">{t("detail.noAnswer")}</p>
            )}
            <div className="flex flex-wrap gap-1.5 text-[11.5px] text-fg-3">
              {r.handoff && <span className={cn("inline-flex h-5 items-center rounded-sm px-1.5", tone.amber.chip)}>{t("detail.handedOff")}</span>}
              {r.tools.map((x) => (
                <span key={x} className="mono inline-flex h-5 items-center rounded-sm bg-hover px-1.5">
                  {x}
                </span>
              ))}
              {r.action && <span className={cn("inline-flex h-5 items-center rounded-sm px-1.5", tone[AI_TONE].chip)}>{t("detail.action", { title: r.action.title })}</span>}
              {r.sources.map((s) => (
                <span key={`${s.kind}-${s.id}`} className="inline-flex h-5 max-w-[200px] items-center rounded-sm border border-line bg-surface px-1.5">
                  <span className="truncate">{s.title}</span>
                </span>
              ))}
            </div>
            {r.checks.length > 0 && <CheckList checks={r.checks} toolName={toolName} />}
          </>
        )}
      </div>
    </div>
  );
}
