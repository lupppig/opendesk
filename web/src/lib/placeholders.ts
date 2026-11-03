// Unfilled placeholders in a reply: the short notes in square brackets that AI drafts leave
// for the agent ("[confirm the refund amount]"), so a reply is not sent with one by mistake.
//
// The rule, on the reply's own text only (quoted text, code and links left out, each
// paragraph or line on its own): a placeholder is "[" and "]" around 2 to 300 characters
// on one line, holding at least one letter and no other bracket, and not
//   - a markdown link or image: "]" directly followed by "(";
//   - a reference: it starts with "#" or "@" ([#123], [@anna]), has "#" before a digit
//     ([Ticket #123]), is one word with a digit ([ACM-7], [INV/2026/0042], [v2]), or starts
//     with cid:, image:, mailto:, http: or https: (inline image and link stand-ins);
//   - a cut or a checkbox: no letter ([…], [...], [ ], [1]) or a single character ([x]).

/** Elements whose text is not the reply's own words. */
const SKIP = new Set(["BLOCKQUOTE", "PRE", "CODE", "A", "SCRIPT", "STYLE", "TEMPLATE"]);
/** Elements that end a line. */
const BLOCK = new Set(["P", "DIV", "LI", "UL", "OL", "H1", "H2", "H3", "H4", "H5", "H6", "TR", "TD", "TH", "TABLE", "HR", "SECTION", "ARTICLE"]);

const SPAN = /\[([^[\]\n]{2,300})\](?!\()/g;
const LETTER = /\p{L}/u;
const DIGIT = /\p{N}/u;
const STANDIN = /^(cid|image|mailto|https?):/i;
const NBSP = String.fromCharCode(0xa0);

/** The reply's own text, a line per paragraph, without quoted text, code or links. */
export function ownText(html: string): string {
  if (!html) return "";
  const doc = new DOMParser().parseFromString(html, "text/html");
  const out: string[] = [];
  const walk = (n: Node) => {
    if (n.nodeType === Node.TEXT_NODE) {
      out.push((n.textContent ?? "").split(NBSP).join(" "));
      return;
    }
    if (n.nodeType !== Node.ELEMENT_NODE) return;
    const tag = (n as Element).tagName;
    if (SKIP.has(tag)) return;
    if (tag === "BR") {
      out.push("\n");
      return;
    }
    const block = BLOCK.has(tag);
    if (block) out.push("\n");
    n.childNodes.forEach(walk);
    if (block) out.push("\n");
  };
  doc.body.childNodes.forEach(walk);
  return out.join("");
}

/** Whether a bracketed span's content is a placeholder rather than a reference (rule above). */
export function isPlaceholder(content: string): boolean {
  const c = content.trim();
  if (c.length < 2 || !LETTER.test(c)) return false;
  if (c.startsWith("#") || c.startsWith("@") || /#\s*\p{N}/u.test(c) || STANDIN.test(c)) return false;
  if (!/\s/.test(c) && DIGIT.test(c)) return false;
  return true;
}

/** The unfilled placeholders of a reply, each once, in order, brackets included. */
export function findPlaceholders(html: string): string[] {
  const found: string[] = [];
  for (const line of ownText(html).split("\n")) {
    // A plain-text quote ("> ...") is someone else's words.
    if (line.trimStart().startsWith(">")) continue;
    for (const m of line.matchAll(SPAN)) {
      const p = `[${m[1].trim()}]`;
      if (isPlaceholder(m[1]) && !found.includes(p)) found.push(p);
    }
  }
  return found;
}
