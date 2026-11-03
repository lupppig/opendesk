// Photographs the social card (src/pages/og) into public/og.jpg, and copies it to
// brand/social/og.jpg (GitHub's social preview) and docs/public/og.jpg. Then renders
// the 1:1 profile pictures into brand/social as JPEG: avatar.jpg and avatar-light.jpg from
// the SVGs scripts/build-brand.mjs writes, and avatar-painted.jpg, the mark on a glass tile
// over the card's painting. Run it after changing the hero's app window: pnpm og
//
// It starts its own dev server with OG_CARDS=1 on a free port, beside any you have
// running, and stops it after.
// Needs Chromium for Playwright once: pnpm exec playwright-core install chromium,
// or PLAYWRIGHT_EXECUTABLE_PATH set to an installed Chromium-based browser (Brave, Chrome).
import { spawn } from 'node:child_process';
import { copyFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright-core';
import sharp from 'sharp';

const site = join(dirname(fileURLToPath(import.meta.url)), '..');
const out = join(site, 'public/og.jpg');
const SCALE = 1.6; // 1920x1008, the size large previews are drawn at

const port = await new Promise((resolve) => {
  const s = createServer().listen(0, '127.0.0.1', () => {
    const { port } = s.address();
    s.close(() => resolve(port));
  });
});
const base = `http://127.0.0.1:${port}`;

const dev = spawn(join(site, 'node_modules/.bin/astro'), ['dev', '--ignore-lock', '--host', '127.0.0.1', '--port', String(port)], {
  cwd: site,
  env: { ...process.env, OG_CARDS: '1' },
  stdio: ['ignore', 'ignore', 'inherit'],
});
const stop = () => dev.exitCode === null && dev.kill('SIGTERM');
process.on('exit', stop);

async function ready() {
  for (let i = 0; i < 240; i++) {
    try {
      if ((await fetch(`${base}/og/`)).ok) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error('the dev server did not start');
}

let browser;
try {
  await ready();
  browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH || undefined });
  const page = await browser.newPage({ viewport: { width: 1200, height: 630 }, deviceScaleFactor: SCALE, reducedMotion: 'reduce', colorScheme: 'light' });
  // The first visit can be cut short while Vite optimises dependencies and reloads.
  for (let i = 0; ; i++) {
    try {
      await page.goto(`${base}/og/`, { waitUntil: 'load' });
      break;
    } catch (err) {
      if (i === 3) throw err;
      await page.waitForTimeout(1500);
    }
  }
  await page.evaluate(async () => {
    await document.fonts.ready;
    await Promise.all([...document.images].map((img) => img.decode().catch(() => {})));
  });
  await page.locator('[data-og-card]').screenshot({ path: out, type: 'jpeg', quality: 84 });
  for (const copy of ['../brand/social/og.jpg', '../docs/public/og.jpg']) copyFileSync(out, join(site, copy));
  console.log('[og] public/og.jpg, brand/social/og.jpg, docs/public/og.jpg');
} finally {
  await browser?.close();
  stop();
}

const social = join(site, '../brand/social');
for (const name of ['avatar', 'avatar-light']) {
  await sharp(join(social, `${name}.svg`)).resize(1024, 1024).jpeg({ quality: 92, chromaSubsampling: '4:4:4' }).toFile(join(social, `${name}.jpg`));
}
await paintedAvatar(join(site, 'src/assets/backdrops/roadside.webp'), join(social, 'avatar-painted.jpg'));
console.log('[og] brand/social/avatar.jpg, avatar-light.jpg, avatar-painted.jpg');

// The mark on a frosted tile over a painting, like the covers on the blog.
async function paintedAvatar(painting, out) {
  const S = 1024, T = 560, o = (S - T) / 2, r = T * 0.28, m = T * 0.5;
  const MARK = 'M11 4h12a7 7 0 0 1 7 7v12a7 7 0 0 1-7 7h-12a7 7 0 0 1-7-7v-12a7 7 0 0 1 7-7Z M47 4a13 13 0 1 1 0 26a13 13 0 1 1 0-26Z M11 34h12a7 7 0 0 1 7 7v12a7 7 0 0 1-7 7h-12a7 7 0 0 1-7-7v-12a7 7 0 0 1 7-7Z M41 34h12a7 7 0 0 1 7 7v12a7 7 0 0 1-7 7h-12a7 7 0 0 1-7-7v-12a7 7 0 0 1 7-7Z';
  const base = await sharp(painting).resize(S, S, { fit: 'cover', position: 'top' }).toBuffer();
  const tile = Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="${T}" height="${T}"><rect width="${T}" height="${T}" rx="${r}" fill="#fff"/></svg>`);
  const frosted = await sharp(base).extract({ left: o, top: o, width: T, height: T }).blur(28)
    .composite([{ input: tile, blend: 'dest-in' }]).png().toBuffer();
  const glass = Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="${S}" height="${S}">
    <rect x="${o}" y="${o}" width="${T}" height="${T}" rx="${r}" fill="#fff" fill-opacity="0.58" stroke="#fff" stroke-opacity="0.65" stroke-width="3"/>
    <path transform="translate(${(S - m) / 2} ${(S - m) / 2}) scale(${m / 64})" fill="#0A0A0A" fill-rule="evenodd" d="${MARK}"/></svg>`);
  await sharp(await sharp(base).composite([{ input: frosted, left: o, top: o }, { input: glass }]).png().toBuffer())
    .jpeg({ quality: 90, chromaSubsampling: '4:4:4' }).toFile(out);
}
