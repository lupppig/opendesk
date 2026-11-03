import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { Requester, requesterLabel } from "./requester";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("requesterLabel", () => {
  it("shows the contact's name", () => {
    expect(requesterLabel({ name: "Mónica Guerra", email: "monica@example.com" })).toBe("Mónica Guerra");
  });

  it("shows the address when the name is only its local part, whatever the case", () => {
    expect(requesterLabel({ name: "service", email: "service@paypal.be" })).toBe("service@paypal.be");
    expect(requesterLabel({ name: "Service", email: "SERVICE@paypal.be" })).toBe("SERVICE@paypal.be");
    expect(requesterLabel({ name: " service ", email: "service@paypal.be" })).toBe("service@paypal.be");
  });

  it("shows the address when the name is empty or the address itself", () => {
    expect(requesterLabel({ name: "", email: "service@paypal.be" })).toBe("service@paypal.be");
    expect(requesterLabel({ name: "   ", email: "service@paypal.be" })).toBe("service@paypal.be");
    expect(requesterLabel({ name: "Service@PayPal.be", email: "service@paypal.be" })).toBe("service@paypal.be");
  });

  it("keeps a name that only starts like the address", () => {
    expect(requesterLabel({ name: "servicedesk", email: "service@paypal.be" })).toBe("servicedesk");
    expect(requesterLabel({ name: "PayPal", email: "service@paypal.be" })).toBe("PayPal");
  });

  it("shows the name of a contact without an address, and nothing without a contact", () => {
    expect(requesterLabel({ name: "Visitor 42", email: null })).toBe("Visitor 42");
    expect(requesterLabel({ name: "", email: null })).toBe("");
    expect(requesterLabel(null)).toBe("");
    expect(requesterLabel(undefined)).toBe("");
  });
});

describe("Requester", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  const cell = () => host.querySelector("span")!;

  it("renders the label with the address on hover", async () => {
    await act(async () => root.render(<Requester contact={{ name: "service", email: "service@paypal.be" }} />));
    expect(cell().textContent).toBe("service@paypal.be");
    expect(cell().title).toBe("service@paypal.be");
    expect(cell().className).toContain("truncate");
  });

  it("renders the name when it says more than the address", async () => {
    await act(async () => root.render(<Requester contact={{ name: "Mónica Guerra", email: "monica@example.com" }} />));
    expect(cell().textContent).toBe("Mónica Guerra");
    expect(cell().title).toBe("monica@example.com");
  });

  it("keeps its column without a contact", async () => {
    await act(async () => root.render(<Requester contact={null} />));
    expect(cell().textContent).toBe("");
    expect(cell().hasAttribute("title")).toBe(false);
    expect(cell().className).toContain("shrink-0");
  });
});
