import type navigation from "@/i18n/locales/en/navigation.json";
import { useEffect, useRef } from "react";

export type HotkeyMap = Record<string, (e: KeyboardEvent) => void>;

export function isTypingTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null;
  if (!el || !el.tagName) return false;
  if (el.isContentEditable) return true;
  const tag = el.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") {
    const type = (el as HTMLInputElement).type;
    return !["checkbox", "radio", "button", "submit"].includes(type);
  }
  return false;
}

/** What Enter and Space press or toggle when it has the focus: links, buttons, form controls and their ARIA roles. */
const PRESSABLE = [
  "a[href]",
  "button",
  "input",
  "select",
  "textarea",
  "summary",
  '[contenteditable]:not([contenteditable="false"])',
  ...["button", "link", "checkbox", "radio", "switch", "tab", "menuitem", "menuitemcheckbox", "menuitemradio", "combobox"].map((r) => `[role="${r}"]`),
].join(", ");

/** Whether the event's target is, or is inside, a control that Enter and Space act on. */
export function isPressTarget(t: EventTarget | null): boolean {
  const el = t as Element | null;
  return !!el?.closest?.(PRESSABLE);
}

function modalOpen() {
  return !!document.querySelector('[role="dialog"][aria-modal="true"], [role="alertdialog"], [data-radix-popper-content-wrapper]');
}

/** Normalised name for a key event: "mod+k", "shift+?", "j", "enter", "escape". */
export function keyName(e: KeyboardEvent): string {
  const parts: string[] = [];
  if (e.metaKey || e.ctrlKey) parts.push("mod");
  if (e.altKey) parts.push("alt");
  let k = e.key.length === 1 ? e.key.toLowerCase() : e.key.toLowerCase();
  if (k === " ") k = "space";
  if (e.shiftKey && k.length > 1) parts.push("shift");
  parts.push(k);
  return parts.join("+");
}

/**
 * Page-level single-key shortcuts. Ignored while typing or while a dialog or
 * popover is open, except bindings that use the mod key. Enter and Space are
 * left to a focused link, button or other control, which they press. "g i"-style
 * sequences are supported.
 */
export function useHotkeys(map: HotkeyMap, enabled = true) {
  const ref = useRef(map);
  ref.current = map;
  useEffect(() => {
    if (!enabled) return;
    let pending: string | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing) return;
      const name = keyName(e);
      const withMod = name.startsWith("mod+");
      if (!withMod && (isTypingTarget(e.target) || modalOpen())) return;
      if ((name === "enter" || name === "space") && isPressTarget(e.target)) return;
      const seq = pending ? `${pending} ${name}` : null;
      const fn = (seq && ref.current[seq]) || ref.current[name];
      if (fn) {
        pending = null;
        e.preventDefault();
        fn(e);
        return;
      }
      if (!withMod && Object.keys(ref.current).some((k) => k.startsWith(`${name} `))) {
        pending = name;
        clearTimeout(timer);
        timer = setTimeout(() => (pending = null), 900);
      } else pending = null;
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      clearTimeout(timer);
    };
  }, [enabled]);
}

export const shortcutGroups: { title: keyof typeof navigation.shortcuts; items: { keys: string; label: keyof typeof navigation.shortcuts }[] }[] = [
  {
    title: "general",
    items: [
      { keys: "mod k", label: "commandMenu" },
      { keys: "mod j", label: "askOllie" },
      { keys: "/", label: "searchTickets" },
      { keys: "c", label: "newTicket" },
      { keys: "?", label: "keyboardShortcuts" },
      { keys: "g i", label: "goToInbox" },
      { keys: "g t", label: "switchTeam" },
      { keys: "g c", label: "goToContacts" },
      { keys: "g k", label: "goToKnowledgeBase" },
      { keys: "g s", label: "goToSettings" },
    ],
  },
  {
    title: "ticketList",
    items: [
      { keys: "j", label: "nextTicket" },
      { keys: "k", label: "previousTicket" },
      { keys: "enter", label: "openTicket" },
      { keys: "x", label: "selectTicket" },
      { keys: "e", label: "solveFocusedTicket" },
      { keys: "r", label: "openAndReply" },
      { keys: "escape", label: "closeTicketOrClearSelection" },
    ],
  },
  {
    title: "ticket",
    items: [
      { keys: "r", label: "reply" },
      { keys: "n", label: "addANote" },
      { keys: "a", label: "assign" },
      { keys: "s", label: "changeStatus" },
      { keys: "p", label: "changePriority" },
      { keys: "t", label: "moveToTeam" },
      { keys: "h", label: "snooze" },
      { keys: "e", label: "solve" },
      { keys: "mod enter", label: "send" },
      { keys: "mod j", label: "draftWithOllie" },
    ],
  },
];
