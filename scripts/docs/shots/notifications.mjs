// Shots for docs/src/content/docs/guides/notifications.mdx, keyboard-shortcuts.mdx and csat.mdx.
import { around, button, labelled, settle, text } from '../helpers.mjs';

const section = (p, title) => p.locator('section').filter({ has: p.locator('h2').filter({ hasText: new RegExp(`^\\s*${title}\\s*$`) }) }).first();
const bell = (p) => p.locator('header button[aria-label^="Notifications"]').first();
const panel = (p) => around(p, 'Notifications', 'Mark all read', 'Unread').last();

async function openBell(page) {
  await bell(page).click();
  await settle(page, 500);
}

const column = (p, name) => p.locator('[role="columnheader"]').filter({ hasText: new RegExp(`^\\s*${name}\\s*$`) }).first();
/** The header cells from `first` to `last`, a little inside their edges so rings on neighbouring columns keep apart. */
async function columns(p, first, last = first) {
  const a = await column(p, first).boundingBox();
  const b = await column(p, last).boundingBox();
  const row = await column(p, first).locator('xpath=..').boundingBox();
  return { x: a.x + 4, y: row.y + 5, width: b.x + b.width - a.x - 8, height: row.height - 10 };
}

async function openAccountMenu(page) {
  await page.getByText('maya@margin.app').last().click();
  await settle(page);
}

export const shots = [
  {
    name: 'notifications/bell',
    path: '/inbox/open',
    run: openBell,
    keepFocus: true,
    clip: (p) => [bell(p), panel(p)],
    pad: 24,
    highlight: [
      { target: bell, label: 1, badge: 'l' },
      { target: (p) => around(p, 'All', 'Unread').last(), label: 2, inset: 3, badge: 'tr' },
      { target: (p) => button(p, 'Mark all read'), label: 3, inset: 3, badge: 'tl' },
      { target: (p) => p.locator('[aria-label="Unread"][role="img"]').first(), label: 4, inset: 6, badge: 'r' },
      { target: (p) => around(p, 'SLA breached', '#1067').last(), label: 5, inset: 2, badge: 'l' },
    ],
  },
  {
    name: 'notifications/settings',
    path: '/settings/notifications',
    viewport: { width: 1440, height: 1100 },
    clip: (p) => [section(p, 'Email schedule'), section(p, 'Pause notifications')],
    pad: 24,
    highlight: [
      { target: (p) => p.locator('[role="radiogroup"]').first(), label: 1, inset: 2, badge: 'l' },
      { target: (p) => around(p, 'Sent to', 'Send me a test email').last(), label: 2, inset: 2, badge: 'l' },
      { target: (p) => button(p, 'Pause'), label: 3, inset: 4, badge: 'l' },
    ],
  },
  {
    name: 'notifications/types',
    path: '/settings/notifications',
    viewport: { width: 1440, height: 1800 },
    clip: (p) => section(p, 'What reaches you where'),
    pad: 24,
    highlight: [
      { target: (p) => columns(p, 'In the app'), label: 1, inset: 0, badge: 'tl' },
      { target: (p) => columns(p, 'Email', 'Desktop'), label: 2, inset: 0, badge: 'tr' },
    ],
  },
  {
    name: 'notifications/unsubscribe',
    path: '/unsubscribe?token=demo',
    clip: (p) => around(p, 'Stop notification emails?', 'Unsubscribe'),
    pad: 28,
    highlight: [{ target: (p) => button(p, 'Unsubscribe'), label: 1, inset: 4, badge: 'l' }],
  },
  {
    name: 'shortcuts/sheet',
    path: '/inbox/open',
    run: async (page) => {
      await page.keyboard.press('Shift+Slash');
      await settle(page, 500);
    },
    keepFocus: true,
    clip: (p) => p.getByRole('dialog'),
    pad: 20,
  },
  {
    name: 'shortcuts/menu',
    path: '/inbox/open',
    run: openAccountMenu,
    keepFocus: true,
    clip: () => [{ x: 0, y: 420, width: 1, height: 1 }, { x: 300, y: 899, width: 1, height: 1 }],
    pad: 0,
    highlight: [{ target: (p) => around(p, 'Keyboard shortcuts', '?').last(), label: 1, inset: 2, badge: 'r' }],
  },
  {
    name: 'csat/setting',
    path: '/settings/workspace',
    viewport: { width: 1440, height: 1400 },
    clip: (p) => section(p, 'Conversations'),
    pad: 24,
    highlight: [{ target: (p) => around(p, 'Satisfaction survey', 'Ask customers to rate').last().locator('xpath=..'), label: 1, inset: { x: 8, y: 2 }, badge: 'tr' }],
  },
  {
    name: 'csat/survey',
    path: '/csat/demo',
    run: async (page) => {
      await page.getByRole('radio', { name: /Great/ }).click();
      await page.locator('#csat-comment').fill('Quick and clear, thank you.');
    },
    clip: (p) => [p.getByText('How was your conversation?').first(), button(p, 'Send feedback')],
    pad: 48,
    highlight: [
      { target: (p) => p.locator('[role="radiogroup"]'), label: 1, inset: 6, badge: 'l' },
      { target: (p) => p.locator('#csat-comment'), label: 2, inset: 6, badge: 'l' },
      { target: (p) => button(p, 'Send feedback'), label: 3, inset: 4, badge: 'l' },
    ],
  },
  {
    name: 'csat/ticket',
    path: '/inbox/solved',
    viewport: { width: 1440, height: 2000 },
    run: async (page) => {
      await page.getByText('How do I invite my team?').first().click();
      await settle(page, 700);
    },
    clip: (p) => [around(p, 'SLA', 'First response', 'Resolution').last(), around(p, '5/5', 'Super quick, thanks Sam!').last()],
    pad: 28,
    highlight: [
      { target: (p) => around(p, '5/5', 'Super quick, thanks Sam!').last(), label: 1, inset: 4, badge: 'l' },
    ],
  },
  {
    name: 'csat/report',
    path: '/reports',
    clip: (p) => around(p, 'CSAT', '/ 5').last().locator('xpath=..'),
    pad: 20,
    highlight: [{ target: (p) => around(p, 'CSAT', '/ 5').last(), label: 1, inset: 2, badge: 'tr' }],
  },
];
