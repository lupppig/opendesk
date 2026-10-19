// Client-side sanitizing for stored HTML (articles, messages). The server
// already sanitizes before storage; this is a second, strict layer before any
// dangerouslySetInnerHTML.

import DOMPurify, { type Config } from "dompurify";
import { config } from "./config";

const ALLOWED_TAGS = [
  "a", "b", "strong", "i", "em", "u", "s", "strike", "del", "p", "br", "hr", "div", "span",
  "ul", "ol", "li", "blockquote", "pre", "code", "h1", "h2", "h3", "h4", "h5", "h6",
  "table", "thead", "tbody", "tfoot", "tr", "th", "td", "img", "figure", "figcaption", "sub", "sup", "small", "mark",
];
const ALLOWED_ATTR = ["href", "title", "alt", "src", "width", "height", "colspan", "rowspan", "class", "data-id", "target", "rel"];

// A designed email (sanitizeEmailHtml) keeps its styles, layout attributes and classes;
// it renders only in its own frame (EmailFrame), so none of it reaches the app.
const EMAIL_TAGS = [...ALLOWED_TAGS, "style", "center", "font", "caption", "col", "colgroup", "section", "article", "header", "footer", "abbr", "cite", "q", "tt", "big", "wbr", "dl", "dt", "dd"];
const EMAIL_ATTR = [...ALLOWED_ATTR, "style", "align", "valign", "bgcolor", "cellpadding", "cellspacing", "border", "color", "face", "size", "dir", "lang", "nowrap", "span", "scope"];

// In the app's own DOM a class could pick up the app's styles; only mentions keep theirs.
const APP_CLASSES = new Set(["mention"]);

// The pass in progress: whether classes stay, and whether remote images are held back
// (and how many were).
type Pass = { keepClasses: boolean; blockRemote: boolean; blocked: number };
let pass: Pass | null = null;

const CSS_URL = /url\s*\(\s*(['"]?)([^'"()]*)\1\s*\)/gi;

/** Our own images: data and blob URLs, and attachments served by the API (inline images, a mail account's signed files). */
function ownImage(src: string): boolean {
  if (/^(data|blob):/i.test(src.trim())) return true;
  try {
    const u = new URL(src, location.href);
    const api = new URL(config.apiUrl || location.origin, location.href);
    return (u.origin === api.origin || u.origin === location.origin) && (u.pathname.startsWith("/v1/attachments/") || u.pathname.startsWith("/v1/collab/attachments/"));
  } catch {
    return false;
  }
}

/**
 * CSS with every url() that is not our own image replaced, counted on the pass. It fails
 * closed: CSS that could load an image some other way, or a url() it cannot read, is dropped.
 */
function holdBackCSS(css: string, p: Pass): string {
  if (/image-set|@import|@font-face|\bsrc\s*\(/i.test(css)) {
    p.blocked++;
    return "";
  }
  let seen = 0;
  const out = css.replace(CSS_URL, (whole, _q: string, url: string) => {
    seen++;
    if (ownImage(url)) return whole;
    p.blocked++;
    return "none";
  });
  if ((css.match(/url\s*\(/gi) ?? []).length !== seen) {
    p.blocked++;
    return "";
  }
  return out;
}

let hooked = false;
function ensureHook() {
  if (hooked) return;
  hooked = true;
  DOMPurify.addHook("afterSanitizeElements", (node) => {
    if (pass?.blockRemote && node instanceof Element && node.tagName === "STYLE") {
      node.textContent = holdBackCSS((node.textContent ?? "").replace(/@import[^;]*;?/gi, ""), pass);
    }
  });
  DOMPurify.addHook("afterSanitizeAttributes", (node) => {
    if (!pass?.keepClasses && node.hasAttribute("class")) {
      const kept = (node.getAttribute("class") ?? "").split(/\s+/).filter((c) => APP_CLASSES.has(c));
      if (kept.length) node.setAttribute("class", kept.join(" "));
      else node.removeAttribute("class");
    }
    if (node.hasAttribute("style")) {
      const style = node.getAttribute("style") ?? "";
      // Stored CSS has no escapes (the server drops them), so every url() is in plain sight.
      if (style.includes("\\")) node.removeAttribute("style");
      else if (pass?.blockRemote) {
        const held = holdBackCSS(style, pass);
        if (held) node.setAttribute("style", held);
        else node.removeAttribute("style");
      }
    }
    if (node.tagName === "A") {
      const href = node.getAttribute("href") ?? "";
      if (/^\s*(javascript|data|vbscript):/i.test(href)) node.removeAttribute("href");
      node.setAttribute("target", "_blank");
      node.setAttribute("rel", "noopener noreferrer nofollow");
    }
    if (node.tagName === "IMG") {
      const src = node.getAttribute("src") ?? "";
      if (!/^(https?:|blob:|data:image\/(png|gif|jpe?g|webp|svg\+xml);|\/)/i.test(src)) node.removeAttribute("src");
      else if (pass?.blockRemote && !ownImage(src)) {
        node.removeAttribute("src");
        node.setAttribute("data-od-held", "");
        pass.blocked++;
      }
      node.setAttribute("loading", "lazy");
      node.setAttribute("referrerpolicy", "no-referrer");
    }
  });
}

const URI = /^(?:(?:https?|mailto|tel|blob):|data:image\/|[^a-z]|[a-z+.-]+(?:[^a-z+.\-:]|$))/i;

function run(html: string, p: Pass, cfg: Config): string {
  ensureHook();
  pass = p;
  try {
    return DOMPurify.sanitize(html ?? "", cfg);
  } finally {
    pass = null;
  }
}

export type Sanitized = { html: string; held: number };

export type SanitizeOptions = {
  /** Hold back images that would load from another site (an inbound email, until the reader loads them). */
  blockRemoteImages?: boolean;
};

/** HTML for the app's own DOM: plain formatting, no styles, only the app's own classes. */
export function sanitizeHtml(html: string): string {
  return sanitizeProse(html).html;
}

export function sanitizeProse(html: string, opts: SanitizeOptions = {}): Sanitized {
  const p: Pass = { keepClasses: false, blockRemote: !!opts.blockRemoteImages, blocked: 0 };
  const out = run(html, p, { ALLOWED_TAGS, ALLOWED_ATTR: [...ALLOWED_ATTR, "loading", "referrerpolicy"], ALLOW_DATA_ATTR: false, ALLOWED_URI_REGEXP: URI });
  return { html: out, held: p.blocked };
}

/** A designed email for its own frame (EmailFrame): styles, layout attributes and classes stay. */
export function sanitizeEmailHtml(html: string, opts: SanitizeOptions = {}): Sanitized {
  const p: Pass = { keepClasses: true, blockRemote: !!opts.blockRemoteImages, blocked: 0 };
  const out = run(html, p, {
    ALLOWED_TAGS: EMAIL_TAGS,
    ALLOWED_ATTR: [...EMAIL_ATTR, "loading", "referrerpolicy"],
    ALLOW_DATA_ATTR: false,
    FORCE_BODY: true,
    ALLOWED_URI_REGEXP: URI,
  });
  return { html: out, held: p.blocked };
}

/** A designed email: laid out with tables or painted backgrounds, which the app's prose would flatten. */
export function isDesignedEmail(html: string): boolean {
  return /<table[\s>]|\sbgcolor=|background(-color|-image)?\s*:/i.test(html ?? "");
}
