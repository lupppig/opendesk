// Mock ticket panels (contracts/integrations.md "Ticket panels"): a seeded Shopify store
// whose orders are made up from the contact's email, and http panels (a seeded ERP) that
// answer version 2 blocks for the references in the ticket's subject. Nothing is fetched;
// credentials are only checked as given, never kept.

import type { PanelBlock, PanelOrder, ShopifyField, TicketPanel, TicketPanelData } from "@/lib/api/types";
import { DEFAULT_SHOPIFY_FIELDS, isShopifyField, MAX_ORDERS_SHOWN, normalizeFields, panelShows, SHOPIFY_FIELDS, shownData } from "@/lib/panelFields";
import { DEFAULT_REFERENCE_PATTERN, findReferences, MAX_PATTERN_LENGTH, referenceRegExp } from "@/lib/panelReferences";
import { db, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, forbidden, get, invalid, notFound, patch, post, respond, type Ctx } from "../router";
import { findTicket } from "../tickets";
import { nowIso, uuid } from "../util";

const panelsOf = new Map<string, TicketPanel[]>();

function seed(): TicketPanel[] {
  const at = nowIso();
  return [
    {
      id: uuid(),
      kind: "shopify",
      name: "Margin Store",
      host: "margin-store.myshopify.com",
      auth: "client_credentials",
      client_id: "3f9c0d1e7a2b4c5d8e6f",
      fields: [...DEFAULT_SHOPIFY_FIELDS],
      orders_shown: MAX_ORDERS_SHOWN,
      enabled: true,
      position: 0,
      status: "connected",
      last_error: "",
      created_at: at,
      updated_at: at,
    },
    {
      id: uuid(),
      kind: "http",
      name: "Odoo",
      host: "erp.example.com",
      fields: [],
      reference_pattern: DEFAULT_REFERENCE_PATTERN,
      enabled: true,
      position: 1,
      status: "connected",
      last_error: "",
      created_at: at,
      updated_at: at,
    },
  ];
}

export function panelsState(ws: WsData): TicketPanel[] {
  let list = panelsOf.get(ws.workspace.id);
  if (!list) {
    list = seed();
    panelsOf.set(ws.workspace.id, list);
  }
  return list;
}

/** A Shopify panel's credentials, as the server reads them: an app's client ID and secret, or a token. */
function shopifyAuth(b: Record<string, unknown>): Pick<TicketPanel, "auth" | "client_id"> {
  const id = String(b.client_id ?? "").trim();
  const secret = String(b.client_secret ?? "").trim();
  const token = String(b.token ?? "").trim();
  if (token && (id || secret)) throw invalid("Give the app's client ID and secret, or an access token, not both.");
  if (token) return { auth: "access_token", client_id: undefined };
  if (!id || !secret) throw invalid("Paste the app's client ID and client secret.");
  return { auth: "client_credentials", client_id: id };
}

function admin(ws: WsData) {
  const r = ws.members.find((m) => m.user_id === db.meId)?.role;
  if (r !== "owner" && r !== "admin") throw forbidden("Only admins can manage ticket panels.");
}

function find(ctx: Ctx): TicketPanel {
  const p = panelsState(ctx.ws).find((x) => x.id === ctx.params.id);
  if (!p) throw notFound("Ticket panel");
  return p;
}

function audit(ws: WsData, action: string, p: TicketPanel) {
  ws.audit.unshift({ id: uuid(), actor: { type: "user", user: userRef(db.meId), api_key_id: null }, action, entity_type: "ticket_panel", entity_id: p.id,
    data: { kind: p.kind, host: p.host }, ip: "84.152.33.17", created_at: nowIso() });
}

/** A chosen list of fields, as the server checks it: from the catalogue, unit prices with their lines. */
function fieldsOf(raw: unknown): ShopifyField[] {
  if (!Array.isArray(raw)) throw invalid("fields is a list.");
  const bad = raw.find((f) => !isShopifyField(f));
  if (bad !== undefined) throw invalid(`Unknown field "${String(bad)}": choose from ${SHOPIFY_FIELDS.join(", ")}.`);
  const list = raw as ShopifyField[];
  if (list.includes("line_prices") && !list.includes("line_items")) throw invalid("line_prices needs line_items: a unit price shows on its line.");
  return normalizeFields(list);
}

