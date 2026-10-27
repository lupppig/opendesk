// Clickable references in Ollie's answers: tickets (#123), contacts, articles,
// wiki pages, views and automations, opened in the app.

import { router } from "@/router";
import { api } from "@/lib/api/endpoints";
import type { AssistantRef } from "./api";

export function currentFolder(): string {
  const m = location.pathname.match(/^\/inbox\/([^/]+)/);
  return m ? m[1] : "open";
}

export async function openTicket(id: string | undefined, number?: number) {
  let ticketId = id;
  if (!ticketId && number) {
    try {
      ticketId = (await api.tickets.get(`#${number}`)).id;
    } catch {
      return;
    }
  }
  if (!ticketId) return;
  const view = location.pathname.match(/^\/views\/([^/]+)/)?.[1];
  if (view) void router.navigate({ to: "/views/$viewId/{-$ticketId}", params: { viewId: view, ticketId } });
  else void router.navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: currentFolder(), ticketId } });
}

export function openRef(r: AssistantRef) {
  switch (r.kind) {
    case "ticket":
      return openTicket(r.id, r.number);
    case "contact":
      return router.navigate({ to: "/contacts/{-$contactId}", params: { contactId: r.id } });
    case "article":
      return router.navigate({ to: "/kb/articles/$articleId", params: { articleId: r.id } });
    case "wiki_page":
      return router.navigate({ to: "/wiki/$pageId", params: { pageId: r.id } });
    case "view":
      return router.navigate({ to: "/views/$viewId/{-$ticketId}", params: { viewId: r.id, ticketId: undefined } });
    case "automation":
      return router.navigate({ to: "/settings/automations/$automationId", params: { automationId: r.id } });
  }
}
