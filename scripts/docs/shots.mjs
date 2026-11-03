// Screenshots for the docs guides, taken from the real web app in mock mode.
//
//   cd web && VITE_MOCK=1 pnpm exec vite --port 5310        # the app, with sample data
//   node scripts/docs/shots.mjs                              # every file in scripts/docs/shots
//   node scripts/docs/shots.mjs scripts/docs/shots/inbox.mjs # one file
//   node scripts/docs/shots.mjs inbox/overview mailboxes/add # only these shots (any file)
//
// DOCS_SHOTS_URL points at another app (default http://127.0.0.1:5310).
// PLAYWRIGHT_EXECUTABLE_PATH runs an installed Chromium-based browser instead of
// Playwright's bundled one.
//
// A shot file exports `shots`, a list of:
//
//   {
//     name: 'mailboxes/add-button',   // docs/public/guides/mailboxes/add-button.webp (+ .dark.webp)
//     path: '/settings/mailboxes',    // where to start; ?mock_quiet=1 is added
//     run: async (page) => {},        // optional: click into the state to capture
//     highlight: [                    // optional: "where to click" marks, drawn over the app
//       { target: 'button:has-text("Add mailbox")', label: 1, note: 'Add mailbox' },
//       { target: (page) => page.getByRole('link', { name: 'Mailboxes' }), label: 2 },
//       { target: (page) => [a, b],   // several locators: one ring around all of them
//         badge: 'tr', side: 'left', inset: 6 },
//     ],
//     clip: '[role="dialog"]',        // optional: crop to an element (selector or fn), plus
//     pad: 24,                        //   the marks around it and `pad` px (default 24)
//     dim: true,                      // optional: soften everything outside the rings
//     viewport: { width: 1440, height: 900 },
//     themes: ['light', 'dark'],      // default: both
//   }
//
// A target is a selector string, a Locator, or a function of the page returning a
// Locator, a box {x, y, width, height}, or a list of those (one ring around all).
// `label` draws a numbered badge (`badge`: tl, tr, bl, br, l, r, tm, bm; default tl; it
// moves to the next free spot when that one would cover text) and `note`
// a short bubble beside the ring (`side`: right, left, top, bottom; default auto).
// `inset` is the room between the element and its ring: px, or { x, y } (default 4).
//
// Every shot is taken twice, light and dark, at 2x, with a frozen clock, a seeded
// Math.random, reduced motion and no live mock traffic, so reruns give the same picture.
// Output is WebP: full windows 1920 px wide, crops at their own 2x size (at most 1800).
import { chromium } from '../e2e/node_modules/playwright/index.mjs';
import { createRequire } from 'node:module';
import { mkdirSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const sharp = createRequire(path.join(root, 'docs/package.json'))('sharp');
const BASE = (process.env.DOCS_SHOTS_URL ?? 'http://127.0.0.1:5310').replace(/\/$/, '');
const OUT = path.join(root, 'docs/public/guides');
const SHOTS = path.join(root, 'scripts/docs/shots');
// A fixed moment for every shot: the mock's relative times ("6m", "Synced 2 minutes
// ago") and dates are computed from it.
const CLOCK = new Date('2026-10-13T09:42:00Z');
const FULL_WIDTH = 1920;
const CROP_MAX = 1800;

const args = process.argv.slice(2);
const files = args.filter((a) => a.endsWith('.mjs'));
const only = args.filter((a) => !a.endsWith('.mjs'));
const sources = files.length
  ? files.map((f) => path.resolve(f))
  : readdirSync(SHOTS).filter((f) => f.endsWith('.mjs')).sort().map((f) => path.join(SHOTS, f));

const all = [];
for (const file of sources) {
  const { shots } = await import(pathToFileURL(file).href);
  for (const s of shots) all.push({ ...s, file: path.basename(file) });
}
const todo = only.length ? all.filter((s) => only.includes(s.name)) : all;
if (!todo.length) {
  console.error(only.length ? `no shot named ${only.join(', ')}` : 'no shots found');
  process.exit(1);
}

try {
  const res = await fetch(BASE + '/');
  if (!res.ok) throw new Error(String(res.status));
} catch {
  console.error(`The app is not reachable at ${BASE}. Start it with:\n  cd web && VITE_MOCK=1 pnpm exec vite --port 5310`);
  process.exit(1);
}

// Palette for the marks: ink on light, paper on dark, as the app's own primary.
const MARKS = {
  light: { ring: '#0a0a0a', halo: 'rgb(10 10 10 / 0.10)', glow: 'rgb(10 10 10 / 0.18)', fg: '#ffffff', edge: '#ffffff', dim: 'rgb(10 10 10 / 0.10)' },
  dark: { ring: '#f5f5f6', halo: 'rgb(245 245 246 / 0.14)', glow: 'rgb(0 0 0 / 0.5)', fg: '#141416', edge: '#141416', dim: 'rgb(0 0 0 / 0.32)' },
};

// Steadies the page before a shot: no motion, no caret, no scrollbars, no tips.
const STEADY_CSS = `
  *, *::before, *::after {
    animation-duration: 0s !important; animation-delay: 0s !important;
    transition-duration: 0s !important; transition-delay: 0s !important;
    caret-color: transparent !important;
  }
  * { scrollbar-width: none !important; }
  ::-webkit-scrollbar { display: none !important; }
`;

async function resolveTarget(page, target) {
  const t = typeof target === 'function' ? await target(page) : target;
  if (typeof t === 'string') return resolveBoxes(page, [page.locator(t).first()]);
  if (Array.isArray(t)) return resolveBoxes(page, t);
  if (t && typeof t.boundingBox === 'function') return resolveBoxes(page, [t]);
  if (t && typeof t.x === 'number') return { box: t, radius: 8 };
  throw new Error('a highlight target must be a selector, a Locator, a list of Locators or a box');
}

async function resolveBoxes(page, locators) {
  const boxes = [];
  let radius = 0;
  for (const item of locators) {
    const l = await item;
    if (l && typeof l.x === 'number') {
      boxes.push(l);
      continue;
    }
    await l.waitFor({ state: 'visible', timeout: 10000 });
    const b = await l.boundingBox();
    if (!b) throw new Error(`no box for ${l}`);
    boxes.push(b);
    radius = Math.max(radius, await l.evaluate((el) => parseFloat(getComputedStyle(el).borderTopLeftRadius) || 0));
  }
  return { box: union(boxes), radius: locators.length > 1 ? 8 : radius };
}

function union(boxes) {
  const x = Math.min(...boxes.map((b) => b.x));
  const y = Math.min(...boxes.map((b) => b.y));
  const r = Math.max(...boxes.map((b) => b.x + b.width));
  const btm = Math.max(...boxes.map((b) => b.y + b.height));
  return { x, y, width: r - x, height: btm - y };
}

// Draws the rings, badges and notes as one fixed layer over the app.
async function drawMarks(page, marks, colors, dim) {
  return page.evaluate(
    ({ marks, colors, dim }) => {
      const vw = innerWidth;
      const vh = innerHeight;
      const layer = document.createElement('div');
      layer.id = 'od-docs-marks';
      layer.style.cssText = 'position:fixed;inset:0;z-index:2147483647;pointer-events:none;';
      const mono = '"Geist Mono Variable","Geist Mono",ui-monospace,monospace';
      const sans = '"Geist Variable","Geist",ui-sans-serif,system-ui,sans-serif';
      const rings = marks.map((m) => {
        const ix = typeof m.inset === 'object' ? m.inset.x : m.inset ?? 4;
        const iy = typeof m.inset === 'object' ? m.inset.y : m.inset ?? 4;
        const x = Math.max(2, m.box.x - ix);
        const y = Math.max(2, m.box.y - iy);
        const r = Math.min(vw - 2, m.box.x + m.box.width + ix);
        const b = Math.min(vh - 2, m.box.y + m.box.height + iy);
        return { ...m, x, y, w: r - x, h: b - y, rad: Math.min(14, Math.max(6, (m.radius || 6) + Math.min(ix, iy) - 1)) };
      });

      if (dim) {
        const ns = 'http://www.w3.org/2000/svg';
        const svg = document.createElementNS(ns, 'svg');
        svg.setAttribute('width', vw);
        svg.setAttribute('height', vh);
        svg.style.cssText = 'position:absolute;inset:0;';
        const mask = document.createElementNS(ns, 'mask');
        mask.id = 'od-docs-hole';
        const full = document.createElementNS(ns, 'rect');
        Object.entries({ width: vw, height: vh, fill: 'white' }).forEach(([k, v]) => full.setAttribute(k, v));
        mask.appendChild(full);
        for (const g of rings) {
          const hole = document.createElementNS(ns, 'rect');
          Object.entries({ x: g.x, y: g.y, width: g.w, height: g.h, rx: g.rad, fill: 'black' }).forEach(([k, v]) => hole.setAttribute(k, v));
          mask.appendChild(hole);
        }
        const defs = document.createElementNS(ns, 'defs');
        defs.appendChild(mask);
        svg.appendChild(defs);
        const shade = document.createElementNS(ns, 'rect');
        Object.entries({ width: vw, height: vh, fill: colors.dim, mask: 'url(#od-docs-hole)' }).forEach(([k, v]) => shade.setAttribute(k, v));
        svg.appendChild(shade);
        layer.appendChild(svg);
      }

      document.body.appendChild(layer);

      // Text on screen, so badges and notes can keep clear of it.
      const words = [];
      const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
        acceptNode: (n) => (n.textContent.trim() && !layer.contains(n.parentNode) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT),
      });
      const range = document.createRange();
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        range.selectNodeContents(n);
        for (const r of range.getClientRects()) {
          if (r.width && r.height && r.bottom > 0 && r.right > 0 && r.top < vh && r.left < vw) words.push(r);
        }
      }
      // Marks already placed, and every ring's edges: a badge should not sit on another ring.
      const taken = [];
      for (const g of rings) {
        taken.push({ x: g.x, y: g.y, w: g.w, h: 2, soft: true }, { x: g.x, y: g.y + g.h - 2, w: g.w, h: 2, soft: true });
        taken.push({ x: g.x, y: g.y, w: 2, h: g.h, soft: true }, { x: g.x + g.w - 2, y: g.y, w: 2, h: g.h, soft: true });
      }
      const overlap = (a, b) =>
        Math.max(0, Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x)) * Math.max(0, Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y));
      // Covering text or another badge costs most, crossing another ring's edge a little,
      // and each step down the list of spots a little more; the cheapest spot wins.
      const place = (spots, w, h) => {
        let best = null;
        spots.forEach(([sx, sy], rank) => {
          const x = Math.min(vw - w - 3, Math.max(3, sx));
          const y = Math.min(vh - h - 3, Math.max(3, sy));
          const box = { x: x - 2, y: y - 2, w: w + 4, h: h + 4 };
          let cost = rank * 20;
          for (const r of words) cost += 10 * overlap(box, { x: r.left, y: r.top, w: r.width, h: r.height });
          for (const t of taken) cost += (t.soft ? 1 : 10) * overlap(box, t);
          if (x !== sx || y !== sy) cost += 15; // pushed in from the window's edge
          if (!best || cost < best.cost) best = { x, y, cost };
        });
        taken.push({ x: best.x, y: best.y, w, h });
        return [best.x, best.y];
      };

      for (const g of rings) {
        const ring = document.createElement('div');
        ring.style.cssText = `position:absolute;left:${g.x}px;top:${g.y}px;width:${g.w}px;height:${g.h}px;box-sizing:border-box;border:2px solid ${colors.ring};border-radius:${g.rad}px;box-shadow:0 0 0 4px ${colors.halo},0 6px 22px -4px ${colors.glow};`;
        layer.appendChild(ring);

        if (g.label != null) {
          const size = 22;
          const badge = document.createElement('div');
          badge.textContent = String(g.label);
          // Badges sit on the ring's outside corner (or beside it), never on text: the
          // preferred spot unless it covers text or another mark, then the next one.
          const o = 7;
          const spots = {
            tl: [g.x - size + o, g.y - size + o],
            tr: [g.x + g.w - o, g.y - size + o],
            bl: [g.x - size + o, g.y + g.h - o],
            br: [g.x + g.w - o, g.y + g.h - o],
            l: [g.x - size - 5, g.y + g.h / 2 - size / 2],
            r: [g.x + g.w + 5, g.y + g.h / 2 - size / 2],
            tm: [g.x + g.w / 2 - size / 2, g.y - size + o],
            bm: [g.x + g.w / 2 - size / 2, g.y + g.h - o],
          };
          const order = [...new Set([g.badge ?? 'tl', 'tl', 'tr', 'bl', 'br', 'l', 'r', 'tm', 'bm'])];
          const [cx, cy] = place(order.map((k) => spots[k]), size, size);
          badge.style.cssText = `position:absolute;left:${cx}px;top:${cy}px;width:${size}px;height:${size}px;display:flex;align-items:center;justify-content:center;border-radius:7px;background:${colors.ring};color:${colors.fg};font:500 12px/1 ${mono};box-shadow:0 0 0 2px ${colors.edge},0 4px 12px -2px ${colors.glow};`;
          layer.appendChild(badge);
        }

        if (g.note) {
          const note = document.createElement('div');
          note.textContent = g.note;
          note.style.cssText = `position:absolute;left:0;top:0;white-space:nowrap;padding:5px 10px;border-radius:8px;background:${colors.ring};color:${colors.fg};font:500 12.5px/16px ${sans};letter-spacing:-0.005em;box-shadow:0 0 0 2px ${colors.edge},0 8px 20px -6px ${colors.glow};`;
          layer.appendChild(note);
          const nw = note.offsetWidth;
          const nh = note.offsetHeight;
          const gap = 12;
          const sides = {
            right: [g.x + g.w + gap, g.y + g.h / 2 - nh / 2],
            left: [g.x - gap - nw, g.y + g.h / 2 - nh / 2],
            bottom: [g.x + g.w / 2 - nw / 2, g.y + g.h + gap],
            top: [g.x + g.w / 2 - nw / 2, g.y - gap - nh],
          };
          const order = [...new Set([g.side ?? 'right', 'right', 'left', 'bottom', 'top'])];
          const [nx, ny] = place(order.map((k) => sides[k]), nw, nh);
          note.style.left = `${nx}px`;
          note.style.top = `${ny}px`;
        }
      }
      // Everything drawn, so a crop can keep the marks inside it.
      return [...layer.children]
        .filter((el) => el.tagName !== 'svg')
        .map((el) => {
          const r = el.getBoundingClientRect();
          return { x: r.x - 6, y: r.y - 6, width: r.width + 12, height: r.height + 12 };
        });
    },
    { marks, colors, dim },
  );
}

