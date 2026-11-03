import { cn } from "@/lib/cn";
import type { ContactRef } from "@/lib/api/types";

/**
 * Who a ticket is from, as a person would recognise them: the contact's name, or their email
 * address when the name says nothing more than the address does: empty, the address itself,
 * or only its local part (a contact named "service" from service@paypal.be). "" without
 * either.
 */
export function requesterLabel(contact: Pick<ContactRef, "name" | "email"> | null | undefined): string {
  const name = contact?.name?.trim() ?? "";
  const email = contact?.email?.trim() ?? "";
  if (!email) return name;
  if (!name) return email;
  const n = name.toLowerCase();
  const e = email.toLowerCase();
  const local = e.split("@")[0];
  return n === e || n === local ? email : name;
}

/**
 * A ticket's requester as a column of its own in a list of tickets: a fixed width so the
 * column lines up from row to row (empty without a contact), the label truncated, the email
 * address on hover.
 */
export function Requester({ contact, className }: { contact: Pick<ContactRef, "name" | "email"> | null | undefined; className?: string }) {
  const label = requesterLabel(contact);
  return (
    <span className={cn("w-24 shrink-0 truncate text-[12.5px] text-fg-2 sm:w-36", className)} title={contact?.email ?? undefined} data-ph-mask>
      {label}
    </span>
  );
}
