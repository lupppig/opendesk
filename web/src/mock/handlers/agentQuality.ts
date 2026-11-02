// The agent quality loop (contracts/agent.md "Tests", "Improve answer",
// "Review queue"). Tests are answered by the playground's planner with
// read-only tools; a run is simulated with timers that publish
// ai_agent_test_run.* and ai_agent_test_result.updated as each test finishes.
// "Mentions" checks are graded by matching the expectation's words.

import type {
  AgentExpectation,
  AgentExpectationKind,
  AgentReviewItem,
  AgentTest,
  AgentTestCheck,
  AgentTestResult,
  AgentTestRun,
  PlaygroundMessage,
  PlaygroundVisitor,
} from "@/lib/api/types";
import { db, userRef, type WsData } from "../db";
import { effectiveModels, fromMicro, offersModel, reserve, roundUsd, spend, toMicro, typicalUsd } from "../aiBalance";
import { publish } from "../gateway";
import { HttpError, del, get, invalid, notFound, patch, post, respond, type Ctx } from "../router";
import { DAY, HOUR, MIN, ago, isRecord, markdownToHtml, nowIso, stripHtml, uuid } from "../util";
import { assertAi, requireAiAdmin, terms } from "./ai";
import { checkTrialBot, plan, widgetModel, type Overrides } from "./agent";
import { addSnippet } from "./knowledge";
import { mcpTool } from "./mcp";
import { proceduresOf } from "./procedures";

export const MAX_TESTS = 200;
const MAX_MESSAGES = 6;
const MAX_EXPECTATIONS = 10;
const EXPECTATION_MAX = 1000;
const NAME_MAX = 100;
const KINDS: AgentExpectationKind[] = ["mentions", "not_mentions", "handoff", "no_handoff", "uses_tool", "cites_knowledge", "uses_procedure"];
/** How long each test takes in the simulated run. */
export const STEP_MS = 700;

type StoredTest = Omit<AgentTest, "last_result">;
type StoredRun = Omit<AgentTestRun, "results"> & { settings: Overrides | null };
type State = { tests: StoredTest[]; runs: StoredRun[]; results: Map<string, AgentTestResult[]>; reviewed: Set<string>; timers: Map<string, ReturnType<typeof setTimeout>> };

const states = new WeakMap<WsData, State>();

function state(ws: WsData): State {
  let s = states.get(ws);
  if (!s) {
    s = { tests: [], runs: [], results: new Map(), reviewed: new Set(), timers: new Map() };
    states.set(ws, s);
    if (ws === db.workspaces[0]) seed(ws, s);
  }
  return s;
}

// ─── Reading ───────────────────────────────────────────────────────────────

const FINAL = new Set(["passed", "failed", "error"]);
const runOf = (s: State, id: string) => s.runs.find((r) => r.id === id);
const runOut = ({ settings: _s, ...r }: StoredRun): AgentTestRun => (void _s, { ...r });

/** The test's result in the run created before this one, for "newly failing". */
function previous(s: State, testId: string, run: StoredRun): AgentTestResult["previous_status"] {
  const earlier = s.runs.filter((r) => r.created_at < run.created_at).sort((a, b) => b.created_at.localeCompare(a.created_at));
  for (const r of earlier) {
    const x = s.results.get(r.id)?.find((y) => y.test_id === testId && FINAL.has(y.status));
    if (x) return x.status as AgentTestResult["previous_status"];
  }
  return null;
}

function withPrevious(s: State, r: AgentTestResult): AgentTestResult {
  const run = runOf(s, r.run_id);
  return run ? { ...r, previous_status: previous(s, r.test_id, run) } : r;
}

function lastResult(s: State, testId: string): AgentTestResult | null {
  const runs = [...s.runs].sort((a, b) => b.created_at.localeCompare(a.created_at));
  for (const r of runs) {
    const x = s.results.get(r.id)?.find((y) => y.test_id === testId && FINAL.has(y.status));
    if (x) return withPrevious(s, x);
  }
  return null;
}

