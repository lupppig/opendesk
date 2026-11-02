// The agent quality loop (contracts/agent.md "Tests", "Improve answer"): the
// test editor, test status pills, and the "Improve answer" dialog an admin
// opens on any Ollie reply, in a chat ticket, the playground or the review queue.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { AlertCircle, CircleCheck, CircleDashed, CircleX, Clock, MessageSquareQuote, Plus, Sparkles, Trash2, Wand2 } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Textarea, TextInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { Counter } from "@/components/agent/bits";
import { api } from "@/lib/api/endpoints";
import type { AgentExpectation, AgentExpectationKind, AgentTest, AgentTestBody, Message, PlaygroundMessage } from "@/lib/api/types";
import { EXPECTATION_MAX, MAX_EXPECTATIONS, MAX_TEST_MESSAGES, toolKey, type TestState } from "@/lib/agentTests";
import { springs } from "@/lib/motion";
import { q, qk, removeFromList, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { AI_TONE, tone, type Tone } from "@/lib/tones";
import { cn } from "@/lib/cn";

// ─── Status ─────────────────────────────────────────────────────────────────

const STATE_TONE: Record<TestState, Tone> = { passed: "green", failed: "red", error: "amber", queued: AI_TONE, never: "gray" };
const STATE_ICON: Record<TestState, typeof CircleCheck> = { passed: CircleCheck, failed: CircleX, error: AlertCircle, queued: Clock, never: CircleDashed };

export function TestStatePill({ state, className }: { state: TestState; className?: string }) {
  const { t } = useTranslation("agentQuality");
  const Icon = STATE_ICON[state];
  return (
    <span className={cn("inline-flex h-5 shrink-0 items-center gap-1 whitespace-nowrap rounded-sm px-1.5 text-[11px] font-medium", tone[STATE_TONE[state]].chip, className)}>
      <Icon className={cn("size-3", state === "queued" && "animate-pulse-soft motion-reduce:animate-none")} aria-hidden />
      {t(`state.${state}`)}
    </span>
  );
}

export const KINDS: AgentExpectationKind[] = ["mentions", "not_mentions", "handoff", "no_handoff", "uses_tool", "uses_procedure", "cites_knowledge"];
/** Kinds whose value is picked from a list (a tool, a procedure) rather than typed. */
export const isPicked = (k: AgentExpectationKind) => k === "uses_tool" || k === "uses_procedure";
export const needsValue = (k: AgentExpectationKind) => k === "mentions" || k === "not_mentions" || isPicked(k);

/** Names a picked value: a tool by its server and title, a procedure by its name. */
export type ValueLabel = (kind: AgentExpectationKind, value: string) => string;

/** "Mentions: refunds take 5 days", "Hands off". */
export function expectationText(e: AgentExpectation, t: (k: `kind.${AgentExpectationKind}`) => string, label?: ValueLabel): string {
  if (!needsValue(e.kind) || !e.value) return t(`kind.${e.kind}`);
  const v = isPicked(e.kind) ? (label?.(e.kind, e.value) ?? e.value) : e.value;
  return `${t(`kind.${e.kind}`)}: ${v}`;
}

/** Tool keys of the workspace's enabled MCP tools, with readable names. */
export function useToolOptions() {
  const admin = atLeast(useRole(), "admin");
  const { data } = useQuery({ ...q.mcpServers(), enabled: admin });
  const options = (data ?? [])
    .filter((s) => s.enabled)
    .flatMap((s) => s.tools.filter((x) => x.enabled).map((x) => ({ value: toolKey(s.slug, x.name), label: `${s.name} · ${x.title || x.name}` })));
  const name = (key: string) => options.find((o) => o.value === key)?.label ?? key.replace("__", " · ");
  return { options, name };
}

/** The workspace's procedures, by name. */
export function useProcedureOptions() {
  const admin = atLeast(useRole(), "admin");
  const { data } = useQuery({ ...q.procedures(), enabled: admin });
  const options = (data ?? []).map((p) => ({ value: p.id, label: p.name }));
  const name = (id: string) => options.find((o) => o.value === id)?.label ?? id;
  return { options, name };
}

/** Labels picked values from the workspace's tools and procedures. */
export function useValueLabel(): ValueLabel {
  const tools = useToolOptions();
  const procedures = useProcedureOptions();
  return (kind, value) => (kind === "uses_tool" ? tools.name(value) : kind === "uses_procedure" ? procedures.name(value) : value);
}

// ─── Test editor ────────────────────────────────────────────────────────────

type Row = { id: string; role: "visitor" | "agent"; text: string };
type Exp = { id: string; kind: AgentExpectationKind; value: string };
let rid = 0;
const nid = () => `r${++rid}`;

/** Create or edit a test. `initial` prefills a new one (Save as test from the playground). */
export function TestEditor({ test, initial, onClose }: { test?: AgentTest | null; initial?: Partial<AgentTestBody>; onClose: () => void }) {
  const { t } = useTranslation("agentQuality");
  const qc = useQueryClient();
  const reduce = useReducedMotion();
  const tools = useToolOptions();
  const procedures = useProcedureOptions();
  const src = test ?? initial;
  const [name, setName] = useState(test?.name ?? initial?.name ?? "");
  const [rows, setRows] = useState<Row[]>(() =>
    (src?.messages?.length ? src.messages : [{ role: "visitor" as const, text: "" }]).slice(-MAX_TEST_MESSAGES).map((m) => ({ id: nid(), ...m })),
  );
  const [exps, setExps] = useState<Exp[]>(() => (src?.expectations ?? []).map((e) => ({ id: nid(), kind: e.kind, value: e.value ?? "" })));
  const [verified, setVerified] = useState(!!src?.visitor?.verified);
  const [userId, setUserId] = useState(src?.visitor?.user_id ?? "");
  const [email, setEmail] = useState(src?.visitor?.email ?? "");
  const [tried, setTried] = useState(false);
  const firstText = useRef<HTMLTextAreaElement>(null);

  const kinds = new Set(exps.map((e) => e.kind));
  const lastVisitor = rows[rows.length - 1]?.role === "visitor";
  const rowError = (r: Row) => (!r.text.trim() ? t("editor.messageEmpty") : r.text.length > 4000 ? t("editor.messageLong") : null);
  const expError = (e: Exp) => {
    if (e.kind === "uses_tool") return e.value ? null : t("editor.pickTool");
    if (e.kind === "uses_procedure") return e.value ? null : t("editor.pickProcedure");
    if (needsValue(e.kind)) return !e.value.trim() ? t("editor.valueEmpty") : e.value.length > EXPECTATION_MAX ? t("editor.valueLong") : null;
    return null;
  };
  const conflict = kinds.has("handoff") && kinds.has("no_handoff");
  const valid = rows.every((r) => !rowError(r)) && lastVisitor && exps.length > 0 && exps.every((e) => !expError(e)) && !conflict;

  const body = (): AgentTestBody => ({
    name: name.trim() || undefined,
    messages: rows.map((r) => ({ role: r.role, text: r.text.trim() })),
    expectations: exps.map((e) => (needsValue(e.kind) ? { kind: e.kind, value: e.value.trim() } : { kind: e.kind })),
    visitor: verified ? { verified: true, user_id: userId.trim() || undefined, email: email.trim() || undefined } : {},
  });

  const save = useMutation({
    mutationFn: () => (test ? api.ai.agent.tests.update(test.id, body()) : api.ai.agent.tests.create({ ...body(), visitor: verified ? body().visitor : undefined })),
    onSuccess: (saved) => {
      upsertInList(qc, qk.agentTests, saved);
      toast.success(test ? t("editor.saved") : t("editor.created"));
      onClose();
    },
    onError: (e) => toast.error(e, t("editor.saveError")),
  });

  const addExp = (kind: AgentExpectationKind) => {
    const only = kind === "uses_tool" ? tools.options : kind === "uses_procedure" ? procedures.options : [];
    setExps((all) => [...all, { id: nid(), kind, value: only.length === 1 ? only[0].value : "" }]);
  };
  const patchRow = (id: string, p: Partial<Row>) => setRows((all) => all.map((r) => (r.id === id ? { ...r, ...p } : r)));
  const patchExp = (id: string, p: Partial<Exp>) => setExps((all) => all.map((e) => (e.id === id ? { ...e, ...p } : e)));
  const item = reduce ? {} : { initial: { opacity: 0, y: -4 }, animate: { opacity: 1, y: 0 }, exit: { opacity: 0, transition: { duration: 0.1 } }, transition: springs.row };

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      size="lg"
      title={test ? t("editor.editTitle") : t("editor.title")}
      description={t("editor.description")}
      footer={
        <>
          <Button onClick={onClose}>{t("cancel")}</Button>
          <Button variant="primary" type="submit" form="agent-test-form" loading={save.isPending}>
            {test ? t("save") : t("editor.create")}
          </Button>
        </>
      }
    >
      <form
        id="agent-test-form"
        className="flex flex-col gap-5"
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (valid) save.mutate();
        }}
      >
        <Field label={t("editor.name")} optional hint={t("editor.nameHint")}>
          {(p) => <TextInput {...p} maxLength={100} value={name} placeholder={rows.find((r) => r.role === "visitor")?.text.slice(0, 80) || t("editor.namePlaceholder")} onChange={(e) => setName(e.target.value)} />}
        </Field>

        <section className="flex flex-col gap-2" aria-labelledby="test-conversation">
          <div className="flex items-baseline justify-between">
            <h3 id="test-conversation" className="text-[12.5px] font-medium text-fg">
              {t("editor.conversation")}
            </h3>
            <span className="text-[11.5px] text-fg-4">{t("editor.conversationHint", { n: MAX_TEST_MESSAGES })}</span>
          </div>
          <ol className="flex flex-col gap-2">
            <AnimatePresence initial={false}>
              {rows.map((r, i) => {
                const err = tried ? rowError(r) : null;
                return (
                  <motion.li key={r.id} {...item} className="flex items-start gap-2">
                    <Segmented<"visitor" | "agent">
                      size="xs"
                      className="mt-1 shrink-0"
                      value={r.role}
                      onChange={(role) => patchRow(r.id, { role })}
                      options={[
                        { value: "visitor", label: t("editor.visitor") },
                        { value: "agent", label: t("editor.ollie") },
                      ]}
                    />
                    <div className="flex min-w-0 flex-1 flex-col gap-1">
                      <Textarea
                        ref={i === 0 ? firstText : undefined}
                        autoFocus={i === 0 && !r.text}
                        rows={r.text.length > 120 ? 3 : 1}
                        value={r.text}
                        invalid={!!err}
                        aria-label={t("editor.messageLabel", { n: i + 1 })}
                        placeholder={r.role === "visitor" ? t("editor.visitorPlaceholder") : t("editor.olliePlaceholder")}
                        className="min-h-8 text-[12.5px] [field-sizing:content]"
                        onChange={(e) => patchRow(r.id, { text: e.target.value })}
                      />
                      {err && <span className="text-xs text-danger-fg">{err}</span>}
                    </div>
                    <Button
                      square
                      size="sm"
                      variant="ghost"
                      className="mt-0.5"
                      aria-label={t("editor.removeMessage")}
                      disabled={rows.length === 1}
                      onClick={() => setRows((all) => all.filter((x) => x.id !== r.id))}
                    >
                      <Trash2 className="size-3.5" />
                    </Button>
                  </motion.li>
                );
              })}
            </AnimatePresence>
          </ol>
          <div className="flex items-center justify-between gap-3">
            <Button
              size="xs"
              variant="ghost"
              icon={<Plus />}
              disabled={rows.length >= MAX_TEST_MESSAGES}
              onClick={() => setRows((all) => [...all, { id: nid(), role: all[all.length - 1]?.role === "visitor" ? "agent" : "visitor", text: "" }])}
            >
              {t("editor.addMessage")}
            </Button>
            {tried && !lastVisitor && <span className="text-xs text-danger-fg">{t("editor.lastVisitor")}</span>}
          </div>
        </section>

        <section className="flex flex-col gap-2" aria-labelledby="test-expectations">
          <div className="flex items-baseline justify-between">
            <h3 id="test-expectations" className="text-[12.5px] font-medium text-fg">
              {t("editor.expectations")}
            </h3>
            <span className="text-[11.5px] text-fg-4">{t("editor.expectationsHint")}</span>
          </div>
          {exps.length === 0 && (
            <p className={cn("rounded-md border border-dashed px-3 py-2.5 text-[12px]", tried ? "border-danger-fg/40 text-danger-fg" : "border-line text-fg-3")}>{t("editor.noExpectations")}</p>
          )}
          <ul className="flex flex-col gap-2">
            <AnimatePresence initial={false}>
              {exps.map((e) => {
                const err = tried ? expError(e) : null;
                return (
                  <motion.li key={e.id} {...item} className="flex items-start gap-2">
                    <Select<AgentExpectationKind>
                      size="sm"
                      className="w-[190px] shrink-0"
                      value={e.kind}
                      aria-label={t("editor.kind")}
                      onChange={(kind) => patchExp(e.id, { kind, value: kind === e.kind ? e.value : isPicked(kind) ? "" : needsValue(kind) && needsValue(e.kind) && !isPicked(e.kind) ? e.value : "" })}
                      options={KINDS.map((k) => ({ value: k, label: t(`kind.${k}`) }))}
                    />
                    <div className="flex min-w-0 flex-1 flex-col gap-1">
                      {e.kind === "uses_tool" ? (
                        tools.options.length ? (
                          <Select<string> size="sm" value={e.value || undefined} placeholder={t("editor.pickTool")} aria-label={t("editor.tool")} onChange={(value) => patchExp(e.id, { value })} options={tools.options} />
                        ) : (
                          <span className="flex h-7 items-center text-[12px] text-fg-3">{t("editor.noTools")}</span>
                        )
                      ) : e.kind === "uses_procedure" ? (
                        procedures.options.length ? (
                          <Select<string>
                            size="sm"
                            value={e.value || undefined}
                            placeholder={t("editor.pickProcedure")}
                            aria-label={t("editor.procedure")}
                            onChange={(value) => patchExp(e.id, { value })}
                            options={procedures.options}
                          />
                        ) : (
                          <span className="flex h-7 items-center text-[12px] text-fg-3">{t("editor.noProcedures")}</span>
                        )
                      ) : needsValue(e.kind) ? (
                        <TextInput
                          inputSize="sm"
                          value={e.value}
                          invalid={!!err}
                          maxLength={EXPECTATION_MAX + 50}
                          aria-label={t(`kind.${e.kind}`)}
                          placeholder={e.kind === "mentions" ? t("editor.mentionsPlaceholder") : t("editor.notMentionsPlaceholder")}
                          onChange={(ev) => patchExp(e.id, { value: ev.target.value })}
                        />
                      ) : (
                        <span className="flex h-7 items-center text-[12px] text-fg-3">{t(`kindHint.${e.kind}`)}</span>
                      )}
                      {err && <span className="text-xs text-danger-fg">{err}</span>}
                    </div>
                    <Button square size="sm" variant="ghost" aria-label={t("editor.removeExpectation")} onClick={() => setExps((all) => all.filter((x) => x.id !== e.id))}>
                      <Trash2 className="size-3.5" />
                    </Button>
                  </motion.li>
                );
              })}
            </AnimatePresence>
          </ul>
          <div className="flex items-center justify-between gap-3">
            <Menu>
              <MenuTrigger asChild>
                <Button size="xs" variant="ghost" icon={<Plus />} disabled={exps.length >= MAX_EXPECTATIONS}>
                  {t("editor.addExpectation")}
                </Button>
              </MenuTrigger>
              <MenuContent align="start" className="min-w-[220px]">
                {KINDS.map((k) => (
                  <MenuItem key={k} onSelect={() => addExp(k)} disabled={!needsValue(k) && kinds.has(k)}>
                    {t(`kind.${k}`)}
                  </MenuItem>
                ))}
              </MenuContent>
            </Menu>
            {tried && conflict && <span className="text-xs text-danger-fg">{t("editor.conflict")}</span>}
          </div>
        </section>

        <section className="flex flex-col gap-3 rounded-lg border border-line bg-sunken/60 px-3.5 py-3">
          <label className="flex items-start justify-between gap-3">
            <span className="min-w-0">
              <span className="block text-[12.5px] font-medium text-fg">{t("editor.verified")}</span>
              <span className="block text-[11.5px] leading-[17px] text-fg-3">{t("editor.verifiedHint")}</span>
            </span>
            <Switch checked={verified} onCheckedChange={setVerified} aria-label={t("editor.verified")} />
          </label>
          {verified && (
            <div className="grid animate-rise-in grid-cols-2 gap-3">
              <Field label={t("editor.userId")} optional>
                {(p) => <TextInput {...p} inputSize="sm" maxLength={200} value={userId} placeholder="u_1042" onChange={(e) => setUserId(e.target.value)} />}
              </Field>
              <Field label={t("editor.email")} optional>
                {(p) => <TextInput {...p} inputSize="sm" type="email" maxLength={254} value={email} placeholder="ada@example.com" onChange={(e) => setEmail(e.target.value)} />}
              </Field>
            </div>
          )}
        </section>
      </form>
    </Dialog>
  );
}

