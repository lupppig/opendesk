// Quoted history in a mail message: what the sender wrote, and the earlier mail their
// client copied under it. The conversation already shows the earlier mail, so the reader
// sees the new part and opens the rest when they ask, as a mail app does. The markers
// follow the server's (internal/email/quote.go), quotes only: a signature stays.

export type Split = { body: string; quoted: string };

const ON_WROTE = new RegExp(
  "^\\s*(" +
    [
      "on\\s.{1,300}\\swrote\\s?:",
      "le\\s.{1,300}\\sa\\s[ée]crit\\s?:",
      "am\\s.{1,300}\\sschrieb.{0,100}:",
      "el\\s.{1,300}\\sescribi[óo]\\s?:",
      "op\\s.{1,300}\\sschreef.{0,60}:",
      "il\\s.{1,300}\\sha\\sscritto\\s?:",
      "em\\s.{1,300}\\sescreveu\\s?:",
      "den\\s.{1,300}\\sskrev.{0,60}:",
    ].join("|") +
    ")\\s*$",
  "is",
);
const ORIGINAL = /^\s*-{2,}\s*(original message|ursprüngliche nachricht|message d'origine|mensaje original|messaggio originale)\s*-{2,}/i;
const UNDERSCORES = /^\s*_{20,}\s*$/;
const HEADER_FROM = /^\s*\*?(from|von|de|da|van)\s?:\*?\s+\S/i;
const HEADER_SENT = /^\s*\*?(sent|date|gesendet|datum|envoyé|enviado|inviato|verzonden)\s?:\*?\s/i;
const FORWARD = /^\s*(fwd?|fw|tr|wg|rv|enc)\s*:/i;
const ATTRIBUTION = /^(on|le|am|el|op|il|em|den) /i;

const flat = (s: string) => s.replace(/\u00a0/g, " ").replace(/\s+/g, " ").trim();

/** A forward keeps what it carries: the forwarded mail is the point of the message. */
export const isForward = (subject: string) => FORWARD.test(subject);

/** Splits a plain-text body at its quoted history, or null when it has none. */
export function splitQuotedText(text: string): Split | null {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const next = (from: number, n: number, re: RegExp) => lines.slice(from, from + n).some((l) => re.test(l.trim()));
  const quotedToEnd = (i: number) => lines.slice(i).every((l) => !l.trim() || l.trim().startsWith(">"));
  let cut = -1;
  for (let i = 0; i < lines.length && cut < 0; i++) {
    const line = flat(lines[i]);
    if (
      ON_WROTE.test(line) ||
      (i + 1 < lines.length && ATTRIBUTION.test(line) && ON_WROTE.test(`${line} ${flat(lines[i + 1])}`)) ||
      ORIGINAL.test(line) ||
      (UNDERSCORES.test(line) && next(i + 1, 3, HEADER_FROM)) ||
      (HEADER_FROM.test(line) && next(i + 1, 5, HEADER_SENT)) ||
      (line.startsWith(">") && quotedToEnd(i))
    )
      cut = i;
  }
  if (cut < 0) return null;
  const body = lines.slice(0, cut).join("\n").trimEnd();
  if (!body.trim()) return null;
  return { body, quoted: lines.slice(cut).join("\n") };
}

const BLOCKS = new Set(["DIV", "P", "LI", "TD", "BLOCKQUOTE", "BODY", "SECTION", "ARTICLE"]);

/** The text of a block, with its line breaks. */
function blockText(el: Element): string {
  let out = "";
  const walk = (n: Node) => {
    if (n.nodeType === Node.TEXT_NODE) out += n.nodeValue ?? "";
    else if (n.nodeName === "BR") out += "\n";
    else n.childNodes.forEach(walk);
    if (n !== el && BLOCKS.has(n.nodeName)) out += "\n";
  };
  walk(el);
  return out;
}

/** An element that starts quoted history, by the class or layout its mail client gives it. */
function markerElement(el: Element): boolean {
  const cls = el.classList;
  if (cls.contains("gmail_quote") || cls.contains("gmail_quote_container") || cls.contains("gmail_extra")) return true; // Gmail
  if (cls.contains("moz-cite-prefix")) return true; // Thunderbird
  if (cls.contains("OutlookMessageHeader") || /\byahoo_quoted/i.test(el.getAttribute("class") ?? "")) return true; // Outlook, Yahoo
  if (cls.contains("protonmail_quote") || cls.contains("zmail_extra") || cls.contains("zmail_extra_hr")) return true; // Proton, Zoho
  if (el.tagName !== "DIV" && el.tagName !== "P") return false;
  const text = blockText(el).replace(/\u00a0/g, " ");
  const style = (el.getAttribute("style") ?? "").toLowerCase().replace(/\s/g, "");
  // Outlook desktop's reply header: a top border, then "From:".
  if (/border-top:(\d|solid)/.test(style) && HEADER_FROM.test(text.trim())) return true;
  const lines = text.split("\n").map(flat).filter(Boolean);
  // A reply header block: "From:" then "Sent:" or "Date:" (Outlook, Apple Mail forwards).
  if (lines.length > 1 && HEADER_FROM.test(lines[0]) && lines.slice(1, 5).some((l) => HEADER_SENT.test(l))) return true;
  // An "On ... wrote:" or "Original Message" line in its own block.
  return lines.length > 0 && lines.length <= 3 && (ON_WROTE.test(lines.join(" ")) || ORIGINAL.test(lines[0]));
}

/** The first node where quoted history starts, in document order. */
function findMarker(body: HTMLElement): Node | null {
  const walker = body.ownerDocument.createTreeWalker(body, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (n.nodeType === Node.ELEMENT_NODE) {
      if (markerElement(n as Element)) return n;
      continue;
    }
    // A line of text between breaks: "On ... wrote:" (it may wrap onto a second line),
    // "Original Message", or an Outlook header block ("From:" then "Sent:").
    const line = flat(n.nodeValue ?? "");
    if (!line) continue;
    const after = followingLines(n, 5);
    if (ON_WROTE.test(line) || (ATTRIBUTION.test(line) && after[0] && ON_WROTE.test(`${line} ${after[0]}`)) || ORIGINAL.test(line) || (HEADER_FROM.test(line) && after.some((l) => HEADER_SENT.test(l)))) return n;
  }
  // A quote that runs to the end with nothing after it (Apple Mail's, once its type is gone).
  const quotes = body.querySelectorAll("blockquote");
  const lastQuote = quotes[quotes.length - 1];
  if (lastQuote && !lastQuote.parentElement?.closest("blockquote") && nothingAfter(lastQuote, body)) {
    const prev = previousContent(lastQuote);
    return prev && prev.nodeType === Node.TEXT_NODE && ON_WROTE.test(flat(prev.nodeValue ?? "")) ? prev : lastQuote;
  }
  return null;
}

/** The text of the next few non-empty text nodes after n. */
function followingLines(n: Node, count: number): string[] {
  const walker = n.ownerDocument!.createTreeWalker(n.ownerDocument!.body, NodeFilter.SHOW_TEXT);
  walker.currentNode = n;
  const out: string[] = [];
  for (let t = walker.nextNode(); t && out.length < count; t = walker.nextNode()) {
    const l = flat(t.nodeValue ?? "");
    if (l) out.push(l);
  }
  return out;
}

function nothingAfter(el: Node, root: Node): boolean {
  for (let n: Node | null = el; n && n !== root; n = n.parentNode) {
    for (let s = n.nextSibling; s; s = s.nextSibling) if (!blank(s)) return false;
  }
  return true;
}

function previousContent(el: Node): Node | null {
  let p: Node | null = el.previousSibling;
  while (p && blank(p)) p = p.previousSibling;
  while (p && p.nodeType === Node.ELEMENT_NODE && p.lastChild && !blank(p)) {
    let c: Node | null = p.lastChild;
    while (c && blank(c)) c = c.previousSibling;
    if (!c) break;
    p = c;
  }
  return p;
}

function blank(n: Node): boolean {
  if (n.nodeType === Node.COMMENT_NODE) return true;
  if (n.nodeType === Node.TEXT_NODE) return !flat(n.nodeValue ?? "");
  if (n.nodeType !== Node.ELEMENT_NODE) return true;
  const el = n as Element;
  if (el.tagName === "BR" || el.tagName === "STYLE") return true;
  if (el.tagName === "IMG" || el.querySelector("img, table")) return false;
  return [...el.childNodes].every(blank);
}

/** Drops the empty lines and blocks a cut leaves at the end. */
function trimEnd(n: Node) {
  while (n.lastChild && blank(n.lastChild)) n.removeChild(n.lastChild);
  if (n.lastChild?.nodeType === Node.ELEMENT_NODE && (n.lastChild as Element).tagName !== "IMG") trimEnd(n.lastChild);
}

/**
 * Splits an HTML body at its quoted history, or null when it has none or nothing would
 * remain above it. Parsing is inert (nothing loads or runs); both halves are sanitized
 * by the caller. The style sheets that lead a body go with both halves.
 */
export function splitQuotedHtml(html: string): Split | null {
  const doc = new DOMParser().parseFromString(`<!doctype html><body>${html}`, "text/html");
  const body = doc.body;
  const marker = findMarker(body);
  if (!marker) return null;
  const range = doc.createRange();
  range.setStartBefore(marker);
  range.setEndAfter(body.lastChild!);
  const quoted = range.extractContents();
  trimEnd(body);
  if (blank(body)) return null;
  const holder = doc.createElement("div");
  holder.appendChild(quoted);
  const sheets = [...body.querySelectorAll(":scope > style")].map((s) => s.outerHTML).join("");
  return { body: body.innerHTML, quoted: sheets + holder.innerHTML };
}
