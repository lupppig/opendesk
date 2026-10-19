// The floating composer for new mail (c): which account it sends from and what it
// starts with (or the stored draft it opens). One at a time, kept while it is minimized.

import { create } from "zustand";
import type { MailMessage } from "../api/types";

export type ComposeDraft = { accountId?: string; to?: string[]; subject?: string; html?: string; fromDraft?: MailMessage };

type ComposeState = { open: boolean; minimized: boolean; draft: ComposeDraft; key: number };

export const useCompose = create<ComposeState>(() => ({ open: false, minimized: false, draft: {}, key: 0 }));

export const compose = {
  open: (draft: ComposeDraft = {}) => {
    const s = useCompose.getState();
    // A composer already open comes back as it was, unless something new was asked for.
    if (s.open && !draft.accountId && !draft.to && !draft.subject && !draft.fromDraft) return useCompose.setState({ minimized: false });
    if (s.open && draft.fromDraft && s.draft.fromDraft?.id === draft.fromDraft.id) return useCompose.setState({ minimized: false });
    useCompose.setState({ open: true, minimized: false, draft, key: s.key + 1 });
  },
  close: () => useCompose.setState({ open: false, minimized: false, draft: {} }),
  minimize: (m: boolean) => useCompose.setState({ minimized: m }),
};
