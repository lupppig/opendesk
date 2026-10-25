// URLs and copy shared across the site. Domains follow AGENTS.md.
export const SITE = {
  name: 'opendesk',
  url: 'https://opendesk.sh',
  tagline: 'The open-source helpdesk and team inbox',
  description:
    'opendesk is the open-source helpdesk and team inbox: shared support inboxes, team mail with private and shared accounts, live chat in 14 languages, AI drafts and translation, a knowledge base, automations, SLAs, reports and a public API, all updating live across your team. Free to start, or self-host it in one command.',
} as const;

export const LINKS = {
  app: 'https://app.opendesk.sh',
  signup: 'https://app.opendesk.sh/register',
  login: 'https://app.opendesk.sh/login',
  docs: 'https://docs.opendesk.sh',
  api: 'https://api.opendesk.sh',
  apiReference: 'https://api.opendesk.sh/docs',
  openapi: 'https://api.opendesk.sh/v1/openapi.json',
  github: 'https://github.com/useopendesk/opendesk',
  githubIssues: 'https://github.com/useopendesk/opendesk/issues',
  githubReleases: 'https://github.com/useopendesk/opendesk/releases',
  discord: 'https://dc.opendesk.sh',
  selfHosting: 'https://docs.opendesk.sh/self-hosting/',
  selfHostQuickstart: 'https://docs.opendesk.sh/getting-started/quickstart-self-host/',
  license: 'https://github.com/useopendesk/opendesk/blob/main/LICENSE',
  install: 'curl -fsSL https://opendesk.sh/install.sh | sh',
} as const;

// The one address people reach us on: sales, support, security, privacy and legal alike.
export const EMAIL = 'team@opendesk.sh';

// The company that operates opendesk Cloud. UK law asks for the name, number, place of
// registration and registered office on the site; the ICO reference goes with the
// privacy policy. Shown in the footer, /privacy, /terms and /contact.
export const COMPANY = {
  legalName: 'Mindroot Ltd',
  number: '16543299',
  registeredIn: 'England and Wales',
  address: '71-75 Shelton Street, London, England, WC2H 9JQ',
  shortAddress: '71-75 Shelton Street, London WC2H 9JQ',
  ico: 'ZB958997',
  jurisdiction: 'England and Wales',
} as const;

export const NAV = [
  { href: '/pricing/', label: 'Pricing' },
  { href: '/download/', label: 'Download' },
  { href: LINKS.docs, label: 'Documentation' },
  { href: '/blog/', label: 'Blog' },
] as const;

// The Product menu in the header and the feature anchors on /features.
export const PRODUCT_NAV = [
  { icon: 'inbox', href: '/features/#inbox', title: 'Shared inbox', desc: 'Every mailbox and channel in one queue.' },
  { icon: 'chat', href: '/features/#chat', title: 'Live chat', desc: 'A light widget that speaks 14 languages.' },
  { icon: 'sparkles', href: '/features/#ai', title: 'AI', desc: 'Drafts, summaries, triage and translation.' },
  { icon: 'book', href: '/features/#knowledge-base', title: 'Knowledge base', desc: 'A help center customers search themselves.' },
  { icon: 'zap', href: '/features/#automations', title: 'Automations and SLAs', desc: 'Rules and targets that run on arrival.' },
  { icon: 'chart', href: '/features/#reports', title: 'Reports and CSAT', desc: 'Medians, breaches and satisfaction.' },
  { icon: 'radio', href: '/features/#inbox', title: 'Realtime', desc: 'Presence, typing and live updates.' },
  { icon: 'braces', href: '/features/#api', title: 'API and webhooks', desc: 'The same REST API the app uses.' },
  { icon: 'shield', href: '/security/', title: 'Security', desc: 'CASA Tier 1 controls, written down.' },
] as const;
