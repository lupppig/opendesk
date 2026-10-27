// What Ollie knows about the page: read from the URL when a message is sent, so
// "summarize this" means the open ticket and "these" means the current view.

import type { PageContext } from "./api";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function contextFor(pathname: string, search = ""): PageContext {
  const parts = pathname.split("/").filter(Boolean);
  const ctx: PageContext = { path: pathname.slice(0, 300), page: "other" };
  const params = new URLSearchParams(search);
  const filters: Record<string, string> = {};
  for (const k of ["q", "priority", "channel", "tag", "mailbox", "sort"]) {
    const v = params.get(k);
    if (v) filters[k] = v.slice(0, 200);
  }
  if (Object.keys(filters).length) ctx.filters = filters;
  switch (parts[0]) {
    case "inbox":
      ctx.page = "inbox";
      ctx.folder = parts[1];
      if (parts[2] && UUID.test(parts[2])) {
        ctx.page = "ticket";
        ctx.ticket_id = parts[2];
      }
      break;
    case "views":
      ctx.page = "view";
      if (parts[1] && UUID.test(parts[1])) ctx.view_id = parts[1];
      if (parts[2] && UUID.test(parts[2])) {
        ctx.page = "ticket";
        ctx.ticket_id = parts[2];
      }
      break;
    case "contacts":
    case "companies":
      ctx.page = "contacts";
      break;
    case "kb":
      ctx.page = "kb";
      break;
    case "wiki":
      ctx.page = "wiki";
      if (parts[1] && UUID.test(parts[1])) ctx.wiki_page_id = parts[1];
      break;
    case "reports":
      ctx.page = "reports";
      break;
    case "settings":
      ctx.page = "settings";
      break;
  }
  return ctx;
}

export function pageContext(): PageContext {
  if (typeof location === "undefined") return {};
  const ctx = contextFor(location.pathname, location.search);
  // A list's preview pane shows a ticket without putting it in the URL.
  if (!ctx.ticket_id && typeof document !== "undefined") {
    const id = document.querySelector<HTMLElement>("[data-ollie-ticket]")?.dataset.ollieTicket;
    if (id && UUID.test(id)) {
      ctx.ticket_id = id;
      ctx.page = "ticket";
    }
  }
  return ctx;
}
