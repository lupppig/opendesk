// The agent's procedures on the client (contracts/agent.md "Procedures"): the
// server's limits, and the one-time copy of a wiki page into a procedure's
// instructions as markdown.

export const MAX_PROCEDURES = 50;
export const PROCEDURE_NAME_MAX = 80;
export const PROCEDURE_WHEN_MIN = 3;
export const PROCEDURE_WHEN_MAX = 300;
export const PROCEDURE_INSTRUCTIONS_MAX = 8000;

/** Why a procedure's fields can't be saved, per field; empty when they can. */
export function procedureProblems(p: { name: string; when: string; instructions: string }): { name?: true; when?: true; instructions?: true } {
  const name = p.name.trim().length;
  const when = p.when.trim().length;
  const ins = p.instructions.trim().length;
  return {
    ...(name < 1 || name > PROCEDURE_NAME_MAX ? { name: true as const } : {}),
    ...(when < PROCEDURE_WHEN_MIN || when > PROCEDURE_WHEN_MAX ? { when: true as const } : {}),
    ...(ins < 1 || ins > PROCEDURE_INSTRUCTIONS_MAX ? { instructions: true as const } : {}),
  };
}

const BLOCKS = new Set(["P", "DIV", "H1", "H2", "H3", "H4", "H5", "H6", "UL", "OL", "LI", "BLOCKQUOTE", "PRE", "HR", "TABLE", "TR"]);

/** Inline markdown of an element's children: bold, italics, code, links and line breaks. */
function inline(node: Node): string {
  let out = "";
  node.childNodes.forEach((c) => {
    if (c.nodeType === Node.TEXT_NODE) {
      out += (c.textContent ?? "").replace(/\s+/g, " ");
      return;
    }
    if (c.nodeType !== Node.ELEMENT_NODE) return;
    const el = c as HTMLElement;
    const inner = inline(el);
    switch (el.tagName) {
      case "BR":
        out += "\n";
        break;
      case "STRONG":
      case "B":
        out += inner.trim() ? `**${inner.trim()}**` : "";
        break;
      case "EM":
      case "I":
        out += inner.trim() ? `*${inner.trim()}*` : "";
        break;
      case "CODE":
        out += `\`${el.textContent ?? ""}\``;
        break;
      case "A": {
        const href = el.getAttribute("href") ?? "";
        out += /^(https?:|mailto:)/i.test(href) && inner.trim() ? `[${inner.trim()}](${href})` : inner;
        break;
      }
      case "IMG":
        break;
      default:
        out += BLOCKS.has(el.tagName) ? `\n${block(el, "").trim()}\n` : inner;
    }
  });
  return out;
}

/** Markdown of one block element; `indent` prefixes nested list items. */
function block(el: HTMLElement, indent: string): string {
  switch (el.tagName) {
    case "H1":
    case "H2":
    case "H3":
    case "H4":
    case "H5":
    case "H6":
      return `${"#".repeat(Number(el.tagName[1]))} ${inline(el).trim()}`;
    case "UL":
    case "OL": {
      const ordered = el.tagName === "OL";
      const items = [...el.children].filter((c) => c.tagName === "LI") as HTMLElement[];
      return items
        .map((li, i) => {
          const nested = [...li.children].filter((c) => c.tagName === "UL" || c.tagName === "OL") as HTMLElement[];
          const clone = li.cloneNode(true) as HTMLElement;
          clone.querySelectorAll(":scope > ul, :scope > ol").forEach((n) => n.remove());
          const text = inline(clone).trim().replace(/\n+/g, " ");
          const sub = nested.map((n) => block(n, `${indent}   `)).join("\n");
          return `${indent}${ordered ? `${i + 1}.` : "-"} ${text}${sub ? `\n${sub}` : ""}`;
        })
        .join("\n");
    }
    case "BLOCKQUOTE":
      return children(el)
        .split("\n")
        .map((l) => (l ? `> ${l}` : ">"))
        .join("\n");
    case "PRE":
      return "```\n" + (el.textContent ?? "").replace(/\n$/, "") + "\n```";
    case "HR":
      return "---";
    default:
      return [...el.children].some((c) => BLOCKS.has(c.tagName)) ? children(el) : inline(el).trim();
  }
}

/** Markdown of a container's children, one blank line between blocks. */
function children(root: Element): string {
  const parts: string[] = [];
  let loose = "";
  const flush = () => {
    if (loose.trim()) parts.push(loose.trim());
    loose = "";
  };
  root.childNodes.forEach((c) => {
    if (c.nodeType === Node.ELEMENT_NODE && BLOCKS.has((c as Element).tagName)) {
      flush();
      const md = block(c as HTMLElement, "");
      if (md.trim()) parts.push(md);
    } else if (c.nodeType === Node.TEXT_NODE || c.nodeType === Node.ELEMENT_NODE) {
      const holder = document.createElement("span");
      holder.appendChild(c.cloneNode(true));
      loose += inline(holder);
    }
  });
  flush();
  return parts.join("\n\n");
}

/** A wiki page's rich text (sanitized HTML, contracts/wiki.md "Body") as markdown. */
export function htmlToMarkdown(html: string): string {
  const doc = new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");
  return children(doc.body)
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}
