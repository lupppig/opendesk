import { create } from "zustand";

type ShellState = {
  paletteOpen: boolean;
  shortcutsOpen: boolean;
  newTicketOpen: boolean;
  sidebarOpen: boolean;
};

export const useShell = create<ShellState>(() => ({
  paletteOpen: false,
  shortcutsOpen: false,
  newTicketOpen: false,
  sidebarOpen: false,
}));

/** The new ticket page: New ticket there opens nothing more, the dialog would be the same draft. */
export const NEW_TICKET_PATH = "/tickets/new";

export const shell = {
  palette: (open = true) => useShell.setState({ paletteOpen: open }),
  shortcuts: (open = true) => useShell.setState({ shortcutsOpen: open }),
  newTicket: (open = true) => useShell.setState({ newTicketOpen: open }),
  sidebar: (open: boolean) => useShell.setState({ sidebarOpen: open }),
};

// Contextual commands a page offers the command menu (the open ticket's actions).
export type PageCommand = { id: string; label: string; group?: string; shortcut?: string; icon?: React.ReactNode; keywords?: string[]; run: () => void };
type CommandsState = { commands: PageCommand[] };
export const usePageCommands = create<CommandsState>(() => ({ commands: [] }));

// The sidebar rail: expanded or icon-only, remembered per browser.
const NAV_KEY = "od-nav-collapsed";
function readCollapsed() {
  try {
    return localStorage.getItem(NAV_KEY) === "1";
  } catch {
    return false;
  }
}
export const useNav = create<{ collapsed: boolean }>(() => ({ collapsed: readCollapsed() }));
export function toggleNav(next = !useNav.getState().collapsed) {
  useNav.setState({ collapsed: next });
  try {
    localStorage.setItem(NAV_KEY, next ? "1" : "0");
  } catch {
    /* storage unavailable */
  }
}

// The last header crumb a page names itself (a ticket subject, an article title).
export const useCrumbStore = create<{ label: string | null }>(() => ({ label: null }));
