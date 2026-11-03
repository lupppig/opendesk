// The workspace's Collab card: the hidden collab plan's access (offered or
// complimentary), whether Collab is on, and the actions that change it
// (contracts/admin.md "Collab access").

import { ChevronDown, Gift, Mail, XCircle } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ActionMenuItem } from "@/components/ActionButton";
import { FlagPill } from "@/components/pills";
import { KV, Notice, Section } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Select } from "@/components/ui/select";
import { toast } from "@/components/ui/toast";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AdminWorkspace, CollabAccess } from "@/lib/api/types";
import { UNTIL_CHOICES, untilFor, ymd, type UntilChoice } from "@/lib/collab";
import { bytes, number, shortDate } from "@/lib/format";

function AccessLabel({ access, until }: { access: CollabAccess; until: string | null }) {
  const { t } = useTranslation(["workspaces", "common", "health"]);
  if (access === "offered") return <Badge tone="blue">{t("collab.offered")}</Badge>;
  if (access === "complimentary")
    return (
      <span className="flex flex-wrap items-center gap-2">
        <Badge tone="indigo">{t("collab.complimentary")}</Badge>
        <span className="text-[12px] text-fg-3">{until ? t("collab.untilDate", { date: shortDate(until) }) : t("collab.noEnd")}</span>
      </span>
    );
  return <span className="text-fg-3">{t("common:none")}</span>;
}

export function CollabSection({ w, billing, onSaved }: { w: AdminWorkspace; billing: boolean; onSaved: (w: AdminWorkspace) => void }) {
  const { t } = useTranslation(["workspaces", "common", "health"]);
  const c = w.collab;
  const confirm = useConfirm();
  const [dialog, setDialog] = useState<null | "offered" | "complimentary">(null);

  const remove = () =>
    confirm.show(
      c.access === "complimentary" ? (
        <Trans t={t} i18nKey="collab.remove.complimentary" values={{ name: w.name }} components={{ b: <span className="font-medium text-fg" /> }} />
      ) : (
        <Trans t={t} i18nKey="collab.remove.offered" values={{ name: w.name }} components={{ b: <span className="font-medium text-fg" /> }} />
      ),
      async () => {
        onSaved(await api.workspaces.setCollab(w.id, { access: "none", note: c.note }));
        toast.success(t("collab.remove.done"));
      },
      { title: t("collab.remove.title"), confirmLabel: t("collab.remove.button"), danger: true },
    );

  return (
    <Section
      title={t("collab.title")}
      actions={
        <Menu>
          <MenuTrigger asChild>
            <Button variant="secondary" size="xs" iconRight={<ChevronDown />}>
              {t("collab.change")}
            </Button>
          </MenuTrigger>
          <MenuContent className="w-[250px]">
            {c.access !== "offered" && (
              <ActionMenuItem perm="workspaces.limits" icon={<Mail />} onSelect={() => setDialog("offered")}>
                {t("collab.offer.title")}
              </ActionMenuItem>
            )}
            <ActionMenuItem perm="workspaces.limits" icon={<Gift />} onSelect={() => setDialog("complimentary")}>
              {c.access === "complimentary" ? t("collab.free.change") : t("collab.free.title")}
            </ActionMenuItem>
            {c.access !== "none" && (
              <>
                <MenuSeparator />
                <ActionMenuItem perm="workspaces.limits" icon={<XCircle />} danger onSelect={remove}>
                  {t("collab.remove.button")}
                </ActionMenuItem>
              </>
            )}
          </MenuContent>
        </Menu>
      }
    >
      <KV
        items={[
          [t("collab.access"), <AccessLabel access={c.access} until={c.until} />],
          [t("collab.title"), <FlagPill on={c.enabled} yes={t("health:on")} no={t("health:off")} />],
          [t("collab.accounts"), <span className="mono tabular-nums">{number(c.accounts)}</span>],
          [
            t("collab.stored"),
            <span className="mono tabular-nums">
              {bytes(c.storage_used_bytes)}
              <span className="text-fg-3"> {w.entitlements.collab_storage_gb === 0 ? t("collab.ofUnlimited") : t("collab.ofGb", { gb: number(w.entitlements.collab_storage_gb) })}</span>
            </span>,
          ],
          ...(c.note ? ([[t("collab.note"), <span className="whitespace-pre-wrap text-fg-2">{c.note}</span>]] as [React.ReactNode, React.ReactNode][]) : []),
        ]}
      />
      <p className="mt-3 text-[11.5px] leading-4 text-fg-3">
        {billing
          ? t("collab.hintBilling")
          : t("collab.hintNoBilling")}
      </p>

      {dialog && <CollabDialog w={w} mode={dialog} billing={billing} onClose={() => setDialog(null)} onSaved={onSaved} />}
    </Section>
  );
}

