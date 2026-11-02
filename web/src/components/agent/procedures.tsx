// The procedure editor (contracts/agent.md "Procedures"): name, "Use this
// when", markdown instructions, channels and bots, and a one-time copy of a
// wiki page's text into the instructions.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot as BotIcon, ChevronDown, NotebookText } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Drawer } from "@/components/ui/dialog";
import { Field, Textarea, TextInput } from "@/components/ui/input";
import { Picker, type PickerItem } from "@/components/ui/picker";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Counter } from "@/components/agent/bits";
import { api } from "@/lib/api/endpoints";
import type { Procedure, ProcedureBody, ProcedureChannel } from "@/lib/api/types";
import { htmlToMarkdown, PROCEDURE_INSTRUCTIONS_MAX, PROCEDURE_NAME_MAX, PROCEDURE_WHEN_MAX, procedureProblems } from "@/lib/procedures";
import { q, qk, upsertInList } from "@/lib/queries";
import { cn } from "@/lib/cn";

export const CHANNELS: ProcedureChannel[] = ["chat", "email"];

/** "Chat, Email", "Automations only". */
export function channelsText(channels: ProcedureChannel[], t: (k: "procedures.channels.chat" | "procedures.channels.email" | "procedures.automationOnly") => string): string {
  return channels.length ? channels.map((c) => t(`procedures.channels.${c}`)).join(", ") : t("procedures.automationOnly");
}

/** Create a procedure, or edit `procedure`. */
export function ProcedureEditor({ procedure, open, onClose }: { procedure: Procedure | null; open: boolean; onClose: () => void }) {
  const { t } = useTranslation("agent");
  return (
    <Drawer open={open} onOpenChange={(o) => !o && onClose()} title={procedure ? procedure.name : t("procedures.editor.newTitle")} width={620}>
      {open && <EditorForm key={procedure?.id ?? "new"} procedure={procedure} onClose={onClose} />}
    </Drawer>
  );
}

function EditorForm({ procedure, onClose }: { procedure: Procedure | null; onClose: () => void }) {
  const { t } = useTranslation("agent");
  const qc = useQueryClient();
  const [name, setName] = useState(procedure?.name ?? "");
  const [when, setWhen] = useState(procedure?.when ?? "");
  const [instructions, setInstructions] = useState(procedure?.instructions ?? "");
  const [channels, setChannels] = useState<ProcedureChannel[]>(procedure?.channels ?? CHANNELS);
  const [botIds, setBotIds] = useState<string[]>(procedure?.bot_ids ?? []);
  const [enabled, setEnabled] = useState(procedure?.enabled ?? true);
  const [tried, setTried] = useState(false);
  const problems = procedureProblems({ name, when, instructions });
  const valid = Object.keys(problems).length === 0;

  const save = useMutation({
    mutationFn: () => {
      const body: ProcedureBody = { name: name.trim(), when: when.trim(), instructions: instructions.trim(), enabled, channels, bot_ids: botIds };
      return procedure ? api.ai.procedures.update(procedure.id, body) : api.ai.procedures.create(body);
    },
    onSuccess: (p) => {
      upsertInList(qc, qk.procedures, p);
      toast.success(procedure ? t("procedures.saved") : t("procedures.created"));
      onClose();
    },
    onError: (e) => toast.error(e, t("procedures.saveError")),
  });

  const toggleChannel = (c: ProcedureChannel, on: boolean) => setChannels((cur) => CHANNELS.filter((x) => (x === c ? on : cur.includes(x))));

  return (
    <form
      className="flex min-h-full flex-col"
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if (valid) save.mutate();
      }}
    >
      <div className="flex flex-1 flex-col gap-4 px-5 py-4">
        <Field label={t("procedures.editor.name")} error={tried && problems.name ? t("procedures.editor.nameInvalid") : null}>
          {(p) => (
            <TextInput {...p} data-autofocus maxLength={PROCEDURE_NAME_MAX + 20} placeholder={t("procedures.editor.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
          )}
        </Field>
        <Field label={t("procedures.editor.when")} hint={t("procedures.editor.whenHint")} error={tried && problems.when ? t("procedures.editor.whenInvalid") : null}>
          {(p) => (
            <div className="flex flex-col gap-1">
              <Textarea {...p} rows={2} placeholder={t("procedures.editor.whenPlaceholder")} value={when} onChange={(e) => setWhen(e.target.value)} />
              <Counter n={when.trim().length} max={PROCEDURE_WHEN_MAX} className="self-end" />
            </div>
          )}
        </Field>
        <Field label={t("procedures.editor.instructions")} hint={t("procedures.editor.instructionsHint")} error={tried && problems.instructions ? t("procedures.editor.instructionsInvalid") : null}>
          {(p) => (
            <div className="flex flex-col gap-1">
              <Textarea {...p} rows={12} className="mono text-[12px]" placeholder={t("procedures.editor.instructionsPlaceholder")} value={instructions} onChange={(e) => setInstructions(e.target.value)} />
              <div className="flex items-center justify-between gap-2">
                <WikiCopy onCopy={(md) => setInstructions((cur) => (cur.trim() ? `${cur.trim()}\n\n${md}` : md))} />
                <Counter n={instructions.trim().length} max={PROCEDURE_INSTRUCTIONS_MAX} />
              </div>
            </div>
          )}
        </Field>

        <section className="flex flex-col gap-2">
          <div>
            <h3 className="text-[12.5px] font-medium text-fg">{t("procedures.editor.channels")}</h3>
            <p className="text-[12px] text-fg-3">{t("procedures.editor.channelsHint")}</p>
          </div>
          <div className="flex flex-wrap gap-4">
            {CHANNELS.map((c) => (
              <label key={c} className="flex items-center gap-2 text-[12.5px] text-fg">
                <Switch checked={channels.includes(c)} onCheckedChange={(on) => toggleChannel(c, on)} aria-label={t(`procedures.channels.${c}`)} />
                {t(`procedures.channels.${c}`)}
              </label>
            ))}
          </div>
        </section>

        <section className="flex flex-col gap-2">
          <div>
            <h3 className="text-[12.5px] font-medium text-fg">{t("procedures.editor.bots")}</h3>
            <p className="text-[12px] text-fg-3">{t("procedures.editor.botsHint")}</p>
          </div>
          <BotsPicker values={botIds} onChange={setBotIds} />
        </section>

        <label className="flex items-center justify-between gap-3 rounded-lg border border-line bg-sunken/60 px-3.5 py-2.5">
          <span className="text-[12.5px] font-medium text-fg">{t("procedures.editor.enabled")}</span>
          <Switch checked={enabled} onCheckedChange={setEnabled} aria-label={t("procedures.editor.enabled")} />
        </label>
      </div>
      <div className="sticky bottom-0 flex justify-end gap-2 border-t border-line bg-surface px-5 py-3">
        <Button onClick={onClose}>{t("cancel")}</Button>
        <Button variant="primary" type="submit" loading={save.isPending}>
          {procedure ? t("save") : t("procedures.editor.create")}
        </Button>
      </div>
    </form>
  );
}

