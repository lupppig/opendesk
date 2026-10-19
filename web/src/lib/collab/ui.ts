// Collab's shell state: the customize sidebar dialog.

import { create } from "zustand";

export const useCollabUi = create<{ customizeOpen: boolean }>(() => ({ customizeOpen: false }));

export const collabUi = {
  customize: (open = true) => useCollabUi.setState({ customizeOpen: open }),
};
