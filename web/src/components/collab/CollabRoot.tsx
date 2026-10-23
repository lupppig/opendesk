// What Collab adds to the shell when it is on: the customize sidebar dialog and the
// floating composer for new mail. Loaded only for workspaces with Collab.

import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronUp, Minus, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useMailAccounts } from "@/lib/collab/hooks";
import { compose, useCompose } from "@/lib/collab/compose";
import { defaultIdentity, identitySignature } from "@/lib/collab/identities";
import { springs } from "@/lib/motion";
import { Tooltip } from "@/components/ui/tooltip";
import { CustomizeSidebar } from "./CustomizeSidebar";
import { draftFromMessage, MailComposer, startDraft } from "./MailComposer";

function ComposeDock() {
  const { t } = useTranslation("collab");
  const reduce = useReducedMotion();
  const { open, minimized, draft, key } = useCompose();
  const { data: accounts } = useMailAccounts();
  const [picked, setPicked] = useState<{ key: number; id: string } | null>(null);
  const usable = accounts ?? [];
  const wanted = draft.fromDraft?.account_id ?? draft.accountId;
  const accountId = (picked?.key === key ? picked.id : undefined) ?? (wanted && usable.some((a) => a.id === wanted) ? wanted : usable[0]?.id);
  const account = usable.find((a) => a.id === accountId);
  // New mail starts from the account's default address (Gmail's default send-as).
  const from = account ? defaultIdentity(account) : undefined;

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          key={key}
          role="dialog"
          aria-label={t("composer.newMessage")}
          initial={reduce ? { opacity: 0 } : { opacity: 0, y: 24, scale: 0.98 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={reduce ? { opacity: 0 } : { opacity: 0, y: 16, scale: 0.98 }}
          transition={reduce ? { duration: 0.12 } : springs.pop}
          className="fixed bottom-0 right-4 z-40 flex w-[min(600px,calc(100vw-32px))] flex-col overflow-hidden rounded-t-xl border border-b-0 border-line bg-raised shadow-float"
        >
          <div className="flex h-10 shrink-0 items-center gap-2 border-b border-line bg-sunken px-3">
            <button type="button" onClick={() => compose.minimize(!minimized)} className="min-w-0 flex-1 truncate text-left text-[13px] font-semibold tracking-[-0.01em] outline-none">
              {draft.fromDraft ? draft.fromDraft.subject || t("composer.draft") : t("composer.newMessage")}
            </button>
            <Tooltip content={minimized ? t("composer.restore") : t("composer.minimize")}>
              <button type="button" aria-label={minimized ? t("composer.restore") : t("composer.minimize")} onClick={() => compose.minimize(!minimized)} className="flex size-6 items-center justify-center rounded-md text-fg-3 hover:bg-hover hover:text-fg">
                {minimized ? <ChevronUp className="size-3.5" /> : <Minus className="size-3.5" />}
              </button>
            </Tooltip>
            <Tooltip content={t("composer.close")}>
              <button type="button" aria-label={t("composer.close")} onClick={() => compose.close()} className="flex size-6 items-center justify-center rounded-md text-fg-3 hover:bg-hover hover:text-fg">
                <X className="size-3.5" />
              </button>
            </Tooltip>
          </div>
          <div className={minimized ? "hidden" : "flex max-h-[min(640px,calc(100vh-120px))] min-h-[360px] flex-col"}>
            {account && from ? (
              <MailComposer
                key={key}
                variant="floating"
                accounts={usable}
                accountId={account.id}
                onAccountChange={(id) => setPicked({ key, id })}
                initial={
                  draft.fromDraft
                    ? { ...draftFromMessage(draft.fromDraft), mode: "new" }
                    : { ...startDraft("new", { ownEmail: account.email, signature: identitySignature(from), subject: draft.subject, from: from.email }), to: draft.to ?? [], html: draft.html ?? startDraft("new", { ownEmail: account.email, signature: identitySignature(from) }).html }
                }
                autoFocus
                onSent={() => compose.close()}
                onDiscard={() => compose.close()}
              />
            ) : (
              <p className="p-5 text-[12.5px] text-fg-3">{t("composer.noAccount")}</p>
            )}
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}

export default function CollabRoot() {
  return (
    <>
      <CustomizeSidebar />
      <ComposeDock />
    </>
  );
}
