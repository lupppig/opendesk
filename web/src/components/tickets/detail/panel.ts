// The ticket's details column, shown or hidden, remembered per browser (the
// ticket page and the new ticket page share it).

const PANEL_KEY = "od-ticket-panel";

export function readPanel() {
  try {
    return localStorage.getItem(PANEL_KEY) !== "0";
  } catch {
    return true;
  }
}

export function savePanel(open: boolean) {
  try {
    localStorage.setItem(PANEL_KEY, open ? "1" : "0");
  } catch {
    /* storage unavailable */
  }
}
