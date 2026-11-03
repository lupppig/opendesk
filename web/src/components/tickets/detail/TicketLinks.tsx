// What a ticket points to in other tools: issues, tasks, pages. Ollie adds them when it
// creates something from the ticket; realtime keeps the list current.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { TextInput } from "@/components/ui/input";
import { ProviderMark } from "@/components/settings2/integrations";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import { appForUrl, appLogo } from "@/lib/appLogos";
import type { TicketLink } from "@/lib/api/types";
import { ease } from "@/lib/motion";
import { q, qk, removeFromList, upsertInList } from "@/lib/queries";
import { SetByOllie, useAssistantName } from "../ollie";

function href(l: TicketLink): string | null {
  try {
    const u = new URL(l.url);
    return u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

export function TicketLinks({ ticketId, canEdit }: { ticketId: string; canEdit: boolean }) {
  const { t } = useTranslation("inbox");
  const qc = useQueryClient();
  const reduce = useReducedMotion();
  const ollie = useAssistantName();
  const [addOpen, setAddOpen] = useState(false);
  const [url, setUrl] = useState("");
  const [title, setTitle] = useState("");
  const [addError, setAddError] = useState<string | null>(null);
  const { data: links } = useQuery(q.ticketLinks(ticketId));
  const { data: integrations } = useQuery({ ...q.integrations(), enabled: !!links?.length });

  const add = useMutation({
    mutationFn: (input: { url: string; title?: string }) => api.tickets.addLink(ticketId, input),
    onSuccess: (link) => {
      qc.setQueryData<TicketLink[]>(qk.ticketLinks(ticketId), (current) => {
        if (!current) return [link];
        const index = current.findIndex((item) => item.id === link.id);
        if (index < 0) return [...current, link];
        const next = current.slice();
        next[index] = link;
        return next;
      });
      setAddOpen(false);
      setUrl("");
      setTitle("");
      setAddError(null);
      toast.success(t("links.added"));
    },
    onError: (error) => setAddError(errorMessage(error)),
  });

  const remove = useMutation({
    mutationFn: (l: TicketLink) => api.tickets.removeLink(ticketId, l.id),
    onMutate: (l) => removeFromList(qc, qk.ticketLinks(ticketId), l.id),
    onSuccess: (_r, l) =>
      toast.undo(t("links.removed"), () => {
        void api.tickets
          .addLink(ticketId, { url: l.url, title: l.title, integration_id: l.integration_id ?? undefined })
          .then((row) => upsertInList(qc, qk.ticketLinks(ticketId), row));
      }),
    onError: (e, l) => {
      upsertInList(qc, qk.ticketLinks(ticketId), l);
      toast.error(e);
    },
  });

  if (!links?.length && !canEdit) return null;
  const integrationOf = (l: TicketLink) => integrations?.find((x) => x.id === l.integration_id);
  const nameOf = (l: TicketLink) => integrationOf(l)?.name ?? new URL(l.url).host;
  // A plain link, or one from a custom server, shows the logo of the product it points to.
  const appOf = (l: TicketLink) => (appLogo(l.provider) ? l.provider : (appForUrl(l.url) ?? l.provider));

  return (
    <section className="border-b border-line-soft px-4 py-3.5" aria-label={t("links.title")}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="label-caps">{t("links.title")}</h3>
        {canEdit && (
          <Button variant="ghost" size="xs" icon={<Plus />} onClick={() => setAddOpen(true)}>
            {t("links.add")}
          </Button>
        )}
      </div>
      {!!links?.length && (
        <ul className="flex flex-col gap-0.5">
          <AnimatePresence initial={false}>
            {links.map((l) => {
              const url = href(l);
              const source = nameOf(l);
              return (
                <motion.li
                  key={l.id}
                  layout={reduce ? false : "position"}
                  initial={reduce ? { opacity: 0 } : { opacity: 0, height: 0 }}
                  animate={reduce ? { opacity: 1 } : { opacity: 1, height: "auto" }}
                  exit={reduce ? { opacity: 0 } : { opacity: 0, height: 0 }}
                  transition={{ duration: 0.2, ease: ease.out }}
                  className="group -mx-1.5 flex items-center gap-2 rounded-md px-1.5 py-1 transition-colors hover:bg-sunken"
                >
                  <span className="relative">
                    <ProviderMark provider={appOf(l)} name={source} icon={integrationOf(l)?.icon_url} size="sm" />
                    {l.actor_type === "ai" && <SetByOllie size={10} note={t("links.byOllie", { name: ollie })} className="absolute -right-1 -top-1 ring-1 ring-surface" />}
                  </span>
                  {url ? (
                    <a href={url} target="_blank" rel="noopener noreferrer" className="min-w-0 flex-1 outline-none" title={l.url}>
                      <span className="block truncate text-[12.5px] text-fg-2 group-hover:text-fg">{l.title || source}</span>
                      <span className="block truncate text-[11px] text-fg-4">{source}</span>
                    </a>
                  ) : (
                    <span className="min-w-0 flex-1 truncate text-[12.5px] text-fg-3">{l.title}</span>
                  )}
                  {canEdit && (
                    <Tooltip content={t("links.remove")}>
                      <button
                        type="button"
                        aria-label={t("links.removeNamed", { title: l.title || source })}
                        onClick={() => remove.mutate(l)}
                        className="flex size-5 shrink-0 items-center justify-center rounded-sm text-fg-4 opacity-0 transition-opacity hover:bg-hover hover:text-fg focus-visible:opacity-100 group-hover:opacity-100"
                      >
                        <X className="size-3" />
                      </button>
                    </Tooltip>
                  )}
                </motion.li>
              );
            })}
          </AnimatePresence>
        </ul>
      )}
      <Dialog
        open={addOpen}
        onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) {
            setUrl("");
            setTitle("");
            setAddError(null);
          }
        }}
        title={t("links.addTitle")}
        description={t("links.addDescription")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setAddOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" form="add-ticket-link" loading={add.isPending}>
              {t("links.add")}
            </Button>
          </>
        }
      >
        <form
          id="add-ticket-link"
          className="flex flex-col gap-3"
          onSubmit={(event: FormEvent<HTMLFormElement>) => {
            event.preventDefault();
            setAddError(null);
            let parsedURL: URL;
            try {
              parsedURL = new URL(url.trim());
            } catch {
              setAddError(t("links.httpsRequired"));
              return;
            }
            if (parsedURL.protocol !== "https:") {
              setAddError(t("links.httpsRequired"));
              return;
            }
            add.mutate({ url: parsedURL.href, title: title.trim() || undefined });
          }}
        >
          <label className="flex flex-col gap-1.5 text-[12.5px] font-medium text-fg-2">
            {t("links.url")}
            <TextInput
              type="url"
              inputSize="lg"
              value={url}
              onChange={(event) => setUrl(event.currentTarget.value)}
              placeholder="https://linear.app/acme/issue/ENG-123"
              required
              autoFocus
            />
          </label>
          <label className="flex flex-col gap-1.5 text-[12.5px] font-medium text-fg-2">
            {t("links.linkTitle")}
            <TextInput
              inputSize="lg"
              value={title}
              onChange={(event) => setTitle(event.currentTarget.value)}
              maxLength={300}
            />
          </label>
          {addError && <p role="alert" className="text-xs text-danger-fg">{addError}</p>}
        </form>
      </Dialog>
    </section>
  );
}
