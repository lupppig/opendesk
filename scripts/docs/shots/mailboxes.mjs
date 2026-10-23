// Shots for docs/src/content/docs/guides/mailboxes/ ("Connect a mailbox" and its sub-guides).
import { around, button, labelled, settle, text } from '../helpers.mjs';

// The add-mailbox wizard card: its stepper and everything under it.
const wizard = (page) => page.locator('ol[aria-label="Add a mailbox"]').locator('xpath=..');
const tiles = (page) => around(page, 'Mailboxes', 'Active', 'Disabled', 'Open to see the error');
const ways = (page) => page.getByRole('radiogroup');
const way = (page, name) => page.getByRole('radio', { name });
const emailField = (page) => page.getByLabel('Email address', { exact: true });
const nameField = (page) => page.getByLabel('Name', { exact: true });
// The detected provider: its logo, name and where the answer came from.
const detection = (page, source) => page.getByText(source, { exact: true }).locator('xpath=ancestor::div[contains(@class, "rounded-lg")][1]');
const fieldset = (page, legend) => page.locator('fieldset').filter({ hasText: legend });
const appPassword = (page) => page.getByLabel('App password', { exact: true }).locator('xpath=..');
const servers = (page) => text(page, 'Server settings, set for you').locator('xpath=ancestor::div[contains(@class, "rounded-lg")][1]');
const picker = (page) => text(page, 'How should opendesk connect?').locator('xpath=..');

/** Hides parts of the page next to a crop, so the crop's edge shows no slice of them. */
async function hide(locator) {
  await locator.evaluate((el) => (el.style.visibility = 'hidden'));
}

/** Hides what sits above the ways in (the detected provider), for crops of the ways alone. */
const hideAbove = (page) => hide(picker(page).locator('xpath=preceding-sibling::*[1]'));

/** Hides `outer` but keeps the element matching `inner`, inside it, in view. */
async function only(outer, inner) {
  await outer.evaluate((el, sel) => {
    el.style.visibility = 'hidden';
    el.querySelector(sel).style.visibility = 'visible';
  }, inner);
}

/** The box around `locators`, with its own room on each side: [top, right, bottom, left]. */
async function frame(locators, [t, r, b, l]) {
  const boxes = [];
  for (const loc of locators) boxes.push(await loc.boundingBox());
  const x = Math.min(...boxes.map((v) => v.x));
  const y = Math.min(...boxes.map((v) => v.y));
  const right = Math.max(...boxes.map((v) => v.x + v.width));
  const bottom = Math.max(...boxes.map((v) => v.y + v.height));
  return { x: x - l, y: y - t, width: right - x + l + r, height: bottom - y + t + b };
}

// The wizard is tall once a connect step is open: room for all of it.
const TALL = { width: 1440, height: 1300 };

/**
 * Opens the wizard from the Mailboxes page with the provider sign-in the instance offers
 * (the mock reads localStorage["od.mock.mailboxOAuth"]; null offers both). As on opendesk
 * Cloud, mailboxes send through their own provider, never through opendesk
 * (localStorage["od.mock.systemSend"]).
 */
async function start(page, oauth = 'microsoft') {
  await page.evaluate((o) => {
    if (o === null) localStorage.removeItem('od.mock.mailboxOAuth');
    else localStorage.setItem('od.mock.mailboxOAuth', o);
    localStorage.setItem('od.mock.systemSend', 'off');
  }, oauth);
  await button(page, 'Add mailbox').click();
  await settle(page);
  await nameField(page).waitFor();
}

/** Types the name and address, and waits for the provider and the ways in. */
async function address(page, name, email) {
  await nameField(page).fill(name);
  await emailField(page).fill(email);
  await emailField(page).blur();
  await ways(page).waitFor();
  await settle(page, 500);
}

async function next(page, label = 'Continue') {
  await button(page, label).click();
  await settle(page, 500);
}

/** Through the Gmail app password walkthrough to its last step, Connect. */
async function gmailToConnect(page) {
  await start(page);
  await address(page, 'Care', 'care@margin.app');
  await next(page);
  await next(page, "It's on, continue");
  await next(page, 'I have it, continue');
  await page.getByLabel('App password', { exact: true }).waitFor();
}