function ordersShownOf(raw: unknown): number {
  if (typeof raw !== "number" || !Number.isInteger(raw) || raw < 1 || raw > MAX_ORDERS_SHOWN) throw invalid("orders_shown is 1 to 5.");
  return raw;
}

const SHOP = /^[a-z0-9][a-z0-9-]{0,59}\.myshopify\.com$/;

function shopOf(raw: unknown): string {
  let s = String(raw ?? "").trim().toLowerCase();
  if (s.includes("://")) {
    try {
      s = new URL(s).host;
    } catch {
      s = "";
    }
  }
  if (s && !s.includes(".")) s += ".myshopify.com";
  if (!SHOP.test(s)) throw invalid("Use the shop's myshopify.com domain, such as my-store.myshopify.com.");
  return s;
}

function urlOf(raw: unknown): string {
  const s = String(raw ?? "").trim();
  if (!/^https:\/\/[^/\s]+/.test(s)) throw new HttpError(422, "invalid_url", "The URL must use https://.");
  return s;
}

/** A reference pattern as the server checks it (Go's RE2 reads the usual patterns like the browser). */
function patternOf(raw: unknown): string {
  if (typeof raw !== "string") throw invalid("reference_pattern is a string.");
  const p = raw.trim();
  if (p.length > MAX_PATTERN_LENGTH) throw invalid("The reference pattern is longer than 200 characters.");
  if (p && !referenceRegExp(p)) throw invalid("The reference pattern is not a valid regular expression (RE2 syntax), or matches an empty text.");
  return p;
}

const secret = () => `pnsec_${uuid().replace(/-/g, "")}`;

function hash(s: string) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

const PRODUCTS = [
  { title: "Lucide Leanne Wall Lamp", variant: "Black", price: "14.44" },
  { title: "Arco floor lamp", variant: "Marble", price: "1890.00" },
  { title: "Tolomeo desk lamp", variant: "Aluminium", price: "289.00" },
  { title: "Flowerpot VP3", variant: "Mint", price: "159.00" },
  { title: "LED bulb E27", price: "12.90" },
];

const DESTINATIONS = [
  { city: "Brussels", country: "BE", method: "DHL Express" },
  { city: "Antwerp", country: "BE", method: "bpost" },
  { city: "Lyon", country: "FR", method: "Colissimo" },
  { city: "Rotterdam", country: "NL", method: "PostNL" },
];

/**
 * A made-up store history for an email: the same every time, none for some addresses. The
 * newest order may carry a code and a partial refund; a third order back is cancelled.
 */
