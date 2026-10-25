import { describe, expect, it } from "vitest";
import { variableText } from "./composerTemplates";

describe("variableText", () => {
  it("writes the ticket's value as plain text", () => {
    expect(variableText("contact.first_name", "Ada")).toBe("Ada");
  });
  it("writes the tag when there is no value yet, so the server fills it on send", () => {
    expect(variableText("contact.first_name", undefined)).toBe("{{ contact.first_name }}");
    expect(variableText("contact.first_name", "")).toBe("{{ contact.first_name }}");
    expect(variableText("contact.first_name", "  ")).toBe("{{ contact.first_name }}");
  });
  it("writes the tag for HTML values", () => {
    expect(variableText("agent.signature", "<p>Ada</p>", true)).toBe("{{ agent.signature }}");
  });
  it("never writes a value that would be read as a template on send", () => {
    expect(variableText("contact.name", "{{ agent.phone }}")).toBe("{{ contact.name }}");
    expect(variableText("contact.name", "{% if x %}")).toBe("{{ contact.name }}");
  });
});