// ─── Improve answer ─────────────────────────────────────────────────────────

/**
 * The visitor's question and Ollie's answer, and the better answer the admin
 * writes. It saves a Q&A snippet Ollie answers from at once, and optionally a
 * test expecting it.
 */
export function ImproveAnswerDialog({
  question: initialQuestion,
  answer,
  messageId,
  onClose,
}: {
  question: string;
  answer: string;
  messageId?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation("agentQuality");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [question, setQuestion] = useState(initialQuestion.slice(0, 4000));
  const [better, setBetter] = useState("");
  const [saveTest, setSaveTest] = useState(true);
  const [tried, setTried] = useState(false);
  const qLen = question.trim().length;
  const aLen = better.trim().length;
  const qErr = tried && (qLen < 3 || qLen > 4000) ? t("improve.questionInvalid") : null;
  const aErr = tried && (aLen < 1 || aLen > 4000) ? t("improve.answerInvalid") : null;

  const save = useMutation({
    mutationFn: () => api.ai.agent.improve({ question: question.trim(), answer: better.trim(), message_id: messageId, save_test: saveTest }),
    onSuccess: (r) => {
      if (r.test) upsertInList(qc, qk.agentTests, r.test);
      if (messageId) removeFromList(qc, qk.agentReview, messageId);
      void qc.invalidateQueries({ queryKey: qk.knowledge });
      toast.success(t("improve.saved"), r.test ? { description: t("improve.savedTest"), action: { label: t("improve.viewTests"), onClick: () => void navigate({ to: "/settings/ai/tests" }) } } : { description: t("improve.savedBody") });
      onClose();
    },
    onError: (e) => toast.error(e, t("improve.error")),
  });

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      size="md"
      title={t("improve.title")}
      description={t("improve.description")}
      footer={
        <>
          <Button onClick={onClose}>{t("cancel")}</Button>
          <Button variant="primary" type="submit" form="improve-form" loading={save.isPending} icon={<Wand2 />}>
            {t("improve.submit")}
          </Button>
        </>
      }
    >
      <form
        id="improve-form"
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (qLen >= 3 && qLen <= 4000 && aLen >= 1 && aLen <= 4000) save.mutate();
        }}
      >
        <Field label={t("improve.question")} error={qErr} hint={t("improve.questionHint")}>
          {(p) => <Textarea {...p} rows={2} className="text-[12.5px]" value={question} onChange={(e) => setQuestion(e.target.value)} data-ph-mask />}
        </Field>
        <div className="flex flex-col gap-1.5">
          <span className="flex items-center gap-1.5 text-[12.5px] font-medium text-fg">
            <Sparkles className={cn("size-3.5", tone[AI_TONE].text)} aria-hidden />
            {t("improve.ollieAnswer")}
          </span>
          <div className={cn("max-h-32 overflow-y-auto whitespace-pre-wrap rounded-md border px-3 py-2 text-[12.5px] leading-[18px] text-fg-2", tone[AI_TONE].soft, tone[AI_TONE].line)} data-ph-mask>
            {answer.trim() || <span className="text-fg-4">{t("improve.noAnswer")}</span>}
          </div>
        </div>
        <Field label={t("improve.better")} error={aErr} hint={t("improve.betterHint")}>
          {(p) => (
            <div className="flex flex-col gap-1">
              <Textarea {...p} autoFocus rows={6} value={better} placeholder={t("improve.betterPlaceholder")} onChange={(e) => setBetter(e.target.value)} />
              <div className="flex items-center justify-between">
                {answer.trim() && !better ? (
                  <Button size="xs" variant="ghost" icon={<MessageSquareQuote />} onClick={() => setBetter(answer.trim().slice(0, 4000))}>
                    {t("improve.startFrom")}
                  </Button>
                ) : (
                  <span />
                )}
                <Counter n={better.length} max={4000} />
              </div>
            </div>
          )}
        </Field>
        <label className="flex items-start justify-between gap-3 rounded-lg border border-line bg-sunken/60 px-3.5 py-2.5">
          <span className="min-w-0">
            <span className="block text-[12.5px] font-medium text-fg">{t("improve.saveTest")}</span>
            <span className="block text-[11.5px] leading-[17px] text-fg-3">{t("improve.saveTestHint")}</span>
          </span>
          <Switch checked={saveTest} onCheckedChange={setSaveTest} aria-label={t("improve.saveTest")} />
        </label>
      </form>
    </Dialog>
  );
}