function ordersFor(email: string, shop: string): { orders: PanelOrder[]; spent: number } | null {
  const h = hash(email);
  if (h % 5 === 0) return null;
  const n = 1 + (h % 3);
  const handle = shop.replace(/\.myshopify\.com$/, "");
  const orders: PanelOrder[] = [];
  let spent = 0;
  for (let i = 0; i < n; i++) {
    const a = PRODUCTS[(h + i) % PRODUCTS.length];
    const b = PRODUCTS[(h + i + 2) % PRODUCTS.length];
    const items = [{ title: a.title, variant: a.variant, quantity: 1 + ((h >> i) % 3), price: a.price }];
    if ((h >> i) % 2 === 0) items.push({ title: b.title, variant: b.variant, quantity: 1, price: b.price });
    const gross = items.reduce((s, it) => s + Number(it.price) * it.quantity, 0);
    const id = 5000 + (h % 900) + i;
    const created = Date.now() - (3 + i * 17 + (h % 5)) * 86_400_000;
    const cancelled = i === 2;
    const codes = i === 0 && h % 2 === 0 ? ["WELCOME10"] : [];
    const partly = i === 0 && (h >> 3) % 2 === 0;
    const refunded = cancelled ? gross : partly ? Number(items[0]!.price) : 0;
    const total = gross * (codes.length ? 0.9 : 1) - refunded;
    spent += total;
    const fulfilled = !cancelled && (i > 0 || h % 2 === 0);
    const dest = DESTINATIONS[(h + i) % DESTINATIONS.length]!;
    orders.push({
      id: `gid://shopify/Order/${id}`,
      number: `#${1000 + (h % 400) + n - i}`,
      url: `https://admin.shopify.com/store/${handle}/orders/${id}`,
      created_at: new Date(created).toISOString(),
      total: Math.max(total, 0).toFixed(2),
      currency: "EUR",
      financial_status: cancelled ? "refunded" : partly ? "partially_refunded" : "paid",
      fulfillment_status: fulfilled ? "fulfilled" : "unfulfilled",
      cancelled,
      cancelled_at: cancelled ? new Date(created + 86_400_000).toISOString() : undefined,
      refunded: refunded > 0 ? refunded.toFixed(2) : undefined,
      discount_codes: codes,
      shipping: dest,
      items,
      tracking: fulfilled ? [{ company: "DHL Express", number: `JD01460000${id}`, url: `https://www.dhl.com/track?tracking-id=JD01460000${id}` }] : [],
    });
  }
  return { orders, spent };
}

const iso = (days: number) => new Date(Date.now() + days * 86_400_000).toISOString().slice(0, 10);

/**
 * What a URL of the workspace's own might answer: an ERP's order (found by the first reference in
 * the ticket, else made up from its number) with a supplier running late, an older order folded, the contact's
 * subscription and invoices, and a note.
 */
function blocksFor(p: TicketPanel, t: { number: number; subject: string }): PanelBlock[] {
  const re = referenceRegExp(p.reference_pattern ?? DEFAULT_REFERENCE_PATTERN);
  const ref = (re && findReferences(re, t.subject)[0]) || `S${String(42 + (t.number % 300)).padStart(5, "0")}`;
  const so = 42 + (t.number % 300);
  return [
    {
      type: "card",
      id: `so-${so}`,
      title: `Sales order ${ref}`,
      url: `https://erp.example.com/odoo/sales/${so}`,
      subtitle: "Web shop · ships from Milan, Italy",
      badges: [
        { text: "Supplier late", tone: "warning" },
        { text: "Paid", tone: "success" },
      ],
      links: [
        { label: "Open in the shop", url: `https://admin.shopify.com/store/lumen-store/orders/${5000 + so}`, icon: "shopify" },
        { label: "Open in Odoo", url: `https://erp.example.com/odoo/sales/${so}`, icon: "odoo" },
      ],
      fields: [
        { label: "Promised", value: { type: "date", value: iso(2), relative: true } },
        { label: "Supplier date", value: { type: "date", value: iso(9), relative: true } },
        { label: "Supplier", value: { type: "text", text: "Flos S.p.A." }, url: "https://erp.example.com/odoo/partners/88" },
      ],
      lines: [
        { quantity: 2, title: "Arco floor lamp", subtitle: "Marble", value: { type: "money", amount: "1890.00", currency: "EUR" } },
        { quantity: 4, title: "LED bulb E27", subtitle: "2700 K", value: { type: "money", amount: "12.90", currency: "EUR" } },
      ],
      footer: [{ label: "Total", value: { type: "money", amount: "3831.60", currency: "EUR" } }],
    },
    {
      // An older order, sent folded: the agent opens it when it matters.
      type: "card",
      id: `so-${so - 7}`,
      title: `Sales order S${String(so - 7).padStart(5, "0")}`,
      url: `https://erp.example.com/odoo/sales/${so - 7}`,
      subtitle: "Web shop · delivered",
      collapsed: true,
      badges: [{ text: "Delivered", tone: "success" }],
      links: [{ label: "Open in Odoo", url: `https://erp.example.com/odoo/sales/${so - 7}`, icon: "odoo" }],
      fields: [{ label: "Delivered", value: { type: "date", value: iso(-34) } }],
      lines: [{ quantity: 1, title: "Snoopy table lamp", subtitle: "Black", value: { type: "money", amount: "1150.00", currency: "EUR" } }],
      footer: [{ label: "Total", value: { type: "money", amount: "1150.00", currency: "EUR" } }],
    },
    {
      type: "fields",
      title: "Subscription",
      items: [
        { label: "Plan", value: { type: "badge", text: "Pro yearly", tone: "info" } },
        { label: "Renews", value: { type: "date", value: iso(23), relative: true } },
        { label: "Amount", value: { type: "money", amount: "490", currency: "EUR" } },
      ],
    },
    {
      type: "list",
      title: "Invoices",
      items: [
        { title: "INV/2026/00012", subtitle: "Pro yearly", url: "https://erp.example.com/odoo/invoices/12", value: { type: "money", amount: "490.00", currency: "EUR" }, badge: { text: "Overdue", tone: "danger" } },
        { title: "INV/2026/00009", subtitle: `For ${ref}`, url: "https://erp.example.com/odoo/invoices/9", value: { type: "money", amount: "3831.60", currency: "EUR" }, badge: { text: "Paid", tone: "success" } },
      ],
    },
    { type: "note", text: "Credit note RINV/2026/0003 is waiting for approval.", tone: "info" },
  ];
}

