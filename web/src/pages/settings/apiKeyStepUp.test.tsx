import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import { ApiError } from "@/lib/api/client";
import type { ApiKey } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { CreateKeyDialog as AiClientKeyDialog } from "./AiClients";
import { CreateKeyDialog } from "./ApiKeys";

const create = vi.fn<(b: unknown) => Promise<ApiKey & { secret: string }>>();
vi.mock("@/lib/api/endpoints", () => ({ api: { apiKeys: { create: (b: unknown) => create(b) } } }));
const navigate = vi.fn();
vi.mock("@tanstack/react-router", async (orig) => ({ ...(await orig<object>()), useNavigate: () => navigate }));
const signOut = vi.fn(async () => ({}));
vi.mock("@/lib/session", async (orig) => ({ ...(await orig<object>()), signOut: () => signOut() }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
// The scope checkboxes measure themselves; jsdom has no ResizeObserver.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

const refused = (code: string, detail: string) => new ApiError({ status: 403, code, title: "Forbidden", detail });
const MFA = refused("mfa_required", "this action requires two-factor authentication");
const REAUTH = refused("reauth_required", "For your security, sign in again to do this. Your last sign-in is more than 12 hours old.");

let root: Root;

beforeAll(() => loadAgentCatalogs("fr"));
beforeEach(() => {
  create.mockReset();
  navigate.mockReset();
  signOut.mockClear();
  useSession.setState({ user: { id: "me", email: "owner@example.com" } as never, workspaceId: "w1" });
});
afterEach(async () => {
  await act(async () => root.unmount());
  await i18n.changeLanguage("en");
  document.body.innerHTML = "";
});

async function render(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(qk.teams, []);
  const host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => root.render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>));
}

// TanStack Query hands a mutation's result to React on a timer: let it land.
const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));
const button = (label: string) => [...document.querySelectorAll("button")].find((b) => b.textContent === label)!;
const alert = () => document.querySelector<HTMLElement>('[role="alert"]');

async function type(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function submitApiKey(createLabel = "Create key") {
  await render(<CreateKeyDialog onClose={() => {}} onCreated={() => {}} />);
  await type(document.querySelector<HTMLInputElement>("input[placeholder]")!, "CI deploys");
  await act(async () => button(createLabel).click());
  await settle();
}

describe("New API key", () => {
  it("asks a session that did not pass 2FA to sign in again with its code", async () => {
    create.mockRejectedValue(MFA);
    await submitApiKey();
    expect(create).toHaveBeenCalledTimes(1);
    expect(alert()?.textContent).toContain("Sign in again with your two-factor code to do this.");
    expect(document.body.textContent).not.toContain("this action requires two-factor authentication");
  });

  it("asks an old session to sign in again, and says why", async () => {
    create.mockRejectedValue(REAUTH);
    await submitApiKey();
    expect(alert()?.textContent).toContain("This needs a sign-in from the last 12 hours. Sign in again to confirm it's you.");
    expect(document.body.textContent).not.toContain("For your security, sign in again to do this. Your last sign-in is more than 12 hours old.");
  });

  it("signs this account out and comes back to the same page after signing in", async () => {
    window.history.replaceState(null, "", "/settings/api-keys?x=1");
    create.mockRejectedValue(REAUTH);
    await submitApiKey();
    await act(async () => button("Sign in again").click());
    expect(signOut).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith({ to: "/login", search: { next: "/settings/api-keys?x=1", email: "owner@example.com", add: "1" } });
  });

  it("keeps other errors on one line in the footer", async () => {
    create.mockRejectedValue(new ApiError({ status: 422, detail: "expires_at must be in the future." }));
    await submitApiKey();
    expect(document.body.textContent).toContain("expires_at must be in the future.");
    expect(button("Sign in again")).toBeUndefined();
  });

  it("speaks French", async () => {
    await i18n.changeLanguage("fr");
    create.mockRejectedValue(MFA);
    await submitApiKey("Créer la clé");
    expect(alert()?.textContent).toContain("Reconnectez-vous avec votre code à deux facteurs pour effectuer cette action.");
    expect(button("Se reconnecter")).toBeDefined();
  });
});

describe("New key for an AI client", () => {
  it.each([
    ["mfa_required", MFA, "Sign in again with your two-factor code to do this."],
    ["reauth_required", REAUTH, "This needs a sign-in from the last 12 hours."],
  ])("shows the sign-in notice for %s", async (_code, error, text) => {
    create.mockRejectedValue(error);
    await render(<AiClientKeyDialog client="Claude" onClose={() => {}} onCreated={() => {}} />);
    await act(async () => button("Create key").click());
    await settle();
    expect(create).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).toContain(text);
    expect(button("Sign in again")).toBeDefined();
  });
});
