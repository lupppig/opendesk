// What this browser remembers about Spaces, per user (contracts/spaces.md "The switcher"): what
// is picked, a Space (`od-space:<uid>` holds its id) or a workspace (no key). Also which editor
// is open. Spaces themselves live on the API.

import { create } from "zustand";
import type { Space } from "../api/types";
import { useSession } from "../session";

export const spaceKeys = {
  picked: (uid: string) => `od-space:${uid}`,
};

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    /* storage unavailable */
  }
}

/** The editor: a new Space (blank, or started `from` one), one of the person's, or the open workspace's starter Space (admins). */
export type EditorTarget = { kind: "new"; from?: Space } | { kind: "space"; id: string } | { kind: "starter" };

type State = {
  picked: Record<string, string | null>;
  editor: EditorTarget | null;
};

export const useSpaceUi = create<State>(() => ({ picked: {}, editor: null }));

/** The Space a user picked here (read from storage the first time), or null for a workspace. */
export function pickedSpaceOf(uid: string): string | null {
  const s = useSpaceUi.getState().picked;
  return uid in s ? s[uid] : read(spaceKeys.picked(uid));
}

export const spaceUi = {
  /** Picks a Space, or the workspace (null), and remembers it. */
  pick(uid: string, id: string | null) {
    if (pickedSpaceOf(uid) === id) return;
    write(spaceKeys.picked(uid), id);
    useSpaceUi.setState((s) => ({ picked: { ...s.picked, [uid]: id } }));
  },
  edit(target: EditorTarget) {
    useSpaceUi.setState({ editor: target });
  },
  closeEditor() {
    useSpaceUi.setState({ editor: null });
  },
};

/** The id of the Space the signed-in user picked here, or null. */
export function usePickedSpaceId(): string | null {
  const uid = useSession((s) => s.user?.id ?? "");
  return useSpaceUi((s) => (uid in s.picked ? s.picked[uid] : uid ? read(spaceKeys.picked(uid)) : null));
}