/** The visitor's message an Ollie reply answered: the latest inbound reply before it. */
export function questionFor(m: Message, all: Message[] | undefined): string {
  if (!all) return "";
  const i = all.findIndex((x) => x.id === m.id);
  const before = i < 0 ? all.filter((x) => x.created_at < m.created_at) : all.slice(0, i);
  for (let j = before.length - 1; j >= 0; j--) {
    const x = before[j];
    if (x.kind === "reply" && x.direction === "inbound") return x.body_text.trim();
  }
  return "";
}

/**
 * "Improve answer" on an Ollie reply in the ticket thread (admins). A reply the
 * visitor rated not helpful shows it at once; others on hover or focus.
 */
export function ImproveAnswerButton({ m }: { m: Message }) {
  const { t } = useTranslation("agentQuality");
  const admin = atLeast(useRole(), "admin");
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  if (!admin || !m.ai || m.kind !== "reply") return null;
  const down = m.ai_rating === "down";
  const question = open ? questionFor(m, qc.getQueryData<Message[]>(qk.messages(m.ticket_id))) : "";
  return (
    <>
      <Tooltip content={down ? t("improve.ratedDownHint") : t("improve.hint")}>
        <Button
          size="xs"
          variant={down ? "secondary" : "ghost"}
          icon={<Wand2 />}
          onClick={() => setOpen(true)}
          className={cn(!down && "opacity-0 transition-opacity focus-visible:opacity-100 group-hover/msg:opacity-100 group-focus-within/msg:opacity-100")}
        >
          {t("improve.action")}
        </Button>
      </Tooltip>
      {open && <ImproveAnswerDialog question={question} answer={m.body_text} messageId={m.id} onClose={() => setOpen(false)} />}
    </>
  );
}

