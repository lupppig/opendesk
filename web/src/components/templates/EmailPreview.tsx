import { useTranslation } from "react-i18next";
// The email as the customer gets it (contracts/templates.md "POST /v1/templates/preview"):
// rendered by the server with the brand's layout, on sample data or a real ticket,
// on a desktop or phone width, with a test send to yourself.

import { keepPreviousData, useMutation, useQuery } from "@tanstack/react-query";
import { ChevronDown, Monitor, Send, Smartphone, Ticket } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Picker } from "@/components/ui/picker";
import { Spinner } from "@/components/ui/spinner";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { TemplatePreviewBody, TemplateProblem } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { useUser } from "@/lib/session";
import { qk } from "@/lib/queries";

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

export function EmailPreview({
  body,
  onProblems,
  height = 520,
  className,
  title,
  collapsible,
  defaultOpen = true,
}: {
  body: TemplatePreviewBody;
  /** A header that shows and hides the email; the template is still checked while hidden. */
  collapsible?: boolean;
  defaultOpen?: boolean;
  /** The server's problems with the template, for the editor to show. */
  onProblems?: (p: TemplateProblem[]) => void;
  height?: number;
  className?: string;
  title?: string;
}) {
  const { t: ui } = useTranslation("ui");
  const user = useUser();
  const [device, setDevice] = useState<"desktop" | "phone">("desktop");
  const [open, setOpen] = useState(defaultOpen || !collapsible);
  const [ticketId, setTicketId] = useState<string | null>(null);
  const key = useMemo(() => JSON.stringify(body), [body]);
  const debounced = useDebounced(key, 350);
  const request = useMemo(() => ({ ...(JSON.parse(debounced) as TemplatePreviewBody), ticket_id: ticketId ?? undefined }), [debounced, ticketId]);

  const preview = useQuery({
    queryKey: qk.of("templates", "preview", request),
    queryFn: () => api.templates.preview(request),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
  });
  useEffect(() => {
    if (preview.isError) onProblems?.([]);
    else if (preview.data) onProblems?.(preview.data.problems ?? []);
  }, [preview.data, preview.isError, onProblems]);

  const tickets = useQuery({
    queryKey: qk.of("templates", "preview-tickets"),
    queryFn: () => api.tickets.list({ limit: 25, sort: "last_message_at", order: "desc" }),
    staleTime: 60_000,
  });
  const ticketItems = (tickets.data?.data ?? []).map((t) => ({
    value: t.id,
    label: `#${t.number} ${t.subject || ui("noSubject")}`,
    keywords: [String(t.number), t.contact?.name ?? "", t.contact?.email ?? ""],
    hint: t.contact?.name,
  }));
  const chosen = tickets.data?.data.find((t) => t.id === ticketId);

  const test = useMutation({
    mutationFn: () => api.templates.test(request),
    onSuccess: () => toast.success(ui("testEmailSent"), { description: ui("checkEmailSoon", { email: user.email }) }),
    onError: (e) => toast.error(e),
  });

  return (
    <div className={cn("flex min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-sunken", className)}>
      <div className={cn("flex flex-wrap items-center gap-2 bg-surface px-2.5 py-1.5", open && "border-b border-line")}>
        {collapsible ? (
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
            className="-ml-1 flex h-7 items-center gap-1.5 rounded-md px-1 text-[12.5px] font-medium outline-none hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
          >
            <ChevronDown className={cn("size-3.5 text-fg-3 transition-transform duration-150 motion-reduce:transition-none", !open && "-rotate-90")} />
            {title ?? ui("preview")}
          </button>
        ) : (
          <span className="text-[12.5px] font-medium">{title ?? ui("preview")}</span>
        )}
        {preview.isFetching && <Spinner className="size-3 text-fg-4" />}
        {open && <div className="ml-auto flex items-center gap-1.5">
          <Picker
            width={320}
            align="end"
            value={ticketId}
            placeholder={ui("searchRecentTickets")}
            emptyText={ui("noTicketsYet")}
            items={[{ value: "", label: ui("sampleData"), hint: "Jane Cooper" }, ...ticketItems]}
            onSelect={(v) => setTicketId(v || null)}
            trigger={
              <button
                type="button"
                className="flex h-7 max-w-[200px] items-center gap-1.5 rounded-md px-2 text-[12px] text-fg-2 transition-colors hover:bg-hover hover:text-fg data-[state=open]:bg-hover"
              >
                <Ticket className="size-3.5 shrink-0 text-fg-3" />
                <span className="truncate">{chosen ? `#${chosen.number}` : ui("sampleData")}</span>
                <ChevronDown className="size-3 shrink-0 text-fg-4" />
              </button>
            }
          />
          <Segmented
            size="xs"
            value={device}
            onChange={setDevice}
            options={[
              { value: "desktop", label: <span className="sr-only">{ui("desktop")}</span>, icon: <Monitor /> },
              { value: "phone", label: <span className="sr-only">{ui("phone")}</span>, icon: <Smartphone /> },
            ]}
          />
          <Tooltip content={ui("sendEmailTo", { email: user.email })}>
            <Button size="xs" icon={<Send />} loading={test.isPending} onClick={() => test.mutate()}>
              {ui("sendTest")}
            </Button>
          </Tooltip>
        </div>}
        {!open && preview.data && (
          <span className="ml-auto min-w-0 truncate text-[12px] text-fg-4" data-ph-mask>
            {preview.data.subject}
          </span>
        )}
      </div>
      {open && preview.data && (
        <div className="flex items-baseline gap-2 border-b border-line-soft bg-surface px-3 py-1.5 text-[12px]">
          <span className="shrink-0 text-fg-4">{ui("subject")}</span>
          <span className="min-w-0 truncate text-fg-2" data-ph-mask>
            {preview.data.subject}
          </span>
        </div>
      )}
      {open && <div className="flex min-h-0 justify-center overflow-hidden p-3" style={{ height }}>
        {preview.isError && !preview.data ? (
          <div className="flex items-center text-center text-[12.5px] text-fg-3">{errorMessage(preview.error, ui("previewFailed"))}</div>
        ) : preview.data ? (
          <iframe
            title={ui("emailPreview")}
            // No scripts and no access to the app; links open in a new tab.
            sandbox="allow-popups allow-popups-to-escape-sandbox"
            srcDoc={preview.data.html}
            className={cn(
              "h-full rounded-md border border-line bg-white shadow-xs transition-[width] duration-200 motion-reduce:transition-none",
              device === "phone" ? "w-[380px]" : "w-full",
            )}
            data-ph-mask
          />
        ) : (
          <div className="flex items-center">
            <Spinner className="size-4 text-fg-4" />
          </div>
        )}
      </div>}
    </div>
  );
}
