import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { MailThreadDetail } from "../api/types";
import { qk } from "../queries";
import { applyCollabEvent } from "./cache";

const detail = (accountId: string) => ({ account: { id: accountId } }) as unknown as MailThreadDetail;

describe("collab.accounts", () => {
  it("refreshes the open conversations of the account that changed, with its addresses", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.mailThread("t1"), detail("a1"));
    qc.setQueryData(qk.mailThread("t2"), detail("a2"));
    qc.setQueryData(qk.mailAccounts, []);
    expect(applyCollabEvent(qc, "collab.accounts", { account_id: "a1" })).toBe(true);
    expect(qc.getQueryState(qk.mailThread("t1"))?.isInvalidated).toBe(true);
    expect(qc.getQueryState(qk.mailThread("t2"))?.isInvalidated).toBe(false);
    expect(qc.getQueryState(qk.mailAccounts)?.isInvalidated).toBe(true);
  });

  it("refreshes every open conversation when it names no account", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.mailThread("t1"), detail("a1"));
    qc.setQueryData(qk.mailThread("t2"), detail("a2"));
    applyCollabEvent(qc, "collab.accounts", {});
    expect(qc.getQueryState(qk.mailThread("t1"))?.isInvalidated).toBe(true);
    expect(qc.getQueryState(qk.mailThread("t2"))?.isInvalidated).toBe(true);
  });
});
