import { act, useRef, useState, type CSSProperties } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import "@/i18n";
import { paneLimits, setLiveWidth, STEP } from "@/lib/panes";
import { ResizeHandle } from "./resize-handle";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
// jsdom lays nothing out: the pane reports the live width, else the saved one, else 380.
const live = (el: HTMLElement) => el.style.getPropertyValue("--pane-list-live");
const widthOf = (el: HTMLElement) => parseFloat(live(el)) || parseFloat(el.style.getPropertyValue("--pane-list")) || 380;

beforeEach(() => {
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return { width: widthOf(this) } as DOMRect;
  });
  HTMLElement.prototype.setPointerCapture = () => {};
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

async function mount(side: "before" | "after", saved: number | null = null) {
  const onWidth = vi.fn();
  const onLive = vi.fn();
  function Pane() {
    const ref = useRef<HTMLDivElement>(null);
    const [width, setWidth] = useState(saved);
    return (
      <div>
        <div ref={ref} data-pane style={width === null ? undefined : ({ "--pane-list": `${width}px` } as CSSProperties)} />
        <ResizeHandle
          pane="list"
          side={side}
          target={ref}
          room={() => 2000}
          width={width}
          onLive={(px) => {
            onLive(px);
            setLiveWidth(ref.current, "list", px === null ? null : `${px}px`);
          }}
          onWidth={(px) => {
            onWidth(px);
            setWidth(px);
          }}
        />
      </div>
    );
  }
  await act(async () => root.render(<Pane />));
  return { handle: host.querySelector<HTMLElement>('[role="separator"]')!, pane: host.querySelector<HTMLElement>("[data-pane]")!, onWidth, onLive };
}

const pointer = (el: HTMLElement, type: string, clientX: number) =>
  act(async () => {
    el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientX }));
    await new Promise((r) => requestAnimationFrame(r));
  });

const press = (el: HTMLElement, key: string, init: KeyboardEventInit = {}) =>
  act(async () => {
    el.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init }));
  });

it("is a focusable vertical separator that says the pane's width and limits", async () => {
  const { handle } = await mount("before", 420);
  expect(handle.tabIndex).toBe(0);
  expect(handle.getAttribute("aria-orientation")).toBe("vertical");
  expect(handle.getAttribute("aria-valuenow")).toBe("420");
  expect(handle.getAttribute("aria-valuemin")).toBe(String(paneLimits.list.min));
  expect(handle.getAttribute("aria-valuemax")).toBe(String(paneLimits.list.max));
  expect(handle.getAttribute("aria-label")).toBe("Resize the ticket list");
});

it("widens a pane left of it with ArrowRight and narrows it with ArrowLeft", async () => {
  const { handle, onWidth } = await mount("before");
  await press(handle, "ArrowRight");
  expect(onWidth).toHaveBeenLastCalledWith(380 + STEP);
  expect(handle.getAttribute("aria-valuenow")).toBe(String(380 + STEP));
  await press(handle, "ArrowLeft");
  expect(onWidth).toHaveBeenLastCalledWith(380);
});

it("widens a pane right of it with ArrowLeft", async () => {
  const { handle, onWidth } = await mount("after", 300);
  await press(handle, "ArrowLeft");
  expect(onWidth).toHaveBeenLastCalledWith(300 + STEP);
});

it("goes to the ends with Home and End, and leaves other keys to the page", async () => {
  const { handle, onWidth } = await mount("before");
  await press(handle, "End");
  expect(onWidth).toHaveBeenLastCalledWith(paneLimits.list.max);
  await press(handle, "Home");
  expect(onWidth).toHaveBeenLastCalledWith(paneLimits.list.min);
  onWidth.mockClear();
  const event = new KeyboardEvent("keydown", { key: "j", bubbles: true, cancelable: true });
  await act(async () => void handle.dispatchEvent(event));
  expect(event.defaultPrevented).toBe(false);
  await press(handle, "ArrowRight", { metaKey: true });
  expect(onWidth).not.toHaveBeenCalled();
});

it("goes back to the default on a double click", async () => {
  const { handle, onWidth } = await mount("before", 500);
  await act(async () => void handle.dispatchEvent(new MouseEvent("dblclick", { bubbles: true })));
  expect(onWidth).toHaveBeenLastCalledWith(null);
});

it("leaves the page alone on a press that does not move", async () => {
  const { handle, onWidth, onLive } = await mount("before", 500);
  await pointer(handle, "pointerdown", 100);
  await pointer(handle, "pointerup", 100);
  expect(onLive).not.toHaveBeenCalled();
  expect(onWidth).not.toHaveBeenCalled();
});

it("shows the width live while dragging, saves it on release and lifts the live one", async () => {
  const { handle, pane, onWidth, onLive } = await mount("before");
  await pointer(handle, "pointerdown", 100);
  expect("paneResizing" in document.documentElement.dataset).toBe(true);
  await pointer(handle, "pointermove", 150);
  expect(onLive).toHaveBeenLastCalledWith(430);
  expect(live(pane)).toBe("430px");
  expect(onWidth).not.toHaveBeenCalled();
  await pointer(handle, "pointerup", 150);
  expect(onWidth).toHaveBeenLastCalledWith(430);
  expect(onLive).toHaveBeenLastCalledWith(null);
  expect(live(pane)).toBe("");
  expect(widthOf(pane)).toBe(430);
  expect("paneResizing" in document.documentElement.dataset).toBe(false);
});

it("lifts the live width without saving when the handle goes away mid-drag", async () => {
  const { handle, onWidth, onLive } = await mount("before", 400);
  await pointer(handle, "pointerdown", 100);
  await pointer(handle, "pointermove", 60);
  expect(onLive).toHaveBeenLastCalledWith(360);
  await act(async () => root.render(<div />));
  expect(onLive).toHaveBeenLastCalledWith(null);
  expect(onWidth).not.toHaveBeenCalled();
  expect("paneResizing" in document.documentElement.dataset).toBe(false);
});
