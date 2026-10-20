import { expect, test } from "../lib/proof.ts";

test("open a ticket from the unassigned queue", async ({ page, proof }) => {
  await proof.open("/inbox/unassigned");
  const tickets = page.getByRole("listbox", { name: "Tickets" });
  await expect(tickets.getByRole("option").first()).toBeVisible();
  await proof.chapter("Inbox", "The unassigned queue, then one ticket beside it");
  await proof.shot("unassigned", { caption: "The unassigned queue" });

  await tickets.getByRole("option", { name: /Pricing question/ }).click();
  await expect(page.getByRole("main").getByText("Is the Business plan billed per member").last()).toBeVisible();
  await proof.dwell();
  await proof.shot("ticket", { caption: "A ticket open beside the queue" });
});

test("change the contact on a ticket", async ({ page, proof }) => {
  await proof.open("/inbox/unassigned");
  const tickets = page.getByRole("listbox", { name: "Tickets" });
  const exportTicket = tickets.getByRole("option", { name: /Can't export notebook to PDF/ });
  await expect(exportTicket).toBeVisible();
  await exportTicket.click();

  const main = page.getByRole("main");
  await expect(page.getByRole("heading", { name: "Can't export notebook to PDF" })).toBeVisible();
  const ollieNotice = page.getByRole("button", { name: "Not now" });
  if (await ollieNotice.isVisible()) await ollieNotice.click();
  const changeContact = main.getByRole("button", { name: "Change contact" });
  await expect(changeContact).toBeVisible();
  await changeContact.click();

  const search = page.getByRole("combobox", { name: "Search contacts" });
  await expect(search).toBeVisible();
  await search.pressSequentially("Dana", { delay: 60 });
  const dana = page.getByRole("option", { name: /Dana Whitfield/ });
  await expect(dana).toBeVisible();
  await proof.chapter("Change contact", "Find another contact and make them the ticket's customer");
  await proof.shot("contact-picker", { caption: "Search results for the new contact" });

  await dana.click();
  await expect(main.getByText("Dana Whitfield", { exact: true }).last()).toBeVisible();
  await expect(main.getByText(/changed the contact from .* to Dana Whitfield/)).toBeVisible();
  await main.getByRole("heading", { name: "Contact", exact: true }).scrollIntoViewIfNeeded();
  await proof.dwell();
  await proof.shot("contact-changed", { caption: "Dana Whitfield is the customer now, and the timeline records the change" });
});

test("a live chat keeps the visitor who started it", async ({ page, proof }) => {
  await proof.open("/inbox/unassigned/1086");
  await expect(page.getByRole("heading", { name: "Pricing question" })).toBeVisible();
  const main = page.getByRole("main");
  const contact = main.getByRole("heading", { name: "Contact", exact: true });
  await contact.scrollIntoViewIfNeeded();
  await expect(contact).toBeVisible();
  await expect(main.getByRole("button", { name: "Change contact" })).toHaveCount(0);
  await proof.dwell();
  await proof.shot("chat-contact", { caption: "A live chat's contact has no Change contact button" });
});

test("an agent edits and deletes an internal note", async ({ page, proof }) => {
  await proof.open("/inbox/unassigned/1086");
  await expect(page.getByRole("heading", { name: "Pricing question" })).toBeVisible();
  await proof.chapter("Add an internal note", "Write a team-only note on a ticket");

  await page.getByRole("radio", { name: "Note" }).click();
  await page.getByRole("textbox", { name: "Note" }).fill("Follow-up on the billing question.");
  await page.getByRole("button", { name: "Add note", exact: true }).click();

  const note = page.getByRole("article", { name: "Note from Maya Chen" });
  await expect(note.getByText("Follow-up on the billing question.", { exact: true })).toBeVisible();
  await note.getByRole("button", { name: "Note actions" }).click();
  await page.getByRole("menuitem", { name: "Edit note" }).click();
  const ollieNotice = page.getByRole("button", { name: "Not now" });
  if (await ollieNotice.isVisible()) await ollieNotice.click();
  const edit = note.getByRole("textbox", { name: "Edit note" });
  await edit.fill("Confirmed billing is per workspace, ");
  await edit.pressSequentially("@pri", { delay: 60 });
  const mentions = page.getByRole("listbox", { name: "Mention a teammate" });
  await expect(mentions.getByText("Priya Raman")).toBeVisible();
  await proof.shot("note-edit-mention", { caption: "Editing the note, with @mentions as in the composer" });
  await page.keyboard.press("Enter");
  await expect(edit.getByText("@Priya Raman")).toBeVisible();
  await note.getByRole("button", { name: "Save" }).click();
  await expect(note.getByText("Confirmed billing is per workspace,", { exact: false })).toBeVisible();
  await expect(note.getByText("@Priya Raman")).toBeVisible();
  await expect(note.getByText("Edited", { exact: true })).toBeVisible();
  await proof.chapter("Edit the note", "Correct the note, mention a teammate and see its edited marker");
  await proof.dwell();
  await proof.shot("note-edited", { target: note, caption: "The corrected note with its mention, marked as edited" });

  await note.getByRole("button", { name: "Note actions" }).click();
  await page.getByRole("menuitem", { name: "Delete note" }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog.getByText("This note and its attachments will be permanently deleted.")).toBeVisible();
  await proof.chapter("Delete the note", "Confirm deletion and see the timeline record it");
  await dialog.getByRole("button", { name: "Delete note", exact: true }).click();
  const activity = page.getByText("Maya Chen deleted an internal note", { exact: true });
  await expect(activity).toBeVisible();
  if (await ollieNotice.isVisible()) await ollieNotice.click();
  await proof.dwell();
  await proof.shot("note-deleted", { caption: "The deletion recorded in the ticket timeline" });
});

test("play a visitor's voice message in a live chat", async ({ page, proof }) => {
  await proof.open("/inbox/unassigned");
  const tickets = page.getByRole("listbox", { name: "Tickets" });
  await tickets.getByRole("option", { name: /Feature request: nested tags/ }).click();
  const main = page.getByRole("main");
  const voice = main.getByLabel("Voice message.wav", { exact: true });
  await expect(voice).toBeVisible();
  await expect(main.getByRole("link", { name: "Download Voice message.wav" })).toBeVisible();
  await proof.chapter("Voice messages", "A visitor's recording plays in the thread, with a download link");
  await voice.scrollIntoViewIfNeeded();
  await proof.dwell();
  await proof.shot("voice-message", { caption: "The visitor's voice message under their messages" });
});
