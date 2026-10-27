import { describe, expect, it } from "vitest";
import {
  actionFitsTrigger,
  actionLabel,
  actionNeedsValue,
  aiActions,
  fieldLabel,
  isPhraseOp,
  mergeWindows,
  newAction,
  offered,
  opLabel,
  opsFor,
  phrases,
  sentimentLabel,
  triggerLabel,
  validMergeWindow,
  validPhrases,
} from "./automation";

describe("automation AI helpers", () => {
  it("offers AI actions only when AI is on, but keeps one in use", () => {
    const keys = ["add_tag", "ai_triage"] as const;
    expect(offered([...keys], aiActions, false)).toEqual(["add_tag"]);
    expect(offered([...keys], aiActions, true)).toEqual(["add_tag", "ai_triage"]);
    expect(offered([...keys], aiActions, false, "ai_triage")).toEqual(["add_tag", "ai_triage"]);
  });

  it("offers is / is not for sentiment and needs no value for triage", () => {
    expect(opsFor("sentiment")).toEqual(["equals", "not_equals"]);
    expect(actionNeedsValue("ai_triage")).toBe(false);
    expect(actionNeedsValue("set_field")).toBe(false);
    expect(actionNeedsValue("delete_ticket")).toBe(false);
    expect(sentimentLabel("frustrated")).toBe("Frustrated");
    expect(sentimentLabel("custom")).toBe("custom");
  });
});

describe("automation labels", () => {
  it("reads every label from the catalogs", () => {
    expect(triggerLabel.ticket_created).toBe("A ticket is created");
    expect(opLabel.not_contains).toBe("doesn't contain");
    expect(actionLabel.add_tag).toBe("Add label");
    expect(actionLabel.remove_tag).toBe("Remove label");
    expect(fieldLabel.tag).toBe("Label");
    expect(fieldLabel.from_email).toBe("Sender email");
    expect(Object.keys(fieldLabel)).toContain("sentiment");
    expect(Object.keys(actionLabel).at(-1)).toBe("ai_triage");
    expect(actionLabel.set_field).toBe("Set field");
    expect(actionLabel.delete_ticket).toBe("Delete ticket");
    expect(opLabel.any).toBe("contains any of");
    expect(opLabel.none).toBe("contains none of");
  });
});

describe("phrase lists", () => {
  it("offers contains any of and none of on text fields and the address written to only", () => {
    for (const f of ["subject", "body", "from_email", "received_at"] as const) expect(opsFor(f)).toEqual(expect.arrayContaining(["any", "none"]));
    for (const f of ["status", "channel", "tag", "sentiment", "ai_decide"] as const) expect(opsFor(f)).not.toContain("any");
    expect(isPhraseOp("none")).toBe(true);
    expect(isPhraseOp("not_contains")).toBe(false);
  });

  it("reads one phrase per line, trimmed, without empty lines", () => {
    expect(phrases("  Your order has shipped \r\n\n\tnewsletter  \n   \ninvoice ")).toEqual(["Your order has shipped", "newsletter", "invoice"]);
    expect(phrases(" \n ")).toEqual([]);
  });

  it("takes 1 to 100 phrases of up to 500 characters, 5000 in all", () => {
    expect(validPhrases("refund")).toBe(true);
    expect(validPhrases(" \n \n ")).toBe(false);
    expect(validPhrases(Array.from({ length: 100 }, (_, i) => `p${i}`).join("\n"))).toBe(true);
    expect(validPhrases(Array.from({ length: 101 }, (_, i) => `p${i}`).join("\n"))).toBe(false);
    expect(validPhrases("x".repeat(500))).toBe(true);
    expect(validPhrases("x".repeat(501))).toBe(false);
    expect(validPhrases(Array.from({ length: 11 }, () => "x".repeat(500)).join("\n"))).toBe(false);
  });
});

describe("merge into the contact's open ticket", () => {
  it("starts at 7 days with the same subject, and runs only when a ticket is created", () => {
    expect(actionLabel.merge_into_open_ticket).toBe("Merge into the contact's open ticket");
    expect(newAction("merge_into_open_ticket")).toEqual({ type: "merge_into_open_ticket", value: "7", same_subject: true });
    expect(newAction("add_tag")).toEqual({ type: "add_tag", value: "" });
    expect(actionFitsTrigger("merge_into_open_ticket", "ticket_created")).toBe(true);
    for (const t of ["customer_replied", "agent_replied", "ticket_updated"] as const) {
      expect(actionFitsTrigger("merge_into_open_ticket", t)).toBe(false);
      expect(actionFitsTrigger("add_tag", t)).toBe(true);
    }
  });

  it("takes a window of 1 to 30 whole days, and offers one saved outside the choices", () => {
    for (const v of ["1", "7", "30"]) expect(validMergeWindow(v)).toBe(true);
    for (const v of ["", "0", "31", "1.5", "-2", " 7"]) expect(validMergeWindow(v)).toBe(false);
    expect(mergeWindows("7")).toEqual([1, 2, 3, 7, 14, 30]);
    expect(mergeWindows("10")).toEqual([1, 2, 3, 7, 10, 14, 30]);
    expect(mergeWindows("99")).toEqual([1, 2, 3, 7, 14, 30]);
  });
});