function BotsPicker({ values, onChange }: { values: string[]; onChange: (v: string[]) => void }) {
  const { t } = useTranslation("agent");
  const { data: bots } = useQuery(q.bots());
  const items: PickerItem[] = (bots ?? []).map((b) => ({ value: b.id, label: b.name }));
  const names = values.map((id) => bots?.find((b) => b.id === id)?.name).filter(Boolean);
  return (
    <Picker
      multi
      values={values}
      onChange={onChange}
      items={items}
      width={280}
      trigger={
        <button
          type="button"
          className="flex h-8 w-full max-w-[360px] items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-left text-[12.5px] text-fg transition-colors hover:bg-hover"
        >
          <BotIcon className="size-3.5 shrink-0 text-fg-3" />
          <span className={cn("min-w-0 flex-1 truncate", !names.length && "text-fg-3")}>{names.length ? names.join(", ") : t("procedures.everyBot")}</span>
          <ChevronDown className="size-3.5 shrink-0 text-fg-4" />
        </button>
      }
    />
  );
}

/** Picks a wiki page and copies its text, as markdown, once. */
function WikiCopy({ onCopy }: { onCopy: (markdown: string) => void }) {
  const { t } = useTranslation("agent");
  const [term, setTerm] = useState("");
  const [busy, setBusy] = useState(false);
  const { data: tree } = useQuery(q.wikiTree());
  const { data: hits } = useQuery(q.wikiSearch(term));
  const untitled = t("procedures.editor.untitled");
  const items: PickerItem[] = term.trim()
    ? (hits ?? []).map((h) => ({ value: h.id, label: h.title || untitled, hint: h.path.map((p) => p.title || untitled).join(" / ") || undefined }))
    : [...(tree ?? [])]
        .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
        .slice(0, 30)
        .map((n) => ({ value: n.id, label: n.title || untitled }));

  const copy = async (id: string) => {
    setBusy(true);
    try {
      const page = await api.wiki.get(id);
      const md = htmlToMarkdown(page.body_html);
      if (md) onCopy(md);
      toast.success(t("procedures.editor.copied", { title: page.title || untitled }), { description: t("procedures.editor.copyNote") });
    } catch (e) {
      toast.error(e, t("procedures.editor.copyError"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Picker
      value={null}
      onSelect={(id) => void copy(id)}
      items={items}
      width={320}
      placeholder={t("procedures.editor.copyWikiSearch")}
      emptyText={t("procedures.editor.copyWikiEmpty")}
      onQueryChange={setTerm}
      trigger={
        <Button type="button" size="xs" variant="ghost" icon={<NotebookText />} loading={busy}>
          {t("procedures.editor.copyWiki")}
        </Button>
      }
    />
  );
}
