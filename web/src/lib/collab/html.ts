// Small HTML helpers for Collab: escaping, and the people a comment mentions.

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

/** The user ids of the mentions in editor HTML (`<span data-type="mention" data-id="...">`), unique, in order. */
export function mentionIds(html: string): string[] {
  const out: string[] = [];
  const tag = /<span\b[^>]*>/gi;
  for (const m of html.matchAll(tag)) {
    const t = m[0];
    if (!/data-type="mention"|class="[^"]*\bmention\b/.test(t)) continue;
    const id = /data-id="([^"]+)"/.exec(t)?.[1];
    if (id && !out.includes(id)) out.push(id);
  }
  return out;
}

/** True when editor HTML has no text and no image. */
export function isBlankHtml(html: string): boolean {
  if (/<img\b/i.test(html)) return false;
  return !html.replace(/<[^>]*>/g, "").replace(/&nbsp;/g, " ").trim();
}
