// The workspace's Collab card: the hidden collab plan's access (offered or
// complimentary), whether Collab is on, and the actions that change it
// (contracts/admin.md "Collab access").

import { ChevronDown, Gift, Mail, XCircle } from "lucide-react";
import { useState } from "react";
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
import { number, plural, shortDate } from "@/lib/format";

function AccessLabel({ access, until }: { access: CollabAccess; until: string | null }) {
  if (access === "offered") return <Badge tone="blue">Offered</Badge>;
  if (access === "complimentary")
    return (
      <span className="flex flex-wrap items-center gap-2">
        <Badge tone="indigo">Complimentary</Badge>
        <span className="text-[12px] text-fg-3">{until ? `until ${shortDate(until)}` : "no end date"}</span>
      </span>
    );
  return <span className="text-fg-3">None</span>;
}

export function CollabSection({ w, billing, onSaved }: { w: AdminWorkspace; billing: boolean; onSaved: (w: AdminWorkspace) => void }) {
  const c = w.collab;
  const confirm = useConfirm();
  const [dialog, setDialog] = useState<null | "offered" | "complimentary">(null);

  const remove = () =>
    confirm.show(
      c.access === "complimentary" ? (
        <>
          <span className="font-medium text-fg">{w.name}</span> loses Collab now unless it pays for the plan. Connected mail accounts stop syncing and are kept.
        </>
      ) : (
        <>
          <span className="font-medium text-fg">{w.name}</span> can no longer subscribe to the Collab plan. A subscription it already has keeps running.
        </>
      ),
      async () => {
        onSaved(await api.workspaces.setCollab(w.id, { access: "none", note: c.note }));
        toast.success("Collab access removed");
      },
      { title: "Remove Collab access?", confirmLabel: "Remove access", danger: true },
    );

  return (
    <Section
      title="Collab"
      actions={
        <Menu>
          <MenuTrigger asChild>
            <Button variant="secondary" size="xs" iconRight={<ChevronDown />}>
              Change
            </Button>
          </MenuTrigger>
          <MenuContent className="w-[250px]">
            {c.access !== "offered" && (
              <ActionMenuItem perm="workspaces.limits" icon={<Mail />} onSelect={() => setDialog("offered")}>
                Offer the Collab plan
              </ActionMenuItem>
            )}
            <ActionMenuItem perm="workspaces.limits" icon={<Gift />} onSelect={() => setDialog("complimentary")}>
              {c.access === "complimentary" ? "Change free access" : "Give Collab for free"}
            </ActionMenuItem>
            {c.access !== "none" && (
              <>
                <MenuSeparator />
                <ActionMenuItem perm="workspaces.limits" icon={<XCircle />} danger onSelect={remove}>
                  Remove access
                </ActionMenuItem>
              </>
            )}
          </MenuContent>
        </Menu>
      }
    >
      <KV
        items={[
          ["Access", <AccessLabel access={c.access} until={c.until} />],
          ["Collab", <FlagPill on={c.enabled} yes="On" no="Off" />],
          ["Mail accounts", <span className="mono tabular-nums">{number(c.accounts)}</span>],
          ...(c.note ? ([["Note", <span className="whitespace-pre-wrap text-fg-2">{c.note}</span>]] as [React.ReactNode, React.ReactNode][]) : []),
        ]}
      />
      <p className="mt-3 text-[11.5px] leading-4 text-fg-3">
        {billing
          ? "The Collab plan is hidden. Offer it so this workspace can subscribe at $38 per user per month, or give it for free."
          : "Billing is off on this instance, so every workspace has Collab unless the instance turns it off. Access is kept but has no effect."}
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
      toast.success(mode === "offered" ? "Collab plan offered" : until.until ? `Collab is free until ${shortDate(until.until)}` : "Collab is free");
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
      title={mode === "offered" ? "Offer the Collab plan" : editing ? "Change free Collab access" : "Give Collab for free"}
      description={
        mode === "offered"
          ? `${w.name} sees the Collab plan on its billing page and can subscribe at $38 per user per month, or switch its Pro subscription to it.`
          : `${w.name} gets the Collab plan without paying: unlimited mailboxes and members, $20 of AI per paid seat each month (${plural(Math.max(seats, 1), "seat")} now), and Collab.`
      }
      size="sm"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} disabled={!ok && touched} onClick={() => void save()}>
            {mode === "offered" ? "Offer the plan" : editing ? "Save" : "Give for free"}
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
        {!billing && <Notice kind="info">Billing is off on this instance, so this is kept but has no effect.</Notice>}
        {mode === "complimentary" && (
          <>
            <Field label="Ends">
              {(p) => <Select id={p.id} aria-label="Ends" value={choice} onChange={setChoice} options={UNTIL_CHOICES} />}
            </Field>
            {choice === "date" && (
              <Field label="Last day" hint="Access ends at the end of this day, your time." error={touched || date ? dateError : undefined}>
                {(p) => (
                  <TextInput {...p} autoFocus inputMode="numeric" placeholder="2026-12-31" autoComplete="off" spellCheck={false} value={date} onChange={(e) => setDate(e.target.value)} />
                )}
              </Field>
            )}
            {choice !== "date" && choice !== "none" && "until" in until && until.until && (
              <p className="-mt-2 text-[11.5px] text-fg-3">Ends {shortDate(until.until)}.</p>
            )}
          </>
        )}
        <Field label="Note" optional hint={`${note.length} / 200. Only admins see it.`} error={note.length > 200 ? "Keep it under 200 characters." : undefined}>
          {(p) => <TextInput {...p} autoFocus={mode === "offered"} value={note} onChange={(e) => setNote(e.target.value)} placeholder="Why, and who asked" />}
        </Field>
        {error && <Notice>{error}</Notice>}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  );
}
