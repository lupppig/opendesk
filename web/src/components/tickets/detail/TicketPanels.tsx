// Ticket panels (contracts/integrations.md "Ticket panels"): beside the ticket, what Shopify
// or a URL of the workspace's own knows about its contact. Read-only and never in the way:
// each panel loads on its own, folds away, and a failure is one quiet line.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowUpRight,
  BadgePercent,
  Building,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  CreditCard,
  ExternalLink,
  FileText,
  Globe,
  Info,
  Mail,
  MapPin,
  Package,
  Phone,
  Receipt,
  RefreshCw,
  ShoppingCart,
  Store,
  TriangleAlert,
  Truck,
  Undo2,
  User,
  type LucideIcon,
} from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useId, useState, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AppMark } from "@/components/appmark";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import type {
  PanelBadge,
  PanelBlock,
  PanelField,
  PanelLine,
  PanelLink,
  PanelListItem,
  PanelOrder,
  PanelSection,
  PanelTone,
  PanelValue,
  ShopifyField,
  TicketPanel,
  TicketPanelData,
} from "@/lib/api/types";
import { appLogo } from "@/lib/appLogos";
import { cn } from "@/lib/cn";
import { countryName, fullDate, money, number, panelDate, relDay, shortDate } from "@/lib/format";
import { ease } from "@/lib/motion";
import { DEFAULT_SHOPIFY_FIELDS, MAX_ORDERS_SHOWN, panelShows, shownData } from "@/lib/panelFields";
import { blockKeys, foldScope, useCardFold } from "@/lib/panelFolds";
import { q, qk } from "@/lib/queries";

const CLOSED_KEY = "od-ticket-panels-closed";