function CollabDialog({
  w,
  mode,
  billing,
  onClose,
  onSaved,
}: {
  w: AdminWorkspace;
  mode: "offered" | "complimentary";
  billing: boolean;
  onClose: () => void;
  onSaved: (w: AdminWorkspace) => void;
}) {
  const { t } = useTranslation(["workspaces", "common", "health"]);
  const c = w.collab;
  const editing = mode === "complimentary" && c.access === "complimentary";
  const [choice, setChoice] = useState<UntilChoice>(editing && c.until ? "date" : "none");
  const [date, setDate] = useState(editing && c.until ? ymd(new Date(c.until)) : "");
  const [note, setNote] = useState(c.note);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [touched, setTouched] = useState(false);

  const until = mode === "complimentary" ? untilFor(choice, date) : { until: null };
  const dateError = "error" in until ? until.error : undefined;
  const n = note.trim();
  const ok = !dateError && n.length <= 200;

  const save = async () => {
    setTouched(true);
    if (!ok || busy || "error" in until) return;
    setBusy(true);
    setError(null);
    try {
      onSaved(await api.workspaces.setCollab(w.id, { access: mode, until: until.until, note: n }));
      toast.success(mode === "offered" ? t("collab.offer.done") : until.until ? t("collab.free.doneUntil", { date: shortDate(until.until) }) : t("collab.free.done"));
      onClose();
    } catch (e) {
      setBusy(false);
      setError(errorMessage(e));
    }
  };

  const seats = w.subscription?.billed_seats ?? w.members_list.filter((m) => m.role !== "viewer").length;
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && !busy && onClose()}
      title={mode === "offered" ? t("collab.offer.title") : editing ? t("collab.free.changeTitle") : t("collab.free.title")}
      description={mode === "offered" ? t("collab.offer.description", { name: w.name }) : t("collab.free.description", { name: w.name, count: Math.max(seats, 1), n: number(Math.max(seats, 1)) })}
      size="sm"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            {t("common:actions.cancel")}
          </Button>
          <Button variant="primary" loading={busy} disabled={!ok && touched} onClick={() => void save()}>
            {mode === "offered" ? t("collab.offer.confirm") : editing ? t("common:actions.save") : t("collab.free.confirm")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        {!billing && <Notice kind="info">{t("collab.noBilling")}</Notice>}
        {mode === "complimentary" && (
          <>
            <Field label={t("collab.ends")}>
              {(p) => <Select id={p.id} aria-label={t("collab.ends")} value={choice} onChange={setChoice} options={UNTIL_CHOICES.map((u) => ({ value: u, label: t(`collab.until.${u}`) }))} />}
            </Field>
            {choice === "date" && (
              <Field label={t("collab.lastDay")} hint={t("collab.lastDayHint")} error={touched || date ? dateError : undefined}>
                {(p) => (
                  <TextInput {...p} autoFocus inputMode="numeric" placeholder="2026-12-31" autoComplete="off" spellCheck={false} value={date} onChange={(e) => setDate(e.target.value)} />
                )}
              </Field>
            )}
            {choice !== "date" && choice !== "none" && "until" in until && until.until && (
              <p className="-mt-2 text-[11.5px] text-fg-3">{t("collab.endsOn", { date: shortDate(until.until) })}</p>
            )}
          </>
        )}
        <Field label={t("collab.note")} optional hint={t("collab.noteHint", { length: note.length, max: 200 })} error={note.length > 200 ? t("common:reason.tooLong", { max: 200 }) : undefined}>
          {(p) => <TextInput {...p} autoFocus={mode === "offered"} value={note} onChange={(e) => setNote(e.target.value)} placeholder={t("collab.notePlaceholder")} />}
        </Field>
        {error && <Notice>{error}</Notice>}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  );
}
