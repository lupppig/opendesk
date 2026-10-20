// The light markdown Ollie writes, rendered to React elements (never HTML):
// paragraphs, "-" and "1." lists, **bold**, *italic*, `code`, [links](https://…)
// and #123 ticket references, which open the ticket.

import { Fragment, type ReactNode } from "react";
import { safeNext } from "@/components/auth/parts";
import { cn } from "@/lib/cn";
import type { AssistantRef } from "./api";
import { openTicket } from "./refs";

type Ctx = { tickets: Map<number, AssistantRef> };

const INLINE = /(\*\*[^*\n]+\*\*|`[^`\n]+`|\[[^\]\n]+\]\([^)\s]+\)|(?<![\w&])#\d{1,9}\b|(?<![\w*])\*[^*\n]+\*(?!\w)|(?<![\w_])_[^_\n]+_(?!\w))/g;

/** An absolute https/http link, or a same-origin app path (safeNext refuses "//" and "/\\" tricks). */
function safeHref(href: string): string | null {
  if (/^https?:\/\//i.test(href)) return href;
  return safeNext(href);
}

export function TicketChip({ number, ticket }: { number: number; ticket?: AssistantRef }) {
  return (
    <button
      type="button"
      onClick={() => void openTicket(ticket?.id, number)}
      title={ticket?.label}
      className="mono inline-flex items-baseline rounded-[4px] bg-tone-violet-soft px-1 text-[0.92em] font-medium text-tone-violet transition-colors hover:bg-tone-violet-line hover:underline"
    >
      #{number}
    </button>
  );
}

function inline(text: string, ctx: Ctx, key: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let i = 0;
  for (const m of text.matchAll(INLINE)) {
    const tok = m[0];
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const k = `${key}-${i++}`;
    if (tok.startsWith("**")) out.push(<strong key={k} className="font-semibold text-fg">{inline(tok.slice(2, -2), ctx, k)}</strong>);
    else if (tok.startsWith("`")) out.push(<code key={k} className="mono rounded-[3px] bg-sunken px-1 text-[0.9em]">{tok.slice(1, -1)}</code>);
    else if (tok.startsWith("[")) {
      const mm = tok.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
      const href = mm ? safeHref(mm[2]) : null;
      out.push(
        href ? (
          <a key={k} href={href} target={href.startsWith("/") ? undefined : "_blank"} rel="noopener noreferrer" className="text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg">
            {mm![1]}
          </a>
        ) : (
          <Fragment key={k}>{mm?.[1] ?? tok}</Fragment>
        ),
      );
    } else if (tok.startsWith("#")) {
      const n = Number(tok.slice(1));
      out.push(<TicketChip key={k} number={n} ticket={ctx.tickets.get(n)} />);
    } else out.push(<em key={k}>{inline(tok.slice(1, -1), ctx, k)}</em>);
    last = at + tok.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

type Node = { kind: "p"; lines: string[] } | { kind: "ul" | "ol"; items: string[] } | { kind: "h"; text: string };

export function parseBlocks(md: string): Node[] {
  const nodes: Node[] = [];
  let para: string[] = [];
  const flush = () => {
    if (para.length) nodes.push({ kind: "p", lines: para });
    para = [];
  };
  for (const raw of md.replace(/\r\n/g, "\n").split("\n")) {
    const line = raw.trimEnd();
    const bullet = line.match(/^\s*[-*•]\s+(.*)$/);
    const num = line.match(/^\s*\d+[.)]\s+(.*)$/);
    const head = line.match(/^\s*#{1,4}\s+(.*)$/);
    if (!line.trim()) {
      flush();
      continue;
    }
    if (head) {
      flush();
      nodes.push({ kind: "h", text: head[1] });
      continue;
    }
    if (bullet || num) {
      flush();
      const kind = bullet ? "ul" : "ol";
      const item = (bullet ?? num)![1];
      const prev = nodes[nodes.length - 1];
      if (prev && prev.kind === kind) prev.items.push(item);
      else nodes.push({ kind, items: [item] });
      continue;
    }
    const prev = nodes[nodes.length - 1];
    // A wrapped line continues the list item above it.
    if (!para.length && prev && (prev.kind === "ul" || prev.kind === "ol") && /^\s{2,}/.test(raw)) {
      prev.items[prev.items.length - 1] += " " + line.trim();
      continue;
    }
    para.push(line);
  }
  flush();
  return nodes;
}

export function Markdown({ text, refs = [], caret, className }: { text: string; refs?: AssistantRef[]; caret?: boolean; className?: string }) {
  const tickets = new Map<number, AssistantRef>();
  for (const r of refs) if (r.kind === "ticket" && r.number) tickets.set(r.number, r);
  const ctx: Ctx = { tickets };
  const nodes = parseBlocks(text);
  const caretEl = caret ? <span aria-hidden className="ollie-caret ml-0.5 inline-block h-[1.05em] w-[2px] translate-y-[0.18em] rounded-full bg-tone-violet-solid" /> : null;
  return (
    <div className={cn("flex flex-col gap-1.5 text-[13px] leading-[20px] text-fg-2", className)} data-ph-mask>
      {nodes.map((n, i) => {
        const last = i === nodes.length - 1;
        const k = `b${i}`;
        if (n.kind === "h") return <p key={k} className="font-semibold text-fg">{inline(n.text, ctx, k)}{last && caretEl}</p>;
        if (n.kind === "p")
          return (
            <p key={k}>
              {n.lines.map((l, j) => (
                <Fragment key={j}>
                  {j > 0 && <br />}
                  {inline(l, ctx, `${k}-${j}`)}
                </Fragment>
              ))}
              {last && caretEl}
            </p>
          );
        const List = n.kind === "ul" ? "ul" : "ol";
        return (
          <List key={k} className={cn("flex flex-col gap-0.5 pl-4", n.kind === "ul" ? "list-disc marker:text-fg-4" : "list-decimal marker:text-fg-4 marker:text-[12px]")}>
            {n.items.map((it, j) => (
              <li key={j} className="pl-0.5">
                {inline(it, ctx, `${k}-${j}`)}
                {last && j === n.items.length - 1 && caretEl}
              </li>
            ))}
          </List>
        );
      })}
      {nodes.length === 0 && caretEl && <p>{caretEl}</p>}
    </div>
  );
}