function readClosed(): Set<string> {
  try {
    const v = JSON.parse(localStorage.getItem(CLOSED_KEY) ?? "[]");
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

function writeClosed(ids: Set<string>) {
  try {
    localStorage.setItem(CLOSED_KEY, JSON.stringify([...ids]));
  } catch {
    /* storage unavailable */
  }
}

/** The mark of a panel's source: Shopify's own, or the globe for a URL. */
export function PanelMark({ panel, size = "sm" }: { panel: Pick<TicketPanel, "kind" | "name">; size?: "sm" | "md" }) {
  return panel.kind === "shopify" ? (
    <AppMark app="shopify" name={panel.name} tint="green" size={size} />
  ) : (
    <AppMark name={panel.name} tint="blue" glyph={<Globe />} size={size} />
  );
}

/** Every enabled panel of the workspace, for the open ticket. */
export function TicketPanels({ ticketId }: { ticketId: string }) {
  const { data: panels } = useQuery(q.ticketPanels());
  const [closed, setClosed] = useState(readClosed);
  const on = (panels ?? []).filter((p) => p.enabled);
  if (!on.length) return null;
  const toggle = (id: string) =>
    setClosed((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      writeClosed(n);
      return n;
    });
  return (
    <>
      {on.map((p) => (
        <PanelSectionView key={p.id} ticketId={ticketId} panel={p} open={!closed.has(p.id)} onToggle={() => toggle(p.id)} />
      ))}
    </>
  );
}

function PanelSectionView({ ticketId, panel, open, onToggle }: { ticketId: string; panel: TicketPanel; open: boolean; onToggle: () => void }) {
  const { t } = useTranslation("inbox");
  const qc = useQueryClient();
  const reduce = useReducedMotion();
  const { data, isLoading } = useQuery({ ...q.ticketPanel(ticketId, panel.id), enabled: open });
  const [refreshing, setRefreshing] = useState(false);
  const shows = panelShows(panel);
  const refresh = async () => {
    setRefreshing(true);
    try {
      qc.setQueryData(qk.ticketPanel(ticketId, panel.id), await api.tickets.panel(ticketId, panel.id, true));
    } catch {
      /* the panel keeps what it showed */
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <section className="border-b border-line-soft" aria-label={panel.name}>
      <div className="group/panel flex items-center gap-2 px-4 py-3">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={open}
          aria-label={open ? t("panels.collapse", { name: panel.name }) : t("panels.expand", { name: panel.name })}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-md text-left outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]"
        >
          <PanelMark panel={panel} />
          <h3 className="label-caps min-w-0 truncate">{panel.name}</h3>
          <ChevronRight className={cn("size-3.5 shrink-0 text-fg-4 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-90")} />
        </button>
        {open && (
          <Tooltip content={t("panels.refresh", { name: panel.name })}>
            <button
              type="button"
              onClick={() => void refresh()}
              disabled={refreshing}
              aria-label={t("panels.refresh", { name: panel.name })}
              className="flex size-6 items-center justify-center rounded-md text-fg-4 opacity-0 transition-opacity hover:bg-hover hover:text-fg focus-visible:opacity-100 group-hover/panel:opacity-100 disabled:opacity-60"
            >
              <RefreshCw className={cn("size-3.5", refreshing && "animate-spin motion-reduce:animate-none")} />
            </button>
          </Tooltip>
        )}
      </div>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            key="body"
            initial={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            animate={reduce ? { opacity: 1 } : { height: "auto", opacity: 1 }}
            exit={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            transition={{ duration: 0.2, ease: ease.out }}
            className="overflow-hidden"
          >
            <div className="px-4 pb-3.5" data-ph-mask>
              {isLoading || !data ? (
                <PanelSkeleton />
              ) : (
                <PanelBody data={data} fields={shows.fields} ordersShown={shows.ordersShown} onRetry={() => void refresh()} foldScope={foldScope(ticketId, panel.id)} />
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </section>
  );
}

function PanelSkeleton() {
  return (
    <div className="flex flex-col gap-2">
      <Skeleton className="h-3.5 w-2/3" />
      <Skeleton className="h-3 w-1/2" />
      <Skeleton className="h-10 w-full" />
    </div>
  );
}

function Quiet({ children }: { children: ReactNode }) {
  return <p className="text-[12px] text-fg-4">{children}</p>;
}

/**
 * What a panel answered. A Shopify answer shows the panel's `fields` and `ordersShown` only (an
 * answer from before they changed may carry more); settings previews a choice with the same view.
 * `foldScope` (lib/panelFolds) keeps the cards the agent folded or opened as they left them.
 */
export function PanelBody({
  data,
  onRetry,
  fields = DEFAULT_SHOPIFY_FIELDS,
  ordersShown = MAX_ORDERS_SHOWN,
  foldScope,
}: {
  data: TicketPanelData;
  onRetry?: () => void;
  fields?: readonly ShopifyField[];
  ordersShown?: number;
  foldScope?: string;
}) {
  const { t } = useTranslation("inbox");
  switch (data.state) {
    case "no_contact":
      return <Quiet>{t("panels.noContact")}</Quiet>;
    case "empty":
      return <Quiet>{data.kind === "shopify" ? t("panels.emptyShopify") : t("panels.empty")}</Quiet>;
    case "error":
      return (
        <p className="text-[12px] text-fg-4">
          {t("panels.error", { message: data.message ?? "" })}{" "}
          {onRetry && (
            <button type="button" onClick={onRetry} className="text-fg-3 underline-offset-2 outline-none hover:text-fg hover:underline focus-visible:underline">
              {t("panels.retry")}
            </button>
          )}
        </p>
      );
  }
  if (data.kind === "shopify") return <Commerce data={shownData(data, fields, ordersShown)} />;
  return data.blocks ? <Blocks blocks={data.blocks} foldScope={foldScope} /> : <Sections sections={data.sections ?? []} />;
}

/** "Partly refunded" for partially_refunded, the word Shopify uses otherwise. */
function useStatusWord() {
  const { t, i18n } = useTranslation("inbox");
  return (s: string) => {
    const key = `panels.status.${s}`;
    return i18n.exists(key, { ns: "inbox" }) ? t(key as "panels.status.paid") : s.replace(/_/g, " ");
  };
}

function statusTone(s: string): "ok" | "warn" | "danger" | "neutral" {
  if (s === "paid" || s === "fulfilled") return "ok";
  if (s === "refunded" || s === "voided" || s === "expired") return "danger";
  if (s === "pending" || s === "partially_paid" || s === "partially_refunded" || s === "partially_fulfilled" || s === "on_hold") return "warn";
  return "neutral";
}

/** A Shopify answer, already cut to what the panel shows: whatever it carries shows. */
function Commerce({ data }: { data: TicketPanelData }) {
  const { t } = useTranslation("inbox");
  const c = data.customer;
  const orders = data.orders ?? [];
  const stats = c
    ? [
        c.orders_count !== undefined ? t("panels.orders", { count: c.orders_count }) : "",
        c.total_spent ? t("panels.spent", { amount: money(c.total_spent, c.currency ?? "") }) : "",
      ].filter(Boolean)
    : [];
  return (
    <div className="flex flex-col gap-2.5">
      {c && (
        <div className="flex flex-col gap-1">
          <a
            href={c.url}
            target="_blank"
            rel="noopener noreferrer"
            title={t("panels.openCustomer")}
            className="group inline-flex min-w-0 items-center gap-1 text-[12.5px] font-medium text-fg outline-none hover:underline focus-visible:underline"
          >
            <span className="truncate">{c.name}</span>
            <ArrowUpRight className="size-3 shrink-0 text-fg-4 group-hover:text-fg" />
          </a>
          {c.email && <p className="truncate text-[11.5px] text-fg-3">{c.email}</p>}
          {stats.length > 0 && <p className="text-[11.5px] text-fg-3">{stats.join(" · ")}</p>}
          {c.tags.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {c.tags.slice(0, 6).map((tag) => (
                <Badge key={tag}>{tag}</Badge>
              ))}
            </div>
          )}
        </div>
      )}
      {orders.length ? (
        <ul className="flex flex-col gap-1.5">
          {orders.map((o) => (
            <OrderRow key={o.id} o={o} />
          ))}
        </ul>
      ) : (
        <Quiet>{t("panels.noOrders")}</Quiet>
      )}
    </div>
  );
}

/** "Brussels, Belgium · DHL Express": where an order goes and how, from what the store knows. */
export function shippingLine(s: PanelOrder["shipping"]): string {
  if (!s) return "";
  const place = [s.city, s.country ? countryName(s.country) : ""].filter(Boolean).join(", ");
  return [place, s.method].filter(Boolean).join(" · ");
}

function OrderRow({ o }: { o: PanelOrder }) {
  const { t } = useTranslation("inbox");
  const word = useStatusWord();
  const shipping = shippingLine(o.shipping);
  const codes = o.discount_codes ?? [];
  return (
    <li className={cn("rounded-md border border-line-soft px-2.5 py-2", o.cancelled && "bg-sunken")}>
      <div className="flex items-baseline gap-2">
        <a
          href={o.url}
          target="_blank"
          rel="noopener noreferrer"
          title={t("panels.openOrder", { number: o.number })}
          className="group inline-flex items-center gap-0.5 mono text-[12px] font-medium text-fg outline-none hover:underline focus-visible:underline"
        >
          {o.number}
          <ArrowUpRight className="size-3 shrink-0 text-fg-4 group-hover:text-fg" aria-hidden />
        </a>
        <span className="text-[11.5px] text-fg-4">{shortDate(o.created_at)}</span>
        <span className={cn("ml-auto text-[12px] tabular-nums", o.cancelled ? "text-fg-4" : "text-fg-2")}>{money(o.total, o.currency)}</span>
      </div>
      {(o.cancelled || o.financial_status || o.fulfillment_status) && (
        <div className="mt-1 flex flex-wrap gap-1">
          {o.cancelled && (
            <Badge tone="danger">{o.cancelled_at ? t("panels.cancelledOn", { date: shortDate(o.cancelled_at) }) : t("panels.cancelled")}</Badge>
          )}
          {o.financial_status && <Badge tone={statusTone(o.financial_status)}>{word(o.financial_status)}</Badge>}
          {!o.cancelled && o.fulfillment_status && <Badge tone={statusTone(o.fulfillment_status)}>{word(o.fulfillment_status)}</Badge>}
        </div>
      )}
      {o.items.length > 0 && (
        <ul className="mt-1.5 flex flex-col gap-0.5 text-[11.5px] text-fg-3">
          {o.items.map((it, i) => (
            <li key={i} className="flex gap-1.5">
              <span className="tabular-nums text-fg-4">{it.quantity}×</span>
              <span className="min-w-0 flex-1 truncate" title={it.variant ? `${it.title} - ${it.variant}` : it.title}>
                {it.title}
                {it.variant ? <span className="text-fg-4"> - {it.variant}</span> : null}
              </span>
              {it.price && (
                <span className="shrink-0 tabular-nums text-fg-4" title={t("panels.unitPrice")}>
                  {money(it.price, o.currency)}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      {(shipping || codes.length > 0 || o.refunded) && (
        <ul className="mt-1.5 flex flex-col gap-0.5 text-[11.5px] text-fg-3">
          {shipping && (
            <li className="flex items-center gap-1.5" title={t("panels.shipping")}>
              <MapPin className="size-3 shrink-0 text-fg-4" aria-hidden />
              <span className="min-w-0 truncate">{shipping}</span>
            </li>
          )}
          {codes.length > 0 && (
            <li className="flex items-center gap-1.5" title={t("panels.discountCodes", { count: codes.length })}>
              <BadgePercent className="size-3 shrink-0 text-fg-4" aria-hidden />
              <span className="mono min-w-0 truncate">{codes.join(", ")}</span>
            </li>
          )}
          {o.refunded && (
            <li className="flex items-center gap-1.5">
              <Undo2 className="size-3 shrink-0 text-fg-4" aria-hidden />
              <span className="min-w-0 truncate">{t("panels.refunded", { amount: money(o.refunded, o.currency) })}</span>
            </li>
          )}
        </ul>
      )}
      {o.tracking.length > 0 && (
        <ul className="mt-1.5 flex flex-col gap-0.5">
          {o.tracking.map((tr, i) => (
            <li key={i} className="flex items-center gap-1.5 text-[11.5px] text-fg-3">
              <Truck className="size-3 shrink-0 text-fg-4" />
              <span className="min-w-0 truncate">{[tr.company, tr.number].filter(Boolean).join(" · ")}</span>
              {tr.url && (
                <a href={tr.url} target="_blank" rel="noopener noreferrer" className="ml-auto shrink-0 text-fg-2 outline-none hover:underline focus-visible:underline">
                  {t("panels.track")}
                </a>
              )}
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

function Sections({ sections }: { sections: PanelSection[] }) {
  return (
    <div className="flex flex-col gap-2.5">
      {sections.map((s, i) => (
        <div key={i} className="flex flex-col gap-1">
          {s.title && <h4 className="text-[11.5px] font-medium text-fg-2">{s.title}</h4>}
          <dl className="flex flex-col gap-0.5">
            {s.items.map((it, j) => (
              <div key={j} className="flex items-baseline gap-2 text-[12px]">
                <dt className="w-2/5 shrink-0 truncate text-fg-4">{it.label}</dt>
                <dd className="min-w-0 flex-1 break-words text-fg-2">
                  {it.url ? (
                    <a href={it.url} target="_blank" rel="noopener noreferrer" className="outline-none hover:underline focus-visible:underline">
                      {it.value || it.url}
                    </a>
                  ) : (
                    it.value
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </div>
      ))}
    </div>
  );
}

// Blocks: a version 2 answer from a URL (contracts/integrations.md "The http answer"), already
// cleaned by the server. A card looks like a Shopify order, and tones are the badges' own. A
// block or value type this version does not know shows nothing.

const BADGE_TONE: Record<PanelTone, "neutral" | "blue" | "ok" | "warn" | "danger"> = {
  neutral: "neutral",
  info: "blue",
  success: "ok",
  warning: "warn",
  danger: "danger",
};

function ToneBadge({ badge }: { badge: PanelBadge }) {
  const tone = BADGE_TONE[badge.tone] ?? "neutral";
  return (
    <Badge tone={tone} dot={tone === "blue"}>
      {badge.text}
    </Badge>
  );
}

/** A link out to the other product, or the content alone. */
function Out({ url, children }: { url?: string; children: ReactNode }) {
  if (!url) return <>{children}</>;
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" className="outline-none hover:underline focus-visible:underline">
      {children}
    </a>
  );
}

/** A value as the reader's locale writes it: a date in their timezone (a day as it is), money in its currency. */
export function PanelValueView({ value }: { value: PanelValue }) {
  switch (value.type) {
    case "text":
      return <>{value.text}</>;
    case "date": {
      const rel = value.relative ? relDay(value.value) : "";
      return (
        <time dateTime={value.value} title={value.value.length > 10 ? fullDate(value.value) : undefined}>
          {panelDate(value.value)}
          {rel && <span className="text-fg-4"> · {rel}</span>}
        </time>
      );
    }
    case "money":
      return <span className="tabular-nums">{money(value.amount, value.currency)}</span>;
    case "badge":
      return <ToneBadge badge={value} />;
    default:
      return null;
  }
}

function Blocks({ blocks, foldScope }: { blocks: PanelBlock[]; foldScope?: string }) {
  // A card keeps its identity when an answer adds, drops or moves cards around it.
  const keys = blockKeys(blocks);
  return (
    <div className="flex flex-col gap-2.5">
      {blocks.map((b, i) => (
        <BlockView key={keys[i]} block={b} foldScope={foldScope} foldKey={keys[i]} />
      ))}
    </div>
  );
}

function BlockView({ block, foldScope, foldKey }: { block: PanelBlock; foldScope?: string; foldKey: string }) {
  switch (block.type) {
    case "card":
      return <CardBlock block={block} foldScope={foldScope} foldKey={foldKey} />;
    case "fields":
      return (
        <div className="flex flex-col gap-1">
          {block.title && <h4 className="text-[11.5px] font-medium text-fg-2">{block.title}</h4>}
          <FieldList fields={block.items} />
        </div>
      );
    case "list":
      return <ListBlock title={block.title} items={block.items} />;
    case "note":
      return <NoteBlock text={block.text} tone={block.tone} />;
    default:
      return null;
  }
}

/** Labelled values: the label on the left, its value beside it. */
function FieldList({ fields, className }: { fields: PanelField[]; className?: string }) {
  return (
    <dl className={cn("flex flex-col gap-0.5", className)}>
      {fields.map((f, i) => (
        <div key={i} className="flex items-baseline gap-2 text-[12px]">
          {f.label && (
            <dt className="w-2/5 shrink-0 truncate text-fg-4" title={f.label}>
              {f.label}
            </dt>
          )}
          <dd className="min-w-0 flex-1 break-words text-fg-2">
            <Out url={f.url}>
              <PanelValueView value={f.value} />
            </Out>
          </dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * A card folds to its header (title, links, subtitle, badges) and opens again from its arrow or a
 * click on the header. It starts as the panel asks (`collapsed`), then stays as the agent leaves it.
 */
function CardBlock({ block, foldScope, foldKey }: { block: Extract<PanelBlock, { type: "card" }>; foldScope?: string; foldKey: string }) {
  const { t } = useTranslation("inbox");
  const badges = block.badges ?? [];
  const links = block.links ?? [];
  const fields = block.fields ?? [];
  const lines = block.lines ?? [];
  const footer = block.footer ?? [];
  const [open, toggle] = useCardFold(foldScope, foldKey, !block.collapsed);
  const bodyId = useId();
  // A card with nothing under its header has nothing to fold.
  const foldable = fields.length + lines.length + footer.length > 0;
  const shown = open || !foldable;
  const onHeader = (e: MouseEvent<HTMLDivElement>) => {
    // The header's links and buttons do their own thing (the arrow toggles by itself), and
    // selecting text in it, an order number to copy, is not a click.
    if (!foldable || (e.target as Element).closest("a, button") || window.getSelection()?.toString()) return;
    toggle();
  };
  return (
    <article className="rounded-md border border-line-soft px-2.5 py-2" aria-label={block.title}>
      <div onClick={onHeader} className={cn(foldable && "group/head cursor-pointer")}>
        <div className="flex min-w-0 items-center gap-1.5">
          <h4 className="min-w-0 flex-1 text-[12px] font-medium text-fg">
            {block.url ? (
              <a
                href={block.url}
                target="_blank"
                rel="noopener noreferrer"
                title={t("panels.openLink", { name: block.title })}
                className="group inline-flex min-w-0 max-w-full items-center gap-0.5 outline-none hover:underline focus-visible:underline"
              >
                <span className="truncate">{block.title}</span>
                <ArrowUpRight className="size-3 shrink-0 text-fg-4 group-hover:text-fg" aria-hidden />
              </a>
            ) : (
              <span className="block truncate">{block.title}</span>
            )}
          </h4>
          {links.length > 0 && <LinkButtons links={links} />}
          {foldable && (
            <button
              type="button"
              onClick={toggle}
              // Enter is the arrow's own here: the inbox's Enter shortcut (open the focused ticket)
              // would otherwise take it and cancel the button's click.
              onKeyDown={(e) => e.key === "Enter" && e.stopPropagation()}
              aria-expanded={open}
              aria-controls={bodyId}
              aria-label={block.title}
              className="flex size-5 shrink-0 items-center justify-center rounded-md text-fg-4 outline-none group-hover/head:text-fg hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)]"
            >
              <ChevronRight className={cn("size-3.5 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-90")} aria-hidden />
            </button>
          )}
        </div>
        {block.subtitle && <p className="truncate text-[11.5px] text-fg-4">{block.subtitle}</p>}
        {badges.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {badges.map((b, i) => (
              <ToneBadge key={i} badge={b} />
            ))}
          </div>
        )}
      </div>
      {shown && (
        <div id={bodyId}>
          {fields.length > 0 && <FieldList fields={fields} className="mt-1.5" />}
          {lines.length > 0 && (
            <ul className="mt-1.5 flex flex-col gap-0.5 text-[11.5px] text-fg-3">
              {lines.map((l, i) => (
                <LineRow key={i} line={l} />
              ))}
            </ul>
          )}
          {footer.length > 0 && (
            <dl className="mt-1.5 flex flex-col gap-0.5 border-t border-line-soft pt-1.5">
              {footer.map((f, i) => (
                <div key={i} className="flex items-baseline gap-2 text-[12px]">
                  <dt className="min-w-0 flex-1 truncate text-fg-3">{f.label}</dt>
                  <dd className="shrink-0 font-medium text-fg-2">
                    <Out url={f.url}>
                      <PanelValueView value={f.value} />
                    </Out>
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      )}
    </article>
  );
}

// A link's icon: a generic mark or a product's shipped logo, both drawn by the app (the server
// lets no other name through, internal/integrations/panels linkIcons). Nothing is ever fetched
// from the link's own site: no favicon, no remote picture.
const LINK_ICONS: Record<string, LucideIcon> = {
  "external-link": ExternalLink,
  store: Store,
  "shopping-cart": ShoppingCart,
  receipt: Receipt,
  "file-text": FileText,
  truck: Truck,
  package: Package,
  user: User,
  building: Building,
  phone: Phone,
  mail: Mail,
  "credit-card": CreditCard,
  "map-pin": MapPin,
};

/** What a link's button shows: its product's logo, its icon, or the first letter of its label. */
export function LinkGlyph({ link }: { link: PanelLink }) {
  const Icon = link.icon ? LINK_ICONS[link.icon] : undefined;
  if (Icon) {
    return (
      <span aria-hidden className="flex size-5 shrink-0 items-center justify-center rounded-md border border-line-soft bg-surface text-fg-3 group-hover:text-fg">
        <Icon className="size-3" />
      </span>
    );
  }
  return <AppMark app={appLogo(link.icon) ? link.icon : undefined} name={link.label} tint="gray" size="sm" />;
}

/** The record in other tools: small buttons, each named and tipped by its label, opening a new tab. */
function LinkButtons({ links }: { links: PanelLink[] }) {
  return (
    <span className="flex shrink-0 items-center gap-1">
      {links.map((l, i) => (
        <Tooltip key={i} content={l.label}>
          <a
            href={l.url}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={l.label}
            className="group flex rounded-md outline-none transition-opacity hover:opacity-80 focus-visible:shadow-[0_0_0_2px_var(--focus)]"
          >
            <LinkGlyph link={l} />
          </a>
        </Tooltip>
      ))}
    </span>
  );
}

function LineRow({ line }: { line: PanelLine }) {
  return (
    <li className="flex gap-1.5">
      {line.quantity !== undefined && <span className="tabular-nums text-fg-4">{number(line.quantity)}×</span>}
      <span className="min-w-0 flex-1 truncate" title={line.subtitle ? line.title + " - " + line.subtitle : line.title}>
        <Out url={line.url}>
          {line.title}
          {line.subtitle ? <span className="text-fg-4"> - {line.subtitle}</span> : null}
        </Out>
      </span>
      {line.value && (
        <span className="shrink-0 text-fg-4">
          <PanelValueView value={line.value} />
        </span>
      )}
    </li>
  );
}

function ListBlock({ title, items }: { title?: string; items: PanelListItem[] }) {
  return (
    <div className="flex flex-col gap-1">
      {title && <h4 className="text-[11.5px] font-medium text-fg-2">{title}</h4>}
      <ul className="flex flex-col divide-y divide-line-soft">
        {items.map((it, i) => (
          <li key={i} className="flex items-start gap-2 py-1 text-[12px] first:pt-0 last:pb-0">
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-1.5">
                <div className="min-w-0 truncate text-fg-2" title={it.title}>
                  <Out url={it.url}>{it.title}</Out>
                </div>
                {it.links && it.links.length > 0 && <LinkButtons links={it.links} />}
              </div>
              {it.subtitle && <div className="truncate text-[11.5px] text-fg-4">{it.subtitle}</div>}
            </div>
            {(it.value || it.badge) && (
              <div className="flex shrink-0 flex-col items-end gap-0.5">
                {it.value && (
                  <span className="text-fg-2">
                    <PanelValueView value={it.value} />
                  </span>
                )}
                {it.badge && <ToneBadge badge={it.badge} />}
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

const NOTE_MARK: Record<Exclude<PanelTone, "neutral">, { icon: typeof Info; cls: string }> = {
  info: { icon: Info, cls: "text-tone-blue" },
  success: { icon: CircleCheck, cls: "text-tone-green" },
  warning: { icon: TriangleAlert, cls: "text-tone-amber" },
  danger: { icon: CircleAlert, cls: "text-tone-red" },
};

function NoteBlock({ text, tone }: { text: string; tone: PanelTone }) {
  const mark = tone === "neutral" ? undefined : NOTE_MARK[tone];
  const Icon = mark?.icon;
  return (
    <p className="flex items-start gap-1.5 text-[12px] text-fg-3">
      {Icon && <Icon className={cn("mt-px size-3.5 shrink-0", mark?.cls)} aria-hidden />}
      <span className="min-w-0">{text}</span>
    </p>
  );
}
