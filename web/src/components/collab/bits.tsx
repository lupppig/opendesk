// Small pieces every Collab surface shares: an account's colour dot, box and folder
// icons, label pills and the provider mark.

import { Archive, AtSign, File, FileEdit, Folder, Inbox, Layers, Mail, MailOpen, Send, ShieldAlert, Star, Tag, Trash2, UserCheck, Users } from "lucide-react";
import type { ReactNode } from "react";
import type { MailBox, MailFolder, MailProvider } from "@/lib/api/types";
import { ProviderLogo } from "@/components/mailboxes/ProviderLogo";
import { cn } from "@/lib/cn";
import { isTone, tone } from "@/lib/tones";

/** The tone keys an account can take. */
export const ACCOUNT_TONES = ["gray", "red", "orange", "amber", "green", "teal", "blue", "indigo", "violet", "pink"] as const;

export function AccountDot({ color, className, title }: { color: string; className?: string; title?: string }) {
  const t = isTone(color) ? color : "gray";
  return <span aria-hidden={!title} title={title} className={cn("inline-block size-2 shrink-0 rounded-full", tone[t].solid, className)} />;
}

export const boxIcon: Record<MailBox | "folder", ReactNode> = {
  inbox: <Inbox />,
  unread: <MailOpen />,
  starred: <Star />,
  sent: <Send />,
  drafts: <FileEdit />,
  archive: <Archive />,
  trash: <Trash2 />,
  spam: <ShieldAlert />,
  all: <Layers />,
  shared: <Users />,
  assigned: <UserCheck />,
  folder: <Folder />,
};

/** A user folder's or label's icon; a Gmail label shows its colour. */
export function FolderGlyph({ folder, className }: { folder: Pick<MailFolder, "kind" | "color"> | null; className?: string }) {
  if (folder?.kind === "label")
    return (
      <span className={cn("flex size-[15px] shrink-0 items-center justify-center", className)}>
        {folder.color ? <span className="size-2 rotate-45 rounded-[2px]" style={{ backgroundColor: folder.color }} /> : <Tag className="size-[13px] text-fg-3" />}
      </span>
    );
  return <Folder className={cn("size-[15px] shrink-0", className)} />;
}

/** A label on a conversation: a soft pill, its Gmail colour as the dot. */
export function LabelPill({ name, color, className }: { name: string; color?: string | null; className?: string }) {
  return (
    <span className={cn("inline-flex h-[18px] max-w-[140px] shrink-0 items-center gap-1 rounded-sm bg-hover pl-1.5 pr-1.5 text-[10.5px] font-medium text-fg-2", className)}>
      <span className="size-1.5 shrink-0 rounded-[2px] bg-fg-4" style={color ? { backgroundColor: color } : undefined} />
      <span className="truncate">{name}</span>
    </span>
  );
}

export function ProviderMark({ provider, size = "sm" }: { provider: MailProvider | string; size?: "sm" | "md" | "lg" }) {
  return <ProviderLogo host={provider === "google" || provider === "microsoft" ? provider : ""} label={provider === "imap" ? "IMAP" : provider} size={size} />;
}

export const MailIcon = Mail;
export const MentionIcon = AtSign;
export const FileIcon = File;