/** A labelled group of check verdicts under a result. */
export function CheckList({ checks, label }: { checks: { kind: AgentExpectationKind; value: string; passed: boolean; reason: string }[]; label: ValueLabel }) {
  const { t } = useTranslation("agentQuality");
  return (
    <ul className="flex flex-col gap-1.5">
      {checks.map((c, i) => (
        <li key={i} className="flex items-start gap-2 text-[12.5px] leading-[18px]">
          <span className={cn("mt-px flex size-4 shrink-0 items-center justify-center rounded-full", c.passed ? tone.green.chip : tone.red.chip)} aria-label={c.passed ? t("check.passed") : t("check.failed")} role="img">
            {c.passed ? <CircleCheck className="size-2.5" /> : <CircleX className="size-2.5" />}
          </span>
          <span className="min-w-0">
            <span className="font-medium text-fg">{expectationText(c, t, label)}</span>
            {c.reason && <span className="block text-fg-3">{c.reason}</span>}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** Messages as a test's conversation: the last few, ending with the visitor's. */
export function toTestMessages(all: PlaygroundMessage[]): PlaygroundMessage[] {
  let end = all.length;
  while (end > 0 && all[end - 1].role !== "visitor") end--;
  return all.slice(Math.max(0, end - MAX_TEST_MESSAGES), end).map((m) => ({ role: m.role, text: m.text.slice(0, 4000) }));
}
