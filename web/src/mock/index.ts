// Mock mode (VITE_MOCK=1): an in-memory API and gateway seeded with realistic
// data, so every screen runs without a backend. Only imported behind the
// build-time MOCK flag, so production bundles never include it.

import { setTransport } from "@/lib/api/client";
import { setSocketFactory } from "@/lib/realtime/transport";
import { db } from "./db";
import { paused, publish, socketFactory, sockets } from "./gateway";
import { registerAi } from "./handlers/ai";
import { registerAiClients } from "./handlers/aiClients";
import { registerAiTicket } from "./handlers/aiTicket";
import { registerAssistant } from "./handlers/assistant";
import { registerAgent } from "./handlers/agent";
import { registerAgentQuality } from "./handlers/agentQuality";
import { registerEmailAnswers } from "./handlers/emailAnswers";
import { registerAuth, setSignedIn } from "./handlers/auth";
import { registerBilling } from "./handlers/billing";
import { registerBots } from "./handlers/bots";
import { registerCollab } from "./handlers/collab";
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
import { registerTickets } from "./handlers/tickets";
import { registerWorkspace } from "./handlers/workspace";
import { pauseLive, resumeLive, startLive } from "./live";
import { chatTicket, ollieReply } from "./ollie";
import { dispatch } from "./router";
import { seed } from "./seed";

declare global {
  interface Window {
    __odMock?: {
      db: typeof db;
      emit: (type: string, data: Record<string, unknown>, topics?: string[]) => void;
      pause: () => void;
      resume: () => void;
      dropEvents: (on: boolean) => void;
      reconnect: () => void;
      /** Ollie answers a visitor now on the given ticket (id or number), else the chat ticket you're viewing. */
      ollie: (ticketId?: string) => Promise<string | null>;
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
  registerAuth();
  registerWorkspace();
  registerTickets();
  registerLabels();
  registerResources();
  registerBilling();
  registerAi();
  registerAiTicket();
  registerAssistant();
  registerAgent();
  registerAgentQuality();
  registerEmailAnswers();
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

  setTransport((input, init) => dispatch(input, init));
  setSocketFactory(socketFactory);

  if (params.get("mock_quiet") !== "1") startLive();

  window.__odMock = {
    db,
    emit: (type, data, topics) => {
      publish(db.workspaces[0], type, data, { topics });
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
  };
  console.info("[opendesk] mock mode: in-memory API and gateway. window.__odMock has helpers.");
}
