import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { AgentReviewItem, AgentTest, AgentTestResult, AgentTestRun } from "./api/types";
import { activeRun, applyAgentQualityEvent, newlyFailing, passRate, runProgress, testState, toolKey } from "./agentTests";
import { qk } from "./queries";

const result = (p: Partial<AgentTestResult>): AgentTestResult => ({
  run_id: "r1",
  test_id: "t1",
  status: "passed",
  answer_html: "",
  handoff: false,
  tools: [],
  sources: [],
  procedures: [],
  action: null,
  checks: [],
  error: null,
  usd: 0,
  draft: false,
  previous_status: null,
  finished_at: null,
  ...p,
});
const test = (id: string, last: Partial<AgentTestResult> | null = null): AgentTest => ({
  id,
  name: id,
  messages: [{ role: "visitor", text: "Hi" }],
  expectations: [{ kind: "no_handoff" }],
  visitor: null,
  last_result: last ? result({ test_id: id, ...last }) : null,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
});
const run = (p: Partial<AgentTestRun>): AgentTestRun => ({
  id: "r2",
  status: "running",
  draft: false,
  test_ids: [],
  total: 0,
  passed: 0,
  failed: 0,
  errored: 0,
  usd: 0,
  error: null,
  created_at: "2026-10-02T00:00:00Z",
  started_at: null,
  finished_at: null,
  ...p,
});

describe("agent tests", () => {
  it("summarises tests and runs", () => {
    const all = [test("a", { status: "passed" }), test("b", { status: "failed", previous_status: "passed" }), test("c")];
    expect(passRate(all)).toEqual({ passed: 1, ran: 2, rate: 0.5 });
    expect(passRate([test("c")]).rate).toBeNull();
    expect(newlyFailing(all[1].last_result)).toBe(true);
    expect(newlyFailing(result({ status: "failed", previous_status: "failed" }))).toBe(false);
    expect(newlyFailing(null)).toBe(false);
    const active = run({ test_ids: ["a", "c"], total: 4, passed: 1, failed: 1 });
    expect(activeRun([run({ id: "x", status: "done" }), active])).toBe(active);
    expect(activeRun([run({ status: "cancelled" })])).toBeNull();
    expect(runProgress(active)).toBe(0.5);
    expect(testState(all[0], active)).toBe("queued");
    expect(testState(all[1], active)).toBe("failed");
    expect(testState(all[2], null)).toBe("never");
    expect(toolKey("shop", "lookup order")).toBe("shop__lookup_order");
  });

  it("applies realtime events to the cache", () => {
    const qc = new QueryClient();
    qc.setQueryData<AgentTest[]>(qk.agentTests, [test("a"), test("b")]);
    qc.setQueryData<AgentTestRun[]>(qk.agentTestRuns, []);
    qc.setQueryData<AgentTestRun>(qk.agentTestRun("r2"), run({ results: [result({ run_id: "r2", test_id: "a", status: "pending" })] }));
    qc.setQueryData<AgentReviewItem[]>(qk.agentReview, [{ id: "m1", ticket_id: "t", ticket_number: 1, reason: "handoff", question: "", answer: "", contact_name: "", created_at: "" }]);

    applyAgentQualityEvent(qc, "ai_agent_test.created", { test: test("c") });
    expect(qc.getQueryData<AgentTest[]>(qk.agentTests)!.map((t) => t.id)).toEqual(["a", "b", "c"]);
    applyAgentQualityEvent(qc, "ai_agent_test.deleted", { test_id: "b" });
    expect(qc.getQueryData<AgentTest[]>(qk.agentTests)!.map((t) => t.id)).toEqual(["a", "c"]);

    applyAgentQualityEvent(qc, "ai_agent_test_run.created", { run: run({ status: "queued" }) });
    expect(qc.getQueryData<AgentTestRun[]>(qk.agentTestRuns)![0]).toMatchObject({ id: "r2", status: "queued" });
    applyAgentQualityEvent(qc, "ai_agent_test_result.updated", { result: result({ run_id: "r2", test_id: "a", status: "failed" }) });
    expect(qc.getQueryData<AgentTest[]>(qk.agentTests)![0].last_result?.status).toBe("failed");
    expect(qc.getQueryData<AgentTestRun>(qk.agentTestRun("r2"))!.results![0].status).toBe("failed");
    applyAgentQualityEvent(qc, "ai_agent_test_run.updated", { run: run({ status: "done", failed: 1, results: [] }) });
    expect(qc.getQueryData<AgentTestRun[]>(qk.agentTestRuns)).toHaveLength(1);
    expect(qc.getQueryData<AgentTestRun[]>(qk.agentTestRuns)![0]).not.toHaveProperty("results");
    expect(qc.getQueryData<AgentTestRun>(qk.agentTestRun("r2"))!).toMatchObject({ status: "done", failed: 1, results: [{ status: "failed" }] });

    applyAgentQualityEvent(qc, "ai_agent_review.updated", { message_id: "m1", outcome: "dismissed" });
    expect(qc.getQueryData<AgentReviewItem[]>(qk.agentReview)).toEqual([]);
  });
});