const testOut = (s: State, t: StoredTest): AgentTest => ({ ...t, last_result: lastResult(s, t.id) });

function findTest(ctx: Ctx): StoredTest {
  const t = state(ctx.ws).tests.find((x) => x.id === ctx.params.id);
  if (!t) throw notFound("Test");
  return t;
}

// ─── Validation ────────────────────────────────────────────────────────────

function cleanName(v: unknown, messages: PlaygroundMessage[]): string {
  const raw = typeof v === "string" ? v.replace(/[\p{Cc}\p{Cf}]/gu, " ").replace(/\s+/g, " ").trim() : "";
  const name = raw || messages.find((m) => m.role === "visitor")?.text.replace(/\s+/g, " ").trim() || "Test";
  return name.length > NAME_MAX ? `${name.slice(0, NAME_MAX - 1).trim()}…` : name;
}

function cleanMessages(v: unknown): PlaygroundMessage[] {
  if (!Array.isArray(v) || v.length < 1 || v.length > MAX_MESSAGES) throw invalid(`messages must have 1 to ${MAX_MESSAGES} entries`);
  const out = v.map((m: unknown, i: number) => {
    if (!isRecord(m) || (m.role !== "visitor" && m.role !== "agent") || typeof m.text !== "string") throw invalid(`messages[${i}] needs a role (visitor or agent) and text`);
    const text = m.text.trim();
    if (!text) throw invalid(`messages[${i}] is empty`);
    if (text.length > 4000) throw invalid("Each message can be at most 4,000 characters");
    return { role: m.role, text } as PlaygroundMessage;
  });
  if (out[out.length - 1].role !== "visitor") throw invalid("The last message must be the visitor's");
  return out;
}

function cleanExpectations(ws: WsData, v: unknown): AgentExpectation[] {
  if (!Array.isArray(v) || v.length < 1 || v.length > MAX_EXPECTATIONS) throw invalid(`expectations must have 1 to ${MAX_EXPECTATIONS} entries`);
  const out = v.map((e: unknown, i: number): AgentExpectation => {
    if (!isRecord(e) || !KINDS.includes(e.kind as AgentExpectationKind)) throw invalid(`expectations[${i}].kind must be one of ${KINDS.join(", ")}`);
    const kind = e.kind as AgentExpectationKind;
    const value = typeof e.value === "string" ? e.value.replace(/\s+/g, " ").trim() : "";
    if (kind === "mentions" || kind === "not_mentions") {
      if (!value || value.length > EXPECTATION_MAX) throw invalid(`expectations[${i}].value must be 1 to ${EXPECTATION_MAX} characters`);
      return { kind, value };
    }
    if (kind === "uses_tool") {
      if (!/^[A-Za-z0-9_-]+__[A-Za-z0-9_-]+$/.test(value)) throw invalid(`expectations[${i}].value must be a tool key like shop__lookup_order`);
      return { kind, value };
    }
    if (kind === "uses_procedure") {
      if (!/^[0-9a-f-]{36}$/i.test(value)) throw invalid(`expectations[${i}].value must be a procedure id`);
      return { kind, value: value.toLowerCase() };
    }
    return { kind };
  });
  const kinds = new Set(out.map((e) => e.kind));
  if (kinds.has("handoff") && kinds.has("no_handoff")) throw invalid("A test can't expect a handoff and no handoff");
  void ws;
  return out;
}

function cleanVisitor(v: unknown): PlaygroundVisitor | null {
  if (v === undefined || v === null || !isRecord(v)) return null;
  const email = typeof v.email === "string" ? v.email.trim() : "";
  const user_id = typeof v.user_id === "string" ? v.user_id.trim() : "";
  const name = typeof v.name === "string" ? v.name.trim() : "";
  if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) throw invalid("visitor.email must be an email address");
  if (!email && !user_id && !name && !v.verified) return null;
  return { ...(name ? { name } : {}), ...(email ? { email } : {}), ...(user_id ? { user_id } : {}), verified: v.verified === true };
}