function dataFor(p: TicketPanel, t: { id: string; number: number; subject: string; contact: { email: string | null; name: string } | null }): TicketPanelData {
  const base: TicketPanelData = { panel_id: p.id, kind: p.kind, state: "ok", fetched_at: nowIso() };
  const email = t.contact?.email?.toLowerCase() ?? "";
  if (p.kind === "shopify") {
    if (!email) return { ...base, state: "no_contact" };
    const found = ordersFor(email, p.host);
    if (!found) return { ...base, state: "empty" };
    const handle = p.host.replace(/\.myshopify\.com$/, "");
    const { fields, ordersShown } = panelShows(p);
    // Only what the panel shows, as the server asks Shopify for nothing else.
    return shownData(
      {
        ...base,
        customer: {
          name: t.contact?.name || email,
          url: `https://admin.shopify.com/store/${handle}/customers/${7000 + (hash(email) % 900)}`,
          email,
          orders_count: found.orders.length + (hash(email) % 4),
          total_spent: found.spent.toFixed(2),
          currency: "EUR",
          // As stores tag customers: mostly marketing.
          tags: hash(email) % 3 === 0 ? ["VIP", "newsletter"] : ["footer_signup", "Login with Shop"],
        },
        orders: found.orders,
      },
      fields,
      ordersShown,
    );
  }
  return { ...base, blocks: blocksFor(p, t) };
}