async function capture(browser, shot, theme) {
  const viewport = shot.viewport ?? { width: 1440, height: 900 };
  const ctx = await browser.newContext({
    viewport,
    deviceScaleFactor: 2,
    colorScheme: theme,
    reducedMotion: 'reduce',
    locale: 'en-US',
    timezoneId: 'Europe/London',
  });
  await ctx.addInitScript(
    ({ theme }) => {
      try {
        localStorage.setItem('od-theme', theme);
        localStorage.setItem('od-ollie-tips', JSON.stringify({ paused: 8.64e15 }));
      } catch {}
      // A seeded Math.random, so the mock's ids, avatars and sample data repeat.
      let s = 0x6f64;
      Math.random = () => {
        s = (s + 0x6d2b79f5) | 0;
        let t = Math.imul(s ^ (s >>> 15), 1 | s);
        t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
      };
    },
    { theme },
  );
  const page = await ctx.newPage();
  await page.clock.setFixedTime(CLOCK);
  try {
    const sep = shot.path.includes('?') ? '&' : '?';
    await page.goto(`${BASE}${shot.path}${sep}mock_quiet=1`, { waitUntil: 'networkidle', timeout: 30000 });
    await page.addStyleTag({ content: STEADY_CSS });
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(400);
    const ran = shot.run ? await shot.run(page, { theme }) : undefined;
    await page.waitForLoadState('networkidle');
    await page.evaluate(async () => {
      await document.fonts.ready;
      await Promise.all([...document.images].filter((i) => !i.complete).map((i) => new Promise((r) => { i.onload = i.onerror = r; })));
    });
    // Let the last state settle, and drop focus rings and hover from clicks.
    await page.mouse.move(viewport.width - 1, viewport.height - 1);
    // (Focus inside an open menu, popover or dialog stays, or it would close.)
    if (!shot.keepFocus)
      await page.evaluate(() => {
        const el = document.activeElement;
        if (!(el instanceof HTMLElement) || el.matches('input, textarea, [contenteditable]')) return;
        if (el.closest('[role="menu"], [role="dialog"], [role="listbox"], [data-radix-popper-content-wrapper]')) return;
        el.blur();
      });
    await page.waitForTimeout(350);

    const marks = [];
    for (const h of shot.highlight ?? []) {
      const { box, radius } = await resolveTarget(page, h.target);
      marks.push({ box, radius: h.radius ?? radius, label: h.label, note: h.note, badge: h.badge, side: h.side, inset: h.inset });
    }
    const drawn = marks.length ? await drawMarks(page, marks, MARKS[theme], shot.dim ?? false) : [];

    let clip;
    const clipTarget = shot.clip ?? (shot.narrow && ran && typeof ran.boundingBox === 'function' ? ran : undefined);
    if (clipTarget) {
      const { box } = await resolveTarget(page, clipTarget);
      const pad = shot.pad ?? 24;
      const u = union([box, ...drawn]);
      const x = Math.max(0, Math.floor(u.x - pad));
      const y = Math.max(0, Math.floor(u.y - pad));
      clip = {
        x,
        y,
        width: Math.min(viewport.width, Math.ceil(u.x + u.width + pad)) - x,
        height: Math.min(viewport.height, Math.ceil(u.y + u.height + pad)) - y,
      };
    }
    const png = await page.screenshot({ clip, animations: 'disabled', caret: 'hide' });
    const file = path.join(OUT, `${shot.name}${theme === 'dark' ? '.dark' : ''}.webp`);
    mkdirSync(path.dirname(file), { recursive: true });
    const width = clip ? Math.min(CROP_MAX, Math.round(clip.width * 2)) : FULL_WIDTH;
    const info = await sharp(png)
      .resize({ width, withoutEnlargement: true })
      .webp({ quality: 80, effort: 6, smartSubsample: true })
      .toFile(file);
    console.log('ok  ', path.relative(root, file), `${info.width}x${info.height}`, `${Math.round(info.size / 1024)} KB`);
    return true;
  } catch (e) {
    console.log('FAIL', `${shot.name} (${theme}, ${shot.file})`, String(e.message).split('\n').slice(0, 3).join(' | '));
    return false;
  } finally {
    await ctx.close();
  }
}

const jobs = todo.flatMap((shot) => (shot.themes ?? ['light', 'dark']).map((theme) => ({ shot, theme })));
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH || undefined });
let failed = 0;
const workers = Array.from({ length: Math.min(4, jobs.length) }, async () => {
  for (let job = jobs.shift(); job; job = jobs.shift()) {
    if (!(await capture(browser, job.shot, job.theme))) failed++;
  }
});
await Promise.all(workers);
await browser.close();
process.exit(failed ? 1 : 0);
