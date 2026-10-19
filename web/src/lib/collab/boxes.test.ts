import { describe, expect, it } from "vitest";
import type { MailFolder } from "../api/types";
import { buildFolderTree, flattenTree, flattenWithout, mailListQuery } from "./boxes";
import { applyAction, inverseAction, leavesList } from "./actions";

const f = (id: string, path: string, role: MailFolder["role"] = null): MailFolder => ({
  id,
  account_id: "a1",
  name: path.split("/").pop()!,
  path,
  role,
  kind: "folder",
  unread: 0,
  total: 0,
  color: null,
  can_rename: !role,
});

describe("folder tree", () => {
  it("nests by path, sorts by name, skips special folders and fills missing parents", () => {
    const tree = buildFolderTree([f("i", "INBOX", "inbox"), f("2", "Clients/Acme"), f("3", "Archive 2024"), f("4", "Clients/Beta"), f("5", "Vendors/Old/Gone")]);
    const flat = flattenTree(tree).map(({ node, depth }) => `${depth}:${node.name}:${node.folder?.id ?? "-"}`);
    expect(flat).toEqual(["0:Archive 2024:3", "0:Clients:-", "1:Acme:2", "1:Beta:4", "0:Vendors:-", "1:Old:-", "2:Gone:5"]);
  });
});

describe("folders without the tabs", () => {
  it("unindents a folder whose parent is left out and names it by path", () => {
    const tree = buildFolderTree([f("c", "Clients"), f("a", "Clients/Acme"), f("x", "Clients/Acme/Old"), f("v", "Vendors")]);
    expect(flattenWithout(tree, () => false).map((x) => [x.label, x.depth])).toEqual([["Clients", 0], ["Acme", 1], ["Old", 2], ["Vendors", 0]]);
    expect(flattenWithout(tree, (x) => x.id === "c").map((x) => [x.label, x.depth])).toEqual([["Clients/Acme", 0], ["Old", 1], ["Vendors", 0]]);
  });
});

describe("list query", () => {
  it("a folder replaces the box", () => {
    expect(mailListQuery("folder", { folder: "f1", account: "a1", q: "x" })).toEqual({ folder_id: "f1", q: "x" });
    expect(mailListQuery("starred", { account: "a1" })).toEqual({ box: "starred", account_id: "a1" });
    expect(mailListQuery("nope", {})).toEqual({ box: "inbox" });
  });
});

describe("actions", () => {
  const t = { id: "t", account_id: "a1", subject: "", snippet: "", participants: [], last_message_at: "", message_count: 1, unread: true, starred: false, has_attachments: false, folder_ids: ["i"], shared: false, assignees: [], comment_count: 0 };
  it("applies state changes", () => {
    expect(applyAction(t, "read").unread).toBe(false);
    expect(applyAction(t, "star").starred).toBe(true);
    expect(applyAction(t, "label", "l1").folder_ids).toEqual(["i", "l1"]);
    expect(applyAction(t, "unlabel", "i").folder_ids).toEqual([]);
  });
  it("knows which lists a conversation leaves", () => {
    const inbox = { box: "inbox", folderId: null, folderRole: null };
    const all = { box: "all", folderId: null, folderRole: null };
    const folder = { box: null, folderId: "f1", folderRole: null };
    expect(leavesList("archive", inbox)).toBe(true);
    expect(leavesList("archive", all)).toBe(false);
    expect(leavesList("trash", all)).toBe(true);
    expect(leavesList("move", folder, "f1")).toBe(false);
    expect(leavesList("move", folder, "f2")).toBe(true);
    expect(leavesList("read", inbox)).toBe(false);
  });
  it("undoes archive, trash, spam and moves", () => {
    const inbox = { box: "inbox", folderId: null, folderRole: null };
    expect(inverseAction("archive", inbox)).toEqual({ action: "inbox" });
    expect(inverseAction("spam", inbox)).toEqual({ action: "not_spam" });
    expect(inverseAction("move", { box: null, folderId: "f1", folderRole: null })).toEqual({ action: "move", folderId: "f1" });
    expect(inverseAction("move", inbox)).toEqual({ action: "inbox" });
  });
});
