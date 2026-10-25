// The English catalogs, bundled. Add a namespace here, in NAMESPACES, and as
// locales/en/<ns>.json; i18next.d.ts derives the typed keys from this object.

import agent from "./locales/en/agent.json";
import agentQuality from "./locales/en/agentQuality.json";
import bots from "./locales/en/bots.json";
import ai from "./locales/en/ai.json";
import aiSetup from "./locales/en/aiSetup.json";
import aiTicket from "./locales/en/aiTicket.json";
import assistant from "./locales/en/assistant.json";
import billing from "./locales/en/billing.json";
import collab from "./locales/en/collab.json";
import common from "./locales/en/common.json";
import imports from "./locales/en/imports.json";
import inbox from "./locales/en/inbox.json";
import indicators from "./locales/en/indicators.json";
import navigation from "./locales/en/navigation.json";
import auth from "./locales/en/auth.json";
import ui from "./locales/en/ui.json";
import contacts from "./locales/en/contacts.json";
import emailDesign from "./locales/en/emailDesign.json";
import kb from "./locales/en/kb.json";
import pub from "./locales/en/public.json";
import reports from "./locales/en/reports.json";
import settings from "./locales/en/settings.json";
import settingsChannels from "./locales/en/settingsChannels.json";
import settingsDev from "./locales/en/settingsDev.json";
import settingsInbox from "./locales/en/settingsInbox.json";
import settingsTeams from "./locales/en/settingsTeams.json";
import setup from "./locales/en/setup.json";
import shell from "./locales/en/shell.json";
import together from "./locales/en/together.json";

export const en = { common, billing, collab, ai, agent, agentQuality, bots, aiSetup, aiTicket, assistant, public: pub, shell, settings, settingsInbox, settingsDev, settingsChannels, settingsTeams, emailDesign, contacts, kb, reports, inbox, indicators, navigation, auth, ui, imports, setup, together } as const;
export const NAMESPACES = ["common", "billing", "collab", "ai", "agent", "agentQuality", "bots", "aiSetup", "aiTicket", "assistant", "public", "shell", "settings", "settingsInbox", "settingsDev", "settingsChannels", "settingsTeams", "emailDesign", "contacts", "kb", "reports", "inbox", "indicators", "navigation", "auth", "ui", "imports", "setup", "together"] as const;
export type Namespace = (typeof NAMESPACES)[number];
