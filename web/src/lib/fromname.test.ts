import { describe, expect, it } from "vitest";
import { COLLAB_FROM_VARS, fillFromName, fromNameProblem, hasFromVars, HELPDESK_FROM_VARS } from "./fromname";

// The same cases as internal/fromname's tests: one vocabulary, filled alike in the app and on the server.
describe("From name formats", () => {
  const n = {
    agent: "Maya Chen",
    team: "Billing",
    brand: "Margin",
    mailbox: "Margin Support",
    workspace: "Margin",
  };
  const none = { ...n, agent: "" };

  it("fills Collab formats", () => {
    expect(fillFromName("{agent} at Margin Support", n, COLLAB_FROM_VARS)).toBe("Maya Chen at Margin Support");
    expect(fillFromName("{agent} at {mailbox}", n, COLLAB_FROM_VARS)).toBe("Maya Chen at Margin Support");
    expect(fillFromName("{agent_first} from {workspace}", n, COLLAB_FROM_VARS)).toBe("Maya from Margin");
    expect(fillFromName("{agent} at {mailbox}", none, COLLAB_FROM_VARS)).toBe("Margin Support");
    expect(fillFromName("{agent_first} from {workspace}", none, COLLAB_FROM_VARS)).toBe("Margin");
    expect(fillFromName("{agent}", none, COLLAB_FROM_VARS)).toBe("Margin Support");
    expect(fillFromName("{workspace} - {agent}", none, COLLAB_FROM_VARS)).toBe("Margin");
    expect(fillFromName("{agent} {team}", n, COLLAB_FROM_VARS)).toBe("Maya Chen {team}");
  });

  it("fills helpdesk formats as before", () => {
    expect(fillFromName("{agent_first} from {team}", n, HELPDESK_FROM_VARS)).toBe("Maya from Billing");
    expect(fillFromName("{agent_first} from {team}", { ...none, team: "" }, HELPDESK_FROM_VARS)).toBe("Margin");
    expect(fillFromName("{brand} support", n, HELPDESK_FROM_VARS)).toBe("Margin support");
    expect(fillFromName("Support desk", n, HELPDESK_FROM_VARS)).toBe("Support desk");
    expect(fillFromName("{workspace}", n, HELPDESK_FROM_VARS)).toBe("{workspace}");
  });

  it("checks formats like the server", () => {
    expect(fromNameProblem("{agent} at Margin", COLLAB_FROM_VARS)).toBeNull();
    expect(fromNameProblem("{agent} for {team}", COLLAB_FROM_VARS)).toBe("variable");
    expect(fromNameProblem("{agent", COLLAB_FROM_VARS)).toBe("variable");
    expect(fromNameProblem("a <b>", COLLAB_FROM_VARS)).toBe("chars");
    expect(fromNameProblem("a".repeat(81), COLLAB_FROM_VARS)).toBe("long");
    expect(fromNameProblem("{team}", HELPDESK_FROM_VARS)).toBeNull();
    expect(hasFromVars("{agent} at X")).toBe(true);
    expect(hasFromVars("Margin {support}")).toBe(false);
  });
});