export function registerTicketPanels() {
  get("/v1/ticket-panels", (ctx) => ({ data: [...panelsState(ctx.ws)].sort((a, b) => a.position - b.position) }));

  post("/v1/ticket-panels", (ctx) => {
    admin(ctx.ws);
    const list = panelsState(ctx.ws);
    if (list.length >= 10) throw new HttpError(409, "conflict", "A workspace can have at most 10 ticket panels.");
    const kind = ctx.body.kind;
    const at = nowIso();
    let p: TicketPanel;
    let s: string | undefined;
    if (kind === "shopify") {
      if (ctx.body.reference_pattern !== undefined) throw invalid("Only an http panel finds references.");
      const host = shopOf(ctx.body.shop);
      const auth = shopifyAuth(ctx.body);
      const fields = ctx.body.fields === undefined ? [...DEFAULT_SHOPIFY_FIELDS] : fieldsOf(ctx.body.fields);
      const orders_shown = ctx.body.orders_shown === undefined ? MAX_ORDERS_SHOWN : ordersShownOf(ctx.body.orders_shown);
      p = { id: uuid(), kind, name: String(ctx.body.name ?? "").trim() || "Margin Store", host, ...auth, fields, orders_shown, enabled: true, position: list.length, status: "connected", last_error: "", created_at: at, updated_at: at };
    } else if (kind === "http") {
      if (ctx.body.fields !== undefined || ctx.body.orders_shown !== undefined) throw invalid("Only a Shopify panel has fields to choose.");
      const url = urlOf(ctx.body.url);
      const reference_pattern = ctx.body.reference_pattern === undefined ? DEFAULT_REFERENCE_PATTERN : patternOf(ctx.body.reference_pattern);
      p = { id: uuid(), kind, name: String(ctx.body.name ?? "").trim() || new URL(url).host, host: new URL(url).host, fields: [], reference_pattern, enabled: true, position: list.length, status: "connected", last_error: "", created_at: at, updated_at: at };
      s = secret();
    } else throw invalid("A panel is shopify or http.");
    list.push(p);
    publish(ctx.ws, "ticket_panel.created", { panel: p });
    audit(ctx.ws, "ticket_panel.connected", p);
    return respond(201, s ? { panel: p, secret: s } : { panel: p });
  });

  patch("/v1/ticket-panels/:id", (ctx) => {
    admin(ctx.ws);
    const p = find(ctx);
    const b = ctx.body;
    if (typeof b.name === "string") p.name = b.name.trim().slice(0, 64) || p.name;
    if (typeof b.enabled === "boolean") p.enabled = b.enabled;
    if (typeof b.position === "number") p.position = b.position;
    if (b.fields !== undefined || b.orders_shown !== undefined) {
      if (p.kind !== "shopify") throw invalid("Only a Shopify panel has fields to choose.");
      if (b.fields !== undefined) p.fields = fieldsOf(b.fields);
      if (b.orders_shown !== undefined) p.orders_shown = ordersShownOf(b.orders_shown);
    }
    if (b.token !== undefined || b.client_id !== undefined || b.client_secret !== undefined) {
      if (p.kind !== "shopify") throw invalid("Only a Shopify panel has credentials.");
      // A rotated secret keeps the app's client ID.
      Object.assign(p, shopifyAuth(b.token !== undefined ? b : { client_id: p.client_id, ...b }));
      p.status = "connected";
      p.last_error = "";
    }
    if (b.reference_pattern !== undefined) {
      if (p.kind !== "http") throw invalid("Only an http panel finds references.");
      p.reference_pattern = patternOf(b.reference_pattern);
    }
    if (b.url !== undefined) {
      if (p.kind !== "http") throw invalid("Only an http panel has a URL.");
      p.host = new URL(urlOf(b.url)).host;
      p.status = "connected";
      p.last_error = "";
    }
    p.updated_at = nowIso();
    publish(ctx.ws, "ticket_panel.updated", { panel: p });
    audit(ctx.ws, "ticket_panel.updated", p);
    return p;
  });

  post("/v1/ticket-panels/:id/rotate-secret", (ctx) => {
    admin(ctx.ws);
    const p = find(ctx);
    if (p.kind !== "http") throw invalid("Only an http panel has a signing secret.");
    p.updated_at = nowIso();
    publish(ctx.ws, "ticket_panel.updated", { panel: p });
    return { panel: p, secret: secret() };
  });

  del("/v1/ticket-panels/:id", (ctx) => {
    admin(ctx.ws);
    const p = find(ctx);
    panelsOf.set(ctx.ws.workspace.id, panelsState(ctx.ws).filter((x) => x !== p));
    publish(ctx.ws, "ticket_panel.deleted", { panel_id: p.id });
    audit(ctx.ws, "ticket_panel.disconnected", p);
    return respond(204);
  });

  get("/v1/tickets/:id/panels/:panelId", (ctx) => {
    const t = findTicket(ctx.ws, ctx.params.id);
    if (!t) throw notFound("Ticket");
    const p = panelsState(ctx.ws).find((x) => x.id === ctx.params.panelId && x.enabled);
    if (!p) throw notFound("Ticket panel");
    return dataFor(p, t);
  });
}