// An alias's open identity, under its row in the mailbox's Aliases section.
const aliasPanel = (page) => page.locator('#alias-hello-lumen-studio');

async function openMailbox(page, email) {
  await page.getByText(email, { exact: true }).first().click();
  await settle(page);
}

export const shots = [
  // The Mailboxes page.
  {
    name: 'mailboxes/overview',
    path: '/settings/mailboxes',
    highlight: [
      { target: (p) => button(p, 'Add mailbox'), label: 1, badge: 'l' },
      { target: tiles, label: 2 },
      { target: (p) => [text(p, 'Connection'), text(p, 'IMAP and SMTP · Password'), p.getByText('Google Workspace · Sign-in', { exact: true }).last()], label: 3, inset: { x: 8, y: 6 } },
      { target: (p) => p.getByText('Needs attention', { exact: true }).last(), label: 4, badge: 'tr' },
      { target: (p) => labelled(p, 'Actions for partners@margin.app'), label: 5, badge: 'tr' },
    ],
  },
  {
    name: 'mailboxes/open-settings',
    path: '/settings/mailboxes',
    dim: true,
    // The left of the window: both menus at a size that reads in the column.
    clip: () => [{ x: 0, y: 0, width: 1, height: 1 }, { x: 860, y: 899, width: 1, height: 1 }],
    pad: 0,
    highlight: [
      { target: (p) => p.getByRole('link', { name: 'Settings', exact: true }).last(), label: 1, badge: 'r' },
      { target: (p) => p.getByRole('link', { name: 'Mailboxes', exact: true }).first(), label: 2, badge: 'r' },
    ],
  },
  {
    name: 'mailboxes/add-button',
    path: '/settings/mailboxes',
    clip: (p) => [p.getByRole('heading', { name: 'Mailboxes', exact: true }).last(), button(p, 'Add mailbox'), tiles(p)],
    pad: 28,
    highlight: [{ target: (p) => button(p, 'Add mailbox'), label: 1, badge: 'l' }],
  },

  // The wizard, first step: the address, who hosts it, and the ways in.
  {
    name: 'mailboxes/detect',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Care', 'care@margin.app');
    },
    clip: wizard,
    highlight: [
      { target: (p) => [nameField(p), emailField(p)], label: 1, inset: { x: 8, y: 6 } },
      { target: (p) => detection(p, "Detected from the domain's MX records"), label: 2, inset: 3 },
      { target: ways, label: 3, inset: 6 },
      { target: (p) => button(p, 'Continue'), label: 4, badge: 'l' },
    ],
  },
  {
    name: 'mailboxes/type-address',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Care', 'care@margin.app');
      await hide(picker(page));
    },
    clip: (p) => [p.locator('ol[aria-label="Add a mailbox"]'), detection(p, "Detected from the domain's MX records")],
    pad: 20,
    highlight: [
      { target: emailField, label: 1, badge: 'tr' },
      { target: (p) => detection(p, "Detected from the domain's MX records"), label: 2, inset: 3, badge: 'tr' },
    ],
  },
  {
    name: 'mailboxes/pick-method',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Care', 'care@margin.app');
      await hideAbove(page);
    },
    clip: (p) => [text(p, 'How should opendesk connect?'), ways(p), button(p, 'Continue')],
    pad: 20,
    highlight: [
      { target: (p) => way(p, /App password/), label: 1, inset: 3 },
      { target: (p) => button(p, 'Choose another way'), label: 2, badge: 'r' },
      { target: (p) => button(p, 'Continue'), label: 3, badge: 'l' },
    ],
  },
  {
    name: 'mailboxes/done',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await gmailToConnect(page);
      await page.getByLabel('App password', { exact: true }).fill('abcd efgh ijkl mnop');
      await next(page, 'Connect');
      await button(page, 'Done').waitFor();
    },
    clip: wizard,
    highlight: [
      { target: (p) => text(p, 'Care is connected').locator('xpath=ancestor::div[contains(@class, "items-center")][1]'), label: 1, inset: 6 },
      { target: (p) => text(p, 'Care is connected').locator('xpath=ancestor::div[contains(@class, "flex-col")][1]/div[2]'), label: 2, inset: 4 },
    ],
  },
  {
    name: 'mailboxes/cause-and-fix',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await gmailToConnect(page);
      await page.getByLabel('App password', { exact: true }).fill('my-google-password');
      await next(page, 'Connect');
      await page.getByRole('alert').waitFor();
      await only(wizard(page), '[role="alert"]');
    },
    clip: (p) => p.getByRole('alert'),
    pad: 24,
    highlight: [
      { target: (p) => p.getByRole('alert').locator('> div').first(), label: 1, inset: -3 },
      { target: (p) => p.getByRole('alert').locator('> div').nth(1), label: 2, inset: -3, badge: 'bl' },
    ],
  },

  // Gmail and Google Workspace with an app password: the three steps.
  {
    name: 'mailboxes/gmail-2sv',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Care', 'care@margin.app');
      await next(page);
    },
    clip: wizard,
    highlight: [
      { target: (p) => button(p, 'Open Google security settings'), label: 1, badge: 'r' },
      { target: (p) => button(p, "It's on, continue"), label: 2, badge: 'l' },
    ],
  },
  {
    name: 'mailboxes/gmail-app-password',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Care', 'care@margin.app');
      await next(page);
      await next(page, "It's on, continue");
    },
    clip: wizard,
    highlight: [
      { target: (p) => button(p, 'Open app passwords'), label: 1, badge: 'r' },
      { target: (p) => button(p, 'I have it, continue'), label: 2, badge: 'l' },
    ],
  },
  {
    name: 'mailboxes/gmail-connect',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await gmailToConnect(page);
      await page.getByLabel('App password', { exact: true }).fill('abcd efgh ijkl mnop');
    },
    clip: wizard,
    highlight: [
      { target: appPassword, label: 1, inset: 6 },
      { target: servers, label: 2, inset: 3 },
      { target: (p) => button(p, 'Connect'), label: 3, badge: 'l' },
    ],
  },
  {
    name: 'mailboxes/google-signin',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page, null);
      await address(page, 'Care', 'care@margin.app');
      await hideAbove(page);
    },
    clip: (p) => [text(p, 'How should opendesk connect?'), ways(p), button(p, 'Continue')],
    pad: 20,
    highlight: [
      { target: (p) => way(p, /Sign in with Google/), label: 1, inset: 3 },
      { target: (p) => button(p, 'Continue'), label: 2, badge: 'l' },
    ],
  },

  // Microsoft without its sign-in: forwarding in, replies through SMTP.
  {
    name: 'mailboxes/microsoft-detect',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page, 'google');
      await address(page, 'Support', 'support@contoso.com');
    },
    clip: wizard,
    highlight: [
      { target: (p) => detection(p, "Detected from the domain's MX records"), label: 1, inset: 3 },
      { target: (p) => p.getByText('Microsoft turned off password sign-in', { exact: false }), label: 2, inset: 3 },
      { target: (p) => way(p, /Forward mail to opendesk/), label: 3, inset: 3, badge: 'bl' },
    ],
  },
  {
    name: 'mailboxes/microsoft-forwarding',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page, 'microsoft');
      await address(page, 'Support', 'support@contoso.com');
      await next(page);
    },
    clip: wizard,
    highlight: [
      { target: (p) => [text(p, 'How replies are sent'), way(p, /Your SMTP server/)], label: 1, inset: 8 },
      { target: (p) => button(p, 'Continue with Microsoft'), label: 2, badge: 'l' },
    ],
  },
  {
    name: 'mailboxes/microsoft-smtp',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page, 'google');
      await address(page, 'Support', 'support@contoso.com');
      await next(page);
    },
    clip: wizard,
    highlight: [
      { target: (p) => way(p, /Your SMTP server/), label: 1, inset: 4, badge: 'r' },
      { target: (p) => p.getByText('Microsoft is phasing out password sign-in', { exact: false }), label: 2, inset: { x: 8, y: 5 } },
      { target: (p) => fieldset(p, 'Outgoing mail (SMTP)'), label: 3, inset: 3 },
    ],
  },
  // A sending service over SMTP: the preset fills the server, the API key goes in the password.
  {
    name: 'mailboxes/smtp-service',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page, 'microsoft');
      await address(page, 'Support', 'support@brightline.co');
      await next(page);
      await button(page, 'Resend').click();
      await settle(page);
    },
    clip: wizard,
    highlight: [
      { target: (p) => text(p, 'Using a sending service?').locator('xpath=..'), label: 1, inset: 4 },
      { target: (p) => fieldset(p, 'Outgoing mail (SMTP)'), label: 2, inset: 3 },
    ],
  },
  {
    name: 'mailboxes/microsoft-signin',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page, null);
      await address(page, 'Support', 'support@contoso.com');
      await hideAbove(page);
    },
    clip: (p) => [text(p, 'How should opendesk connect?'), ways(p), button(p, 'Continue')],
    pad: 20,
    highlight: [
      { target: (p) => way(p, /Sign in with Microsoft/), label: 1, inset: 3 },
      { target: (p) => button(p, 'Continue'), label: 2, badge: 'l' },
    ],
  },

  {
    name: 'mailboxes/microsoft-forward-to',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page, 'google');
      await address(page, 'Support', 'support@contoso.com');
      await next(page);
      await next(page, 'Create mailbox');
      await page.getByText(/@in\./).first().waitFor();
    },
    clip: wizard,
    highlight: [
      { target: (p) => p.getByText(/@in\./).first().locator('xpath=ancestor::div[contains(@class, "rounded")][1]'), label: 1, inset: 4 },
      { target: (p) => p.locator('ol').filter({ hasText: 'Exchange admin center' }), label: 2, inset: 6 },
    ],
  },

  // Forwarding: the private address and the provider's steps.
  {
    name: 'mailboxes/forwarding-address',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Returns', 'returns@margin.app');
      await way(page, /Forward mail to opendesk/).click();
      await next(page);
      await next(page, 'Create mailbox');
      await page.getByText(/@in\./).first().waitFor();
    },
    clip: wizard,
    highlight: [
      { target: (p) => p.getByText(/@in\./).first().locator('xpath=ancestor::div[contains(@class, "rounded")][1]'), label: 1, inset: 4 },
      { target: (p) => p.locator('ol').filter({ hasText: 'Google Admin console' }), label: 2, inset: 6 },
    ],
  },

  // Other providers: an app password guide, a detected host, prefilled servers.
  {
    name: 'mailboxes/yahoo-app-password',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Sales', 'sales@yahoo.com');
      await next(page);
    },
    clip: wizard,
    highlight: [
      { target: (p) => text(p, 'Create an app password').locator('xpath=..'), label: 1, inset: 3 },
      { target: appPassword, label: 2, inset: 6 },
      { target: servers, label: 3, inset: 3 },
    ],
  },
  {
    name: 'mailboxes/autoconfig-detect',
    path: '/settings/mailboxes',
    viewport: TALL,
    run: async (page) => {
      await start(page);
      await address(page, 'Help', 'help@acme.example');
    },
    clip: wizard,
    highlight: [
      { target: (p) => detection(p, "From the domain's autoconfig file"), label: 1, inset: 3 },
      { target: (p) => way(p, /Email password/), label: 2, inset: 3 },
    ],
  },
  {
    name: 'mailboxes/imap-prefilled',
    path: '/settings/mailboxes',
    viewport: { width: 1440, height: 1500 },
    run: async (page) => {
      await start(page);
      await address(page, 'Help', 'help@acme.example');
      await way(page, /Other IMAP and SMTP/).click();
      await next(page);
    },
    clip: (p) => [fieldset(p, 'Incoming mail (IMAP)'), fieldset(p, 'Outgoing mail (SMTP)'), button(p, 'Connect and test')],
    pad: 20,
    highlight: [
      { target: (p) => fieldset(p, 'Incoming mail (IMAP)'), label: 1, inset: 3 },
      { target: (p) => fieldset(p, 'Outgoing mail (SMTP)'), label: 2, inset: 3 },
      { target: (p) => button(p, 'Connect and test'), label: 3, badge: 'l' },
    ],
  },

  // A connected mailbox's page.
  {
    name: 'mailboxes/connection-error',
    path: '/settings/mailboxes',
    run: (page) => openMailbox(page, 'partners@margin.app'),
    clip: (p) => frame([text(p, 'Connection'), button(p, 'Test connection'), text(p, 'Last synced')], [24, 24, 14, 24]),
    pad: 0,
    highlight: [
      { target: (p) => text(p, 'Last sync failed').locator('xpath=ancestor::div[contains(@class, "bg-tone-red-soft")][1]'), label: 1, inset: -4 },
      { target: (p) => button(p, 'Test connection'), label: 2, badge: 'l' },
    ],
  },
  {
    name: 'mailboxes/reconnect',
    path: '/settings/mailboxes',
    run: (page) => openMailbox(page, 'support@margin.app'),
    clip: (p) => [text(p, 'Connection'), button(p, 'Test connection'), button(p, 'Reconnect')],
    pad: 24,
    highlight: [{ target: (p) => button(p, 'Reconnect'), label: 1, badge: 'l' }],
  },
  {
    name: 'mailboxes/sending',
    path: '/settings/mailboxes',
    viewport: { width: 1440, height: 1400 },
    run: (page) => openMailbox(page, 'support@margin.app'),
    clip: (p) => [text(p, 'Name and addresses'), around(p, 'A reply', 'Auto-reply')],
    pad: 24,
    highlight: [
      { target: (p) => around(p, 'New tickets to this address go to', 'The team that owns').locator('xpath=..'), label: 1, inset: { x: 8, y: -6 } },
      { target: (p) => [text(p, 'From name'), around(p, 'A reply', 'Auto-reply')], label: 2, inset: 8 },
    ],
  },
  {
    name: 'mailboxes/aliases',
    path: '/settings/mailboxes',
    viewport: { width: 1440, height: 2800 },
    run: async (page) => {
      await openMailbox(page, 'support@margin.app');
      await labelled(page, 'Edit hello@lumen.studio').click();
      await settle(page);
    },
    clip: (p) => [text(p, 'Aliases'), aliasPanel(p)],
    pad: 24,
    highlight: [
      { target: (p) => around(p, 'hello@lumen.studio', '3 / 20'), label: 1, inset: 6 },
      { target: (p) => around(p, 'From Gmail', 'Waiting for confirmation in Gmail'), label: 2, inset: 4 },
      { target: (p) => [aliasPanel(p).locator('.grid').first(), aliasPanel(p).getByText('Go out from this address', { exact: false })], label: 3, inset: 6 },
      { target: (p) => [aliasPanel(p).getByText('Signature', { exact: true }), aliasPanel(p).locator('.signature-editor')], label: 4, inset: 6 },
    ],
  },
  {
    name: 'mailboxes/replies',
    path: '/settings/mailboxes',
    viewport: { width: 1440, height: 2000 },
    run: (page) => openMailbox(page, 'support@margin.app'),
    clip: (p) => [text(p, 'Signature'), text(p, 'Thanks for writing in', false)],
    pad: 24,
    highlight: [
      { target: (p) => around(p, 'agent.first_name').locator('xpath=..'), label: 1, inset: 4 },
      { target: (p) => around(p, 'Send an automatic reply', 'Subject', 'Message'), label: 2, inset: 4 },
    ],
  },
  {
    name: 'mailboxes/row-actions',
    path: '/settings/mailboxes',
    run: async (page) => {
      await labelled(page, 'Actions for support@margin.app').click();
      await settle(page);
    },
    // From the row's top edge down: no slice of the row above it.
    clip: (p) => frame([p.getByRole('menu'), around(p, 'support@margin.app', 'Google Workspace · Sign-in')], [-1, 24, 24, 24]),
    pad: 0,
    highlight: [{ target: (p) => p.getByRole('menu'), inset: 3 }],
  },
];
