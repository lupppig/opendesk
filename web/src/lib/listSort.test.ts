import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { User } from "./api/types";
import { listId, listSortKey, normalSort, rememberedSort, rememberSort, withRememberedSort } from "./listSort";
import { useSession } from "./session";

const signIn = (uid: string, workspaceId = "ws1") => useSession.setState({ user: { id: uid } as User, workspaceId });

beforeEach(() => {
  localStorage.clear();
  signIn("u1");
});
afterEach(() => {
  vi.restoreAllMocks();
  useSession.setState({ user: null, workspaceId: null });
});

describe("list ids", () => {
  it("names folders, views and team inboxes apart", () => {
    expect(listId({ folder: "open" })).toBe("folder:open");
    expect(listId({ viewId: "v1" })).toBe("view:v1");
    expect(listId({ teamId: "mine" })).toBe("team:mine");
    expect(listId({})).toBeNull();
  });
});

describe("normalSort", () => {
  it("leaves out what the list does by default", () => {
    expect(normalSort({})).toEqual({});
    expect(normalSort({ sort: "last_message_at", order: "desc" })).toEqual({});
    expect(normalSort({ order: "desc" })).toEqual({});
    expect(normalSort({ sort: "due", order: "asc" })).toEqual({ sort: "due" });
    expect(normalSort({ sort: "due", order: "desc" })).toEqual({ sort: "due", order: "desc" });
    expect(normalSort({ sort: "created_at", order: "desc" })).toEqual({ sort: "created_at" });
    expect(normalSort({ order: "asc" })).toEqual({ order: "asc" });
  });
});

describe("remembering a list's sort", () => {
  it("brings back the order chosen for that list only", () => {
    rememberSort({ folder: "open" }, { order: "asc" });
    expect(rememberedSort({ folder: "open" })).toEqual({ order: "asc" });
    expect(rememberedSort({ folder: "pending" })).toBeNull();
    expect(rememberedSort({ viewId: "open" })).toBeNull();
    expect(localStorage.getItem(listSortKey("ws1", "u1", "folder:open"))).toBe('{"order":"asc"}');
  });

  it("keeps each member's and each workspace's apart", () => {
    rememberSort({ viewId: "v1" }, { sort: "priority", order: "asc" });
    signIn("u2");
    expect(rememberedSort({ viewId: "v1" })).toBeNull();
    signIn("u1", "ws2");
    expect(rememberedSort({ viewId: "v1" })).toBeNull();
    signIn("u1");
    expect(rememberedSort({ viewId: "v1" })).toEqual({ sort: "priority", order: "asc" });
  });

  it("forgets it when the default order is chosen again", () => {
    rememberSort({ teamId: "t1" }, { sort: "created_at", order: "asc" });
    rememberSort({ teamId: "t1" }, { sort: undefined, order: undefined });
    expect(rememberedSort({ teamId: "t1" })).toBeNull();
    rememberSort({ teamId: "t1" }, { order: "asc" });
    rememberSort({ teamId: "t1" }, { order: "desc" });
    expect(localStorage.getItem(listSortKey("ws1", "u1", "team:t1"))).toBeNull();
  });

  it("ignores what it can't read", () => {
    const key = listSortKey("ws1", "u1", "folder:open");
    localStorage.setItem(key, "not json");
    expect(rememberedSort({ folder: "open" })).toBeNull();
    localStorage.setItem(key, '{"sort":"drop table","order":"sideways"}');
    expect(rememberedSort({ folder: "open" })).toBeNull();
    localStorage.setItem(key, '{"sort":"field.order_ref"}');
    expect(rememberedSort({ folder: "open" })).toEqual({ sort: "field.order_ref" });
  });

  it("does nothing signed out or when storage is unavailable", () => {
    useSession.setState({ user: null, workspaceId: null });
    rememberSort({ folder: "open" }, { order: "asc" });
    expect(localStorage.length).toBe(0);
    signIn("u1");
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => rememberSort({ folder: "open" }, { order: "asc" })).not.toThrow();
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(rememberedSort({ folder: "open" })).toBeNull();
  });
});

describe("withRememberedSort", () => {
  it("adds the remembered sort to a URL that names none, keeping its filters", () => {
    rememberSort({ folder: "open" }, { order: "asc" });
    expect(withRememberedSort({ folder: "open" }, { priority: "urgent" })).toEqual({ priority: "urgent", order: "asc" });
  });

  it("lets a sort in the URL win", () => {
    rememberSort({ folder: "open" }, { order: "asc" });
    expect(withRememberedSort({ folder: "open" }, { sort: "priority" })).toBeNull();
    expect(withRememberedSort({ folder: "open" }, { order: "desc" })).toBeNull();
  });

  it("leaves the URL alone when nothing is remembered", () => {
    expect(withRememberedSort({ folder: "open" }, {})).toBeNull();
  });
});