function audit(ws: WsData, action: string, entityType: string, id: string, data: Record<string, unknown>) {
  ws.audit.unshift({ id: uuid(), actor: { type: "user", user: userRef(db.meId), api_key_id: null }, action, entity_type: entityType, entity_id: id, data, ip: "84.152.33.17", created_at: nowIso() });
}

function createTest(ws: WsData, b: Record<string, unknown>): StoredTest {
  const s = state(ws);
  if (s.tests.length >= MAX_TESTS) throw new HttpError(422, "limit_reached", `A workspace can have at most ${MAX_TESTS} tests`);
  const messages = cleanMessages(b.messages);
  const now = nowIso();
  const t: StoredTest = { id: uuid(), name: cleanName(b.name, messages), messages, expectations: cleanExpectations(ws, b.expectations), visitor: cleanVisitor(b.visitor), created_at: now, updated_at: now };
  s.tests.push(t);
  audit(ws, "ai.agent_test_created", "ai_agent_test", t.id, { name: t.name });
  publish(ws, "ai_agent_test.created", { test: testOut(s, t) });
  return t;
}

// ─── Answering ─────────────────────────────────────────────────────────────

const MARKERS = /\s?\[\d+\](?!\()/g;

/** Whether most of the expectation's words are in the answer. */
function says(answer: string, value: string): boolean {
  const want = [...new Set(terms(value))];
  if (!want.length) return answer.toLowerCase().includes(value.toLowerCase());
  const have = new Set(terms(answer));
  return want.filter((w) => have.has(w)).length / want.length >= 0.6;
}

const verdict = (ok: boolean, yes: string, no: string) => (ok ? yes : no);

/** Answers a test like the worker: read-only tools only, deterministic checks, then the "judge". */
export function answerTest(ws: WsData, t: StoredTest, settings: Overrides | null, meter = true): Omit<AgentTestResult, "run_id" | "draft" | "previous_status" | "finished_at"> {
  const o = settings ?? {};
  const p = plan(ws, t.messages, o, t.visitor ?? {});
  const text = p.text.replace(MARKERS, "");
  const toolsUsed = p.tools.filter((x) => x.status === "done").map((x) => x.name);
  let usd = p.metered && meter ? spend(ws, "playground", db.meId, widgetModel(ws, o)) : p.metered ? typicalUsd("playground", widgetModel(ws, o)) : 0;
  const plain = stripHtml(markdownToHtml(text)) || p.action?.summary || "";
  let judged = false;
  const checks = t.expectations.map((e): AgentTestCheck => {
    const value = e.value ?? "";
    switch (e.kind) {
      case "handoff":
        return { kind: e.kind, value, passed: p.handoff, reason: verdict(p.handoff, "Ollie handed the conversation to the team.", "Ollie did not hand off.") };
      case "no_handoff":
        return { kind: e.kind, value, passed: !p.handoff, reason: verdict(!p.handoff, "Ollie answered without handing off.", "Ollie handed the conversation to the team.") };
      case "cites_knowledge": {
        const n = p.sources.length;
        return { kind: e.kind, value, passed: n > 0, reason: verdict(n > 0, `Ollie cited ${n} ${n === 1 ? "source" : "sources"}.`, "Ollie cited no knowledge.") };
      }
      case "uses_tool": {
        const [slug, name] = value.split("__");
        const tool = mcpTool(ws, slug, name);
        if (!tool) return { kind: e.kind, value, passed: false, reason: "This tool is not connected or not turned on." };
        const used = toolsUsed.includes(value) || p.action?.tool === value;
        const title = tool.tool.title || name;
        return { kind: e.kind, value, passed: used, reason: used ? `Ollie used ${title}.` : `Ollie did not use ${title}.` };
      }
      case "uses_procedure": {
        const proc = proceduresOf(ws).find((x) => x.id === value);
        if (!proc) return { kind: e.kind, value, passed: false, reason: "This procedure no longer exists." };
        const used = p.procedures.some((x) => x.id === value);
        if (used) return { kind: e.kind, value, passed: true, reason: `Ollie followed ${proc.name}.` };
        return { kind: e.kind, value, passed: false, reason: proc.enabled ? `Ollie did not follow ${proc.name}.` : `${proc.name} is turned off.` };
      }
      default: {
        if (!plain) return { kind: e.kind, value, passed: e.kind === "not_mentions", reason: "Ollie wrote no answer." };
        judged = true;
        const hit = says(plain, value);
        const ok = e.kind === "mentions" ? hit : !hit;
        return { kind: e.kind, value, passed: ok, reason: verdict(ok, "The answer meets this.", "The answer does not meet this.") };
      }
    }
  });
  // The judge runs once per test, on the fast model.
  if (judged) usd += meter ? spend(ws, "playground", db.meId, effectiveModels(ws).fast) : typicalUsd("playground", effectiveModels(ws).fast);
  return {
    test_id: t.id,
    status: checks.every((c) => c.passed) ? "passed" : "failed",
    answer_html: text ? markdownToHtml(text) : "",
    handoff: p.handoff,
    tools: toolsUsed,
    sources: p.sources,
    procedures: p.procedures,
    action: p.action,
    checks,
    error: null,
    usd: roundUsd(usd),
  };
}

// ─── Runs ──────────────────────────────────────────────────────────────────

function pending(run: StoredRun, testId: string): AgentTestResult {
  return { run_id: run.id, test_id: testId, status: "pending", answer_html: "", handoff: false, tools: [], sources: [], procedures: [], action: null, checks: [], error: null, usd: 0, draft: run.draft, previous_status: null, finished_at: null };
}

function tally(s: State, run: StoredRun) {
  const rs = s.results.get(run.id) ?? [];
  run.passed = rs.filter((r) => r.status === "passed").length;
  run.failed = rs.filter((r) => r.status === "failed").length;
  run.errored = rs.filter((r) => r.status === "error").length;
  run.usd = fromMicro(rs.reduce((n, r) => n + toMicro(r.usd), 0));
}

function finish(ws: WsData, run: StoredRun, status: "done" | "failed" | "cancelled", error: string | null = null) {
  const s = state(ws);
  const timer = s.timers.get(run.id);
  if (timer) clearTimeout(timer);
  s.timers.delete(run.id);
  run.status = status;
  run.error = error;
  run.finished_at = nowIso();
  tally(s, run);
  publish(ws, "ai_agent_test_run.updated", { run: runOut(run) });
}

/** Answers the run's next pending test, then schedules the one after. */
function step(ws: WsData, run: StoredRun) {
  const s = state(ws);
  if (run.status !== "running") return;
  const rs = s.results.get(run.id) ?? [];
  const i = rs.findIndex((r) => r.status === "pending");
  if (i < 0) return finish(ws, run, "done");
  const t = s.tests.find((x) => x.id === rs[i].test_id);
  let res: AgentTestResult;
  if (!t) {
    res = { ...rs[i], status: "error", error: "This test was deleted.", finished_at: nowIso() };
  } else {
    try {
      reserve(ws, "playground");
    } catch (e) {
      if (e instanceof HttpError && e.code === "ai_balance_empty") return finish(ws, run, "failed", "The AI balance is too low to finish the run.");
      throw e;
    }
    res = { ...rs[i], ...answerTest(ws, t, run.settings), finished_at: nowIso() };
  }
  rs[i] = res;
  tally(s, run);
  publish(ws, "ai_agent_test_result.updated", { result: withPrevious(s, res) });
  publish(ws, "ai_agent_test_run.updated", { run: runOut(run) });
  s.timers.set(run.id, setTimeout(() => step(ws, run), STEP_MS));
}

function startRun(ws: WsData, b: Record<string, unknown>): StoredRun {
  const s = state(ws);
  let settings: Overrides | null = null;
  if (isRecord(b.settings)) {
    settings = b.settings as Overrides;
    if (settings.widget_model !== undefined && settings.widget_model !== "" && !offersModel(ws, settings.widget_model)) throw invalid("settings.widget_model must be empty or one of the offered models");
    checkTrialBot(ws, settings);
  }
  if (s.runs.some((r) => r.status === "queued" || r.status === "running")) throw new HttpError(409, "run_in_progress", "Tests are already running. Wait for that run to finish or stop it.");
  let ids: string[];
  if (Array.isArray(b.test_ids) && b.test_ids.length) {
    ids = [...new Set(b.test_ids.filter((x): x is string => typeof x === "string"))];
    if (ids.some((id) => !s.tests.some((t) => t.id === id))) throw notFound("Test");
  } else {
    if (!s.tests.length) throw new HttpError(422, "no_tests", "Add a test first.");
    ids = s.tests.map((t) => t.id);
  }
  const run: StoredRun = {
    id: uuid(),
    status: "queued",
    draft: !!settings,
    test_ids: ids,
    total: ids.length,
    passed: 0,
    failed: 0,
    errored: 0,
    usd: 0,
    error: null,
    created_at: nowIso(),
    started_at: null,
    finished_at: null,
    settings,
  };
  s.runs.unshift(run);
  s.results.set(
    run.id,
    ids.map((id) => pending(run, id)),
  );
  audit(ws, "ai.agent_test_run_started", "ai_agent_test_run", run.id, { tests: ids.length, draft: run.draft });
  publish(ws, "ai_agent_test_run.created", { run: runOut(run) });
  s.timers.set(
    run.id,
    setTimeout(() => {
      run.status = "running";
      run.started_at = nowIso();
      publish(ws, "ai_agent_test_run.updated", { run: runOut(run) });
      s.timers.set(run.id, setTimeout(() => step(ws, run), STEP_MS));
    }, 300),
  );
  return run;
}

// ─── Review queue ──────────────────────────────────────────────────────────

export function reviewQueue(ws: WsData): AgentReviewItem[] {
  const s = state(ws);
  const since = Date.now() - 30 * DAY;
  const lastAi = new Map<string, string>();
  for (const m of ws.messages) {
    if (!m.ai) continue;
    const prev = lastAi.get(m.ticket_id);
    const pm = prev ? ws.messages.find((x) => x.id === prev) : undefined;
    if (!pm || pm.created_at < m.created_at) lastAi.set(m.ticket_id, m.id);
  }
  const out: AgentReviewItem[] = [];
  for (const m of ws.messages) {
    if (!m.ai || s.reviewed.has(m.id) || Date.parse(m.created_at) < since) continue;
    const t = ws.tickets.find((x) => x.id === m.ticket_id);
    if (!t || ws.deletedTickets.has(t.id)) continue;
    const handoff = t.custom?.ai_handoff === true && lastAi.get(t.id) === m.id;
    if (m.ai_rating !== "down" && !handoff) continue;
    const asked = ws.messages
      .filter((x) => x.ticket_id === t.id && x.direction === "inbound" && x.created_at < m.created_at)
      .sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
    out.push({
      id: m.id,
      ticket_id: t.id,
      ticket_number: t.number,
      reason: m.ai_rating === "down" ? "rated_down" : "handoff",
      question: asked ? (asked.body_text || stripHtml(asked.body_html)).slice(0, 500) : "",
      answer: (m.body_text || stripHtml(m.body_html)).slice(0, 1000),
      contact_name: t.contact?.name ?? "",
      created_at: m.created_at,
    });
  }
  return out.sort((a, b) => b.created_at.localeCompare(a.created_at)).slice(0, 50);
}

function agentReply(ws: WsData, id: string) {
  const m = ws.messages.find((x) => x.id === id && x.ai);
  if (!m) throw notFound("Agent reply");
  return m;
}

function markReviewed(ws: WsData, id: string, outcome: "dismissed" | "improved") {
  state(ws).reviewed.add(id);
  publish(ws, "ai_agent_review.updated", { message_id: id, outcome });
}

// ─── Seed ──────────────────────────────────────────────────────────────────

/** Margin's tests and two runs: the latest has a test that passed the run before. */
function seed(ws: WsData, s: State) {
  const at = (ms: number) => ago(ms);
  const add = (name: string, messages: PlaygroundMessage[], expectations: AgentExpectation[], visitor: PlaygroundVisitor | null = null, created = 6 * DAY) =>
    s.tests.push({ id: uuid(), name, messages, expectations, visitor, created_at: at(created), updated_at: at(created) });
  add("Refund timing", [{ role: "visitor", text: "How long does a refund take?" }], [{ kind: "mentions", value: "5 to 10 business days" }, { kind: "cites_knowledge" }, { kind: "no_handoff" }]);
  add("Student discount", [{ role: "visitor", text: "Do you offer student discounts?" }], [{ kind: "mentions", value: "50% off the Team plan" }, { kind: "not_mentions", value: "lifetime plan" }]);
  add(
    "Order status for a signed-in customer",
    [{ role: "visitor", text: "Where is my order #4821?" }],
    [{ kind: "uses_tool", value: "shop__lookup_order" }, { kind: "no_handoff" }],
    { email: "freya@nilsen.no", verified: true },
  );
  add("Asks for a person", [{ role: "visitor", text: "Can I talk to a real person?" }], [{ kind: "handoff" }], { email: "hugo@moreau.fr", verified: false });
  add("PayPal", [{ role: "visitor", text: "Can I pay with PayPal?" }], [{ kind: "mentions", value: "PayPal is accepted at checkout" }, { kind: "no_handoff" }], null, 3 * DAY);

  const mkRun = (created: number): StoredRun => ({
    id: uuid(),
    status: "done",
    draft: false,
    test_ids: s.tests.map((t) => t.id),
    total: s.tests.length,
    passed: 0,
    failed: 0,
    errored: 0,
    usd: 0,
    error: null,
    created_at: at(created),
    started_at: at(created),
    finished_at: at(created - MIN),
    settings: null,
  });
  // Two days ago every test passed (the PayPal answer was in the help center then).
  const older = mkRun(2 * DAY);
  s.runs.push(older);
  s.results.set(
    older.id,
    s.tests.map((t) => ({ ...pending(older, t.id), status: "passed" as const, answer_html: "<p>…</p>", checks: t.expectations.map((e) => ({ kind: e.kind, value: e.value ?? "", passed: true, reason: "The answer meets this." })), usd: 0.021, finished_at: older.finished_at })),
  );
  tally(s, older);
  // Yesterday's run, answered for real without metering.
  const latest = mkRun(20 * HOUR);
  s.runs.unshift(latest);
  s.results.set(
    latest.id,
    s.tests.map((t) => ({ ...pending(latest, t.id), ...answerTest(ws, t, null, false), finished_at: latest.finished_at })),
  );
  tally(s, latest);
}

// ─── Routes ────────────────────────────────────────────────────────────────

export function registerAgentQuality() {
  const admin = (ctx: Ctx, read = false) => {
    assertAi(ctx.ws);
    requireAiAdmin(ctx.ws, read ? "Only admins can see the agent's tests" : "Only admins can change the agent's tests");
    return state(ctx.ws);
  };

  get("/v1/ai/agent/tests", (ctx) => {
    const s = admin(ctx, true);
    return { data: s.tests.map((t) => testOut(s, t)) };
  });

  post("/v1/ai/agent/tests", (ctx) => {
    const s = admin(ctx);
    return respond(201, testOut(s, createTest(ctx.ws, ctx.body)));
  });

  get("/v1/ai/agent/tests/:id", (ctx) => {
    const s = admin(ctx, true);
    return testOut(s, findTest(ctx));
  });

  patch("/v1/ai/agent/tests/:id", (ctx) => {
    const s = admin(ctx);
    const t = findTest(ctx);
    const b = ctx.body;
    const messages = "messages" in b ? cleanMessages(b.messages) : t.messages;
    const next: StoredTest = {
      ...t,
      messages,
      name: "name" in b ? cleanName(b.name, messages) : t.name,
      expectations: "expectations" in b ? cleanExpectations(ctx.ws, b.expectations) : t.expectations,
      visitor: "visitor" in b ? cleanVisitor(b.visitor) : t.visitor,
      updated_at: nowIso(),
    };
    Object.assign(t, next);
    audit(ctx.ws, "ai.agent_test_updated", "ai_agent_test", t.id, { name: t.name });
    const out = testOut(s, t);
    publish(ctx.ws, "ai_agent_test.updated", { test: out });
    return out;
  });

  del("/v1/ai/agent/tests/:id", (ctx) => {
    const s = admin(ctx);
    const t = findTest(ctx);
    s.tests = s.tests.filter((x) => x.id !== t.id);
    for (const [id, rs] of s.results) s.results.set(id, rs.filter((r) => r.test_id !== t.id || r.status === "pending"));
    audit(ctx.ws, "ai.agent_test_deleted", "ai_agent_test", t.id, { name: t.name });
    publish(ctx.ws, "ai_agent_test.deleted", { test_id: t.id });
    return respond(204);
  });

  post("/v1/ai/agent/test-runs", (ctx) => {
    admin(ctx);
    return respond(202, runOut(startRun(ctx.ws, ctx.body)));
  });

  get("/v1/ai/agent/test-runs", (ctx) => {
    const s = admin(ctx, true);
    return { data: s.runs.slice(0, 20).map(runOut) };
  });

  get("/v1/ai/agent/test-runs/:id", (ctx) => {
    const s = admin(ctx, true);
    const run = runOf(s, ctx.params.id);
    if (!run) throw notFound("Test run");
    return { ...runOut(run), results: (s.results.get(run.id) ?? []).map((r) => withPrevious(s, r)) };
  });

  post("/v1/ai/agent/test-runs/:id/cancel", (ctx) => {
    const s = admin(ctx);
    const run = runOf(s, ctx.params.id);
    if (!run) throw notFound("Test run");
    if (run.status === "queued" || run.status === "running") finish(ctx.ws, run, "cancelled");
    return runOut(run);
  });

  post("/v1/ai/agent/improve", (ctx) => {
    const s = admin(ctx);
    const b = ctx.body;
    const question = typeof b.question === "string" ? b.question.replace(/\s+/g, " ").trim() : "";
    const answer = typeof b.answer === "string" ? b.answer.trim() : "";
    if (question.length < 3 || question.length > 4000) throw invalid("question must be 3 to 4,000 characters");
    if (!answer || answer.length > 4000) throw invalid("answer must be 1 to 4,000 characters");
    if (b.save_test === true && s.tests.length >= MAX_TESTS) throw new HttpError(422, "limit_reached", `A workspace can have at most ${MAX_TESTS} tests`);
    const messageId = typeof b.message_id === "string" ? b.message_id : null;
    if (messageId) agentReply(ctx.ws, messageId);
    const cut = question.length > 300 ? `${question.slice(0, 300).replace(/\s+\S*$/, "")}` : question;
    const source = addSnippet(ctx.ws, cut, answer);
    const test =
      b.save_test === true
        ? createTest(ctx.ws, { messages: [{ role: "visitor", text: question }], expectations: [{ kind: "mentions", value: stripHtml(markdownToHtml(answer)).replace(/\s+/g, " ").trim().slice(0, EXPECTATION_MAX) }] })
        : null;
    if (messageId) markReviewed(ctx.ws, messageId, "improved");
    audit(ctx.ws, "ai.agent_answer_improved", "knowledge_source", source.id, { test_id: test?.id ?? null, message_id: messageId });
    return respond(201, { source: { id: source.id, kind: "snippet", name: source.name }, test: test ? testOut(s, test) : null });
  });

  get("/v1/ai/agent/review", (ctx) => {
    admin(ctx, true);
    return { data: reviewQueue(ctx.ws) };
  });

  post("/v1/ai/agent/review/:id/dismiss", (ctx) => {
    admin(ctx);
    agentReply(ctx.ws, ctx.params.id);
    markReviewed(ctx.ws, ctx.params.id, "dismissed");
    return respond(204);
  });
}
