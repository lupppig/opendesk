// Keeps the ticket timeline at its latest message (Timeline.tsx). It opens there, and stays
// there while what it shows settles: emails drawn in their own frames grow once loaded,
// images arrive, activity and side conversations come in after the messages, the reply box
// grows and shrinks below. Only the agent scrolling up lets go; scrolling back down to the
// end, or a new message while there, holds it again.

/** How close to the end still counts as at the end, in pixels. */
export const STICK_SLACK = 80;
/** How long after the agent's wheel, touch, key or pointer a scroll up counts as theirs, in ms,
 * even while the content changes size. */
const INTENT_MS = 1000;
const UP_KEYS = new Set(["ArrowUp", "PageUp", "Home"]);

export type Stick = {
  /** Whether the box is held at its end. */
  readonly pinned: boolean;
  /** Goes to the end if held there (new items, a live reply growing). */
  follow(): void;
  /** Lets go of the end, for a jump to a message further up (showInTimeline). */
  release(): void;
  stop(): void;
};

/**
 * Holds `el` (the scrolling box) at its end while it or `content` (what scrolls in it) changes size.
 *
 * `floor`, when given, says how far down the hold may go and still keep the latest message in view
 * (latestMessageFloor): history after it (triage, rules, API keys setting a status or a tag) is no
 * reason to scroll the message itself away. The hold goes to the end, or to that floor if it is
 * higher, until the agent scrolls themselves; from then on it is the end, as without one.
 */
export function stickToBottom(el: HTMLElement, content: Element, floor?: () => number | null): Stick {
  let pinned = true;
  // Whether the hold stops at `floor`: from the opening until the agent scrolls.
  let capped = !!floor;
  let last = el.scrollTop;
  let height = el.scrollHeight;
  /** Where the hold is: the end, or higher up, where the latest message stays in view. */
  const target = () => {
    const end = el.scrollHeight - el.clientHeight;
    const f = capped ? floor?.() : null;
    return f == null ? end : Math.max(0, Math.min(end, f));
  };
  // When the agent last reached for the scroll. A scroll up while the content changed size
  // (a frame shrinking and growing again within a frame, the browser clamping) is the
  // layout's, unless they just did; a wheel over an email's frame never reaches the box,
  // but leaves the content as it was.
  let input = Number.NEGATIVE_INFINITY;
  const reach = () => {
    input = performance.now();
  };
  const onKey = (e: KeyboardEvent) => {
    if (UP_KEYS.has(e.key) || (e.key === " " && e.shiftKey)) reach();
  };
  const follow = () => {
    if (!pinned) return;
    const to = target();
    el.scrollTop = to >= el.scrollHeight - el.clientHeight ? el.scrollHeight : to;
    last = el.scrollTop;
    height = el.scrollHeight;
  };
  const onScroll = () => {
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight;
    const resized = el.scrollHeight !== height;
    const theirs = performance.now() - input < INTENT_MS;
    height = el.scrollHeight;
    // The agent scrolling down past the latest message reads what came after it: the end
    // holds from then on.
    if (capped && theirs && el.scrollTop > last) capped = false;
    // Content growing or shrinking moves the end, not the agent: only the agent scrolling
    // up away from the end lets go of it.
    if (gap < STICK_SLACK || (capped && Math.abs(el.scrollTop - target()) < STICK_SLACK)) pinned = true;
    else if (el.scrollTop < last && (!resized || theirs)) {
      pinned = false;
      capped = false;
    }
    last = el.scrollTop;
  };
  el.addEventListener("scroll", onScroll, { passive: true });
  for (const type of ["wheel", "touchmove", "pointerdown"] as const) el.addEventListener(type, reach, { passive: true });
  el.ownerDocument.addEventListener("keydown", onKey);
  const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(follow);
  ro?.observe(el);
  ro?.observe(content);
  follow();
  return {
    get pinned() {
      return pinned;
    },
    follow,
    release: () => {
      pinned = false;
      capped = false;
    },
    stop: () => {
      el.removeEventListener("scroll", onScroll);
      for (const type of ["wheel", "touchmove", "pointerdown"] as const) el.removeEventListener(type, reach);
      el.ownerDocument.removeEventListener("keydown", onKey);
      ro?.disconnect();
    },
  };
}

/** Room kept between the latest message and the box's edge when the hold stops at it, in pixels. */
export const FLOOR_PAD = 16;

/**
 * The lowest scroll position of `el` that keeps the latest message in `content` (the last
 * `article[id^="m-"]`, as MessageItem draws them) in view: its top near the top of the box when
 * it fits, else its end near the bottom. Null when there is no message yet.
 */
export function latestMessageFloor(el: HTMLElement, content: Element): number | null {
  const messages = content.querySelectorAll('article[id^="m-"]');
  const m = messages[messages.length - 1];
  if (!m) return null;
  const box = el.getBoundingClientRect();
  const r = m.getBoundingClientRect();
  const top = r.top - box.top + el.scrollTop;
  const bottom = r.bottom - box.top + el.scrollTop;
  return Math.max(top - FLOOR_PAD, bottom - el.clientHeight + FLOOR_PAD);
}
