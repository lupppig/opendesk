import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isPressTarget, useHotkeys, type HotkeyMap } from "./hotkeys";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const el = (html: string) => {
  const host = document.createElement("div");
  host.innerHTML = html;
  return host.firstElementChild!;
};

describe("isPressTarget", () => {
  it("knows the controls Enter and Space act on, and what is inside them", () => {
    for (const html of [
      "<button>Fold</button>",
      '<a href="/inbox">Inbox</a>',
      '<input type="checkbox">',
      "<select></select>",
      "<summary>More</summary>",
      '<div role="button" tabindex="0">Card</div>',
      '<span role="switch" tabindex="0"></span>',
      '<div role="menuitem" tabindex="-1">Open</div>',
      '<div contenteditable="true"></div>',
    ])
      expect(isPressTarget(el(html)), html).toBe(true);
    const link = el('<a href="/x"><span>Order S00207</span></a>');
    expect(isPressTarget(link.querySelector("span"))).toBe(true);
  });

  it("leaves out what is not a control: a ticket row, a link without href, the page", () => {
    for (const html of ['<div role="option" tabindex="-1">Ticket</div>', "<a>Not a link</a>", '<div contenteditable="false"></div>', "<p>Text</p>"])
      expect(isPressTarget(el(html)), html).toBe(false);
    expect(isPressTarget(document.body)).toBe(false);
    expect(isPressTarget(null)).toBe(false);
  });
});

describe("useHotkeys", () => {
  let host: HTMLDivElement;
  let root: Root;
  const map = { enter: vi.fn(), space: vi.fn(), j: vi.fn(), "mod+enter": vi.fn() };

  function Page({ keys }: { keys: HotkeyMap }) {
    useHotkeys(keys);
    return (
      <>
        <button type="button">Fold</button>
        <a href="/orders/207">
          <span>Order S00207</span>
        </a>
        <div role="option" aria-selected tabIndex={-1}>
          Ticket
        </div>
      </>
    );
  }

  beforeEach(async () => {
    Object.values(map).forEach((f) => f.mockClear());
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    await act(async () => root.render(<Page keys={map} />));
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  /** Presses key on target as the browser does, and says whether the page cancelled it. */
  const press = (target: Element, key: string, init: KeyboardEventInit = {}) => {
    const e = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
    target.dispatchEvent(e);
    return e.defaultPrevented;
  };

  it("runs Enter and Space with nothing pressable focused, and on a ticket row", () => {
    expect(press(document.body, "Enter")).toBe(true);
    expect(press(host.querySelector('[role="option"]')!, "Enter")).toBe(true);
    expect(press(document.body, " ")).toBe(true);
    expect(map.enter).toHaveBeenCalledTimes(2);
    expect(map.space).toHaveBeenCalledTimes(1);
  });

  it("leaves Enter and Space to a focused button or link, so the browser still presses it", () => {
    const button = host.querySelector("button")!;
    button.focus();
    expect(press(button, "Enter")).toBe(false);
    expect(press(button, " ")).toBe(false);
    expect(press(host.querySelector("a span")!, "Enter")).toBe(false);
    expect(map.enter).not.toHaveBeenCalled();
    expect(map.space).not.toHaveBeenCalled();
  });

  it("keeps the other keys, and Enter with the mod key, on a focused button", () => {
    const button = host.querySelector("button")!;
    expect(press(button, "j")).toBe(true);
    expect(press(button, "Enter", { ctrlKey: true })).toBe(true);
    expect(map.j).toHaveBeenCalledTimes(1);
    expect(map["mod+enter"]).toHaveBeenCalledTimes(1);
    expect(map.enter).not.toHaveBeenCalled();
  });
});
