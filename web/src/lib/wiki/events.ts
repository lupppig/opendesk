// wiki_page.* events (contracts/wiki.md "Events") on the TanStack Query cache. They carry
// the page without its body: the tree is patched in place, and an open page someone else
// changed elsewhere is refetched (the page view then takes it, or asks when typing is
// unsaved). Elsewhere includes the same person in another tab or through Ollie: what
// tells this tab's own saves apart is the version its saver already holds.

import type { QueryClient } from "@tanstack/react-query";
import type { WikiNode, WikiPage } from "../api/types";
import { qk } from "../queries";
import { openSaver } from "./saver";
import { removeSubtree, upsertNode } from "./tree";

export function applyWikiEvent(qc: QueryClient, verb: string, d: Record<string, unknown>) {
  void qc.invalidateQueries({ queryKey: qk.of("wiki", "search") });
  const patch = (fn: (nodes: WikiNode[]) => WikiNode[]) => qc.setQueryData<WikiNode[]>(qk.wikiTree, (old) => (old ? fn(old) : old));
  if (verb === "deleted") {
    const ids = (d.wiki_page_ids as string[] | undefined) ?? [d.wiki_page_id as string];
    patch((nodes) => removeSubtree(nodes, ids));
    for (const id of ids) qc.removeQueries({ queryKey: qk.wikiPage(id) });
    return;
  }
  const node = d.wiki_page as WikiNode | undefined;
  if (!node) return;
  if (verb === "moved") {
    // Positions of its old and new siblings moved too, and breadcrumbs below it.
    void qc.invalidateQueries({ queryKey: qk.wikiTree });
    void qc.invalidateQueries({ queryKey: qk.of("wiki", "page") });
    return;
  }
  patch((nodes) => upsertNode(nodes, node));
  const page = qc.getQueryData<WikiPage>(qk.wikiPage(node.id));
  // This tab's own saves come back with their answer (the saver has the version already).
  const known = Math.max(page?.version ?? Infinity, openSaver(node.id)?.version ?? 0);
  if (known < node.version) void qc.invalidateQueries({ queryKey: qk.wikiPage(node.id) });
  void qc.invalidateQueries({ queryKey: qk.wikiRevisions(node.id), refetchType: "none" });
}
