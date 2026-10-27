// Mock mode (VITE_MOCK=1): an in-memory API and gateway seeded with realistic
// data, so every screen runs without a backend. Only imported behind the
// build-time MOCK flag, so production bundles never include it.

import { setTransport } from "@/lib/api/client";
import { setConversationLayout } from "@/lib/conversationLayout";
import { setSocketFactory } from "@/lib/realtime/transport";
import { db } from "./db";
import { paused, publish, socketFactory, sockets } from "./gateway";
import { registerAi } from "./handlers/ai";
import { registerAiClients } from "./handlers/aiClients";
import { registerAiTicket } from "./handlers/aiTicket";
import { registerAssistant } from "./handlers/assistant";
import { registerAssistantPeer } from "./handlers/assistantPeer";
import { registerAgent } from "./handlers/agent";
import { registerAgentQuality } from "./handlers/agentQuality";
import { registerEmailAnswers } from "./handlers/emailAnswers";
import { registerAuth, setSignedIn } from "./handlers/auth";
import { registerBilling } from "./handlers/billing";
import { registerBots } from "./handlers/bots";
import { registerChangelog } from "./handlers/changelog";
import { registerCollab } from "./handlers/collab";
import { registerWorkspaces } from "./handlers/workspaces";
import { registerDrafts, stageDraft } from "./handlers/drafts";
import { registerKnowledge } from "./handlers/knowledge";
import { registerImports } from "./handlers/imports";
import { registerIntegrations } from "./handlers/integrations";
import { registerLabels } from "./handlers/labels";
import { applyMockImap, syncReceiving } from "./handlers/mailhost";
import { registerMcp } from "./handlers/mcp";
import { registerNotificationPrefs } from "./handlers/notificationPrefs";
import { registerSupportChannels } from "./handlers/supportChannels";
import { registerTemplates } from "./handlers/templates";
import { registerResources } from "./handlers/resources";
import { registerSpaces } from "./handlers/spaces";
import { registerTickets } from "./handlers/tickets";
import { registerWorkspace } from "./handlers/workspace";
import { registerWiki } from "./handlers/wiki";
import { pauseLive, resumeLive, startLive } from "./live";
import { chatTicket, ollieReply } from "./ollie";
import { dispatch } from "./router";
import { seed } from "./seed";
import { findTicket } from "./tickets";
import { textToHtml } from "./util";

declare global {
  interface Window {
    __odMock?: {
      db: typeof db;
      emit: (type: string, data: Record<string, unknown>, topics?: string[]) => void;
      /** Publishes an event in another workspace (by id or name), as a Space's light socket sees it. */
      emitIn: (workspace: string, type: string, data: Record<string, unknown>) => void;
      pause: () => void;
      resume: () => void;
      dropEvents: (on: boolean) => void;
      reconnect: () => void;
      /** Ollie answers a visitor now on the given ticket (id or number), else the chat ticket you're viewing. */
      ollie: (ticketId?: string) => Promise<string | null>;
      /** An AI client stages a reply draft on the ticket (id or number) over MCP, as `draft_reply` would. */
      draft: (ticketId: string, text: string, by?: string) => void;
    };
  }
}

let installed = false;

export function installMock(): void {
  if (installed) return;
  installed = true;

  const params = new URLSearchParams(location.search);
  if (params.get("mock") === "anon") setSignedIn(false);

  applyMockImap();
  seed();
  for (const w of db.workspaces) syncReceiving(w.mailboxes);
  const gate = params.get("mock_gate");
  if (gate === "mfa" || gate === "reauth") db.stepUp = gate === "mfa" ? "mfa_required" : "reauth_required";
  // ?mock_locale=az: every workspace shows its customer pages in that language for everyone.
  const fixed = params.get("mock_locale");
  if (fixed) for (const w of db.workspaces) w.workspace.settings = { ...w.workspace.settings, locale: fixed, locale_fixed: true };
  // ?mock_layout=conversation (or =full): the timeline's layout preference, as Settings → Profile sets it.
  const layout = params.get("mock_layout");
  if (layout === "conversation" || layout === "full") setConversationLayout(layout === "conversation");
  registerAuth();
  registerWorkspace();
  registerTickets();
  registerLabels();
  registerResources();
  registerChangelog();
  registerBilling();
  registerAi();
  registerAiTicket();
  registerAssistant();
  registerAssistantPeer();
  registerAgent();
  registerAgentQuality();
  registerEmailAnswers();
  registerDrafts();
  registerMcp();
  registerKnowledge();
  registerIntegrations();
  registerImports();
  registerAiClients();
  registerBots();
  registerNotificationPrefs();
  registerSupportChannels();
  registerTemplates();
  registerCollab();
  registerWorkspaces();
  registerSpaces();
  registerWiki();

  setTransport((input, init) => dispatch(input, init));
  setSocketFactory(socketFactory);

  // The simulated activity stays off with ?mock_quiet=1, or for a whole dev server with
  // VITE_MOCK_QUIET=1 (the QA harness, so stills hold still); ?mock_live=1 turns it back on.
  const quiet = params.get("mock_quiet") === "1" || import.meta.env.VITE_MOCK_QUIET === "1";
  if (!quiet || params.get("mock_live") === "1") startLive();

  window.__odMock = {
    db,
    emit: (type, data, topics) => {
      publish(db.workspaces[0], type, data, { topics });
    },
    emitIn: (workspace, type, data) => {
      const w = db.workspaces.find((x) => x.workspace.id === workspace || x.workspace.name === workspace);
      if (w) publish(w, type, data);
    },
    pause: pauseLive,
    resume: resumeLive,
    dropEvents: (on) => {
      paused.drop = on;
    },
    reconnect: () => sockets.forEach((s) => s.bounce()),
    ollie: async (ticketId) => {
      const ws = db.workspaces[0];
      const t = chatTicket(ws, ticketId);
      if (!t) {
        console.warn("[opendesk] mock: no open chat ticket to answer on");
        return null;
      }
      return ollieReply(ws, t);
    },
    draft: (ticketId, text, by = "Claude") => {
      const ws = db.workspaces[0];
      const t = findTicket(ws, ticketId);
      if (!t) {
        console.warn("[opendesk] mock: no such ticket to draft on");
        return;
      }
      stageDraft(ws, t, textToHtml(text), by);
    },
  };
  console.info("[opendesk] mock mode: in-memory API and gateway. window.__odMock has helpers.");
}
