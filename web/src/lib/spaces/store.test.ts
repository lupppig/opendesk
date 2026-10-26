import { beforeEach, describe, expect, it } from "vitest";
import { pickedSpaceOf, spaceKeys, spaceUi, useSpaceUi } from "./store";

describe("what this browser remembers is picked", () => {
  beforeEach(() => {
    localStorage.clear();
    useSpaceUi.setState({ picked: {}, editor: null });
  });

  it("keeps the picked Space per user under od-space:<uid>, and nothing for a workspace", () => {
    spaceUi.pick("u1", "space-a");
    spaceUi.pick("u2", "space-b");
    expect(localStorage.getItem(spaceKeys.picked("u1"))).toBe("space-a");
    expect(localStorage.getItem("od-space:u2")).toBe("space-b");
    expect(pickedSpaceOf("u1")).toBe("space-a");
    spaceUi.pick("u1", null);
    expect(localStorage.getItem("od-space:u1")).toBeNull();
    expect(pickedSpaceOf("u1")).toBeNull();
    expect(pickedSpaceOf("u2")).toBe("space-b");
  });

  it("reads what an earlier page load saved", () => {
    localStorage.setItem("od-space:u3", "space-c");
    expect(pickedSpaceOf("u3")).toBe("space-c");
    expect(pickedSpaceOf("u4")).toBeNull();
  });

  it("opens and closes the editor", () => {
    spaceUi.edit({ kind: "space", id: "x" });
    expect(useSpaceUi.getState().editor).toEqual({ kind: "space", id: "x" });
    spaceUi.closeEditor();
    expect(useSpaceUi.getState().editor).toBeNull();
  });
});
