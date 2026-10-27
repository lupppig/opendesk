import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, StrictMode, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import { ConfirmProvider } from "@/components/ui/confirm";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { Role, WikiPage } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { flushAll, openSaver, resetSavers, SAVE_DELAY } from "@/lib/wiki/saver";
import { PageScreen } from "./WikiPageView";

vi.mock("@tanstack/react-router", () => ({
  getRouteApi: () => ({ useParams: () => ({ pageId: "p1" }) }),
  Link: ({ children, className }: { children: ReactNode; className?: string }) => <a className={className}>{children}</a>,
  useNavigate: () => () => {},
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) => select({ location: { pathname: "/wiki/p1" } }),
}));
vi.mock("@/app/RootLayout", () => ({ useDocumentTitle: () => {} }));
vi.mock("@/app/useBreadcrumbs", () => ({ usePageCrumb: () => {} }));
let restore: ((p: WikiPage) => void) | undefined;
vi.mock("@/components/wiki/HistoryDrawer", () => ({
  HistoryDrawer: ({ onRestored }: { onRestored: (p: WikiPage) => void }) => {
    restore = onRestored;
    return null;
  },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const page = (over: Partial<WikiPage> = {}): WikiPage => ({
  id: "p1",
  parent_id: null,
  title: "Refunds",
  icon: "",
  body_html: "<p>Refund within 30 days.</p>",
  position: 0,
  version: 1,
  updated_at: "2026-10-01T00:00:00Z",
  created_by_id: "other",
  created_at: "2026-10-01T00:00:00Z",
  created_by: null,
  updated_by: null,
  breadcrumbs: [],
  children: [],
  ...over,
});

// As the API stores it: sanitized, so the editor's own HTML for it differs.
const theirs = page({ version: 2, body_html: `<p>It&#39;s <a href="https://example.com/x" rel="nofollow noopener" target="_blank">40 days</a> now.</p>` });

const wait = (ms: number) => act(() => new Promise((r) => setTimeout(r, ms)));

let host: HTMLDivElement;
let root: Root;
let qc: QueryClient;

async function open(role: Role, shown = page()) {
  useSession.setState({
    workspaceId: "w1",
    user: { id: "me" } as never,
    memberships: [{ workspace: { id: "w1" } as never, role }],
  });
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(qk.wikiPage("p1"), shown);
  qc.setQueryData(qk.wikiTree, []);
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () =>
    root.render(
      // As the app renders: strict mode runs memos twice, which must not leave two savers.
      <StrictMode>
        <QueryClientProvider client={qc}>
          <TooltipProvider>
            <ConfirmProvider>
              <PageScreen page={shown} />
            </ConfirmProvider>
          </TooltipProvider>
        </QueryClientProvider>
      </StrictMode>,
    ),
  );
}

const body = () => host.querySelector(".tiptap")?.textContent;

describe("wiki page view", () => {
  beforeEach(() => {
    vi.spyOn(api.wiki, "update").mockImplementation(async (_id, b) => ({ ...page(), ...b, version: 9 }) as WikiPage);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
    resetSavers();
  });

  it.each(["viewer", "agent"] as const)("shows someone else's change to a %s without saving anything", async (role) => {
    await open(role);
    await act(async () => qc.setQueryData(qk.wikiPage("p1"), theirs));
    await wait(SAVE_DELAY + 300);
    expect(body()).toBe("It's 40 days now.");
    expect(openSaver("p1")?.version).toBe(2);
    expect(api.wiki.update).not.toHaveBeenCalled();
    expect(flushAll()).toBe(false);
  });

  it("opens a page as an agent, and switches between roles, without saving it", async () => {
    await open("agent", theirs);
    await wait(SAVE_DELAY + 300);
    expect(body()).toBe("It's 40 days now.");
    await act(async () => useSession.setState((st) => ({ memberships: st.memberships.map((m) => ({ ...m, role: "viewer" as const })) })));
    await act(async () => useSession.setState((st) => ({ memberships: st.memberships.map((m) => ({ ...m, role: "agent" as const })) })));
    await wait(SAVE_DELAY + 300);
    expect(api.wiki.update).not.toHaveBeenCalled();
    expect(host.querySelector("[role=alert]")).toBeNull();
  });

  it("shows a restored version without saving it again", async () => {
    await open("agent");
    await act(async () => restore?.(page({ version: 3, body_html: "<p>Back to <b>this</b>, it&#39;s fine.</p>" })));
    await wait(SAVE_DELAY + 300);
    expect(body()).toBe("Back to this, it's fine.");
    expect(api.wiki.update).not.toHaveBeenCalled();
  });

  it("says when the person can't edit the page, and offers what they typed", async () => {
    vi.mocked(api.wiki.update).mockRejectedValue(new ApiError({ status: 403, code: "forbidden", title: "Forbidden" }));
    await open("agent");
    const title = host.querySelector("textarea")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(title, "Refunds and returns");
      title.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await wait(SAVE_DELAY + 300);
    expect(host.querySelector("[role=alert]")?.textContent).toContain("You can't edit this page.");
    expect(host.textContent).toContain("Copy my text");
    expect(api.wiki.update).toHaveBeenCalledOnce();
  });
});
