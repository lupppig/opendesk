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

/** Holds `el` (the scrolling box) at its end while it or `content` (what scrolls in it) changes size. */
export function stickToBottom(el: HTMLElement, content: Element): Stick {
  let pinned = true;
  let last = el.scrollTop;
  let height = el.scrollHeight;
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
    el.scrollTop = el.scrollHeight;
    last = el.scrollTop;
    height = el.scrollHeight;
  };
  const onScroll = () => {
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight;
    const resized = el.scrollHeight !== height;
    height = el.scrollHeight;
    // Content growing or shrinking moves the end, not the agent: only the agent scrolling
    // up away from the end lets go of it.
    if (gap < STICK_SLACK) pinned = true;
    else if (el.scrollTop < last && (!resized || performance.now() - input < INTENT_MS)) pinned = false;
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
    },
    stop: () => {
      el.removeEventListener("scroll", onScroll);
      for (const type of ["wheel", "touchmove", "pointerdown"] as const) el.removeEventListener(type, reach);
      el.ownerDocument.removeEventListener("keydown", onKey);
      ro?.disconnect();
    },
  };
}
