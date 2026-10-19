// Settings > Email design (contracts/templates.md "Email design"): each brand's
// layout around every customer email, its satisfaction email, and the variable
// reference. Editors sit next to a live preview of the email as sent.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { BookOpen, Check, Copy, LayoutTemplate, RotateCcw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Section, SettingsPage } from "@/components/layout";
import { SaveBar } from "@/components/settings/SaveBar";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import { EmailPreview } from "@/components/templates/EmailPreview";
import { Problems, TemplateEditor } from "@/components/templates/TemplateEditor";
import { VariableList, VariablePicker } from "@/components/templates/VariablePicker";
import { tagFor } from "@/components/templates/catalog";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { copyText } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty";
import { TextInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Segmented, Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { Brand, EmailLayoutOptions, EmailTemplate, TemplateFilter, TemplateProblem } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";

const DOCS = "https://docs.opendesk.sh/guides/email-templates/";
const HEX = /^#[0-9a-fA-F]{6}$/;
const tag = (k: string) => ({ [k]: `{{ ${k} }}`, interpolation: { escapeValue: false } });

export default function EmailDesign() {
  const { t } = useTranslation("emailDesign");
  const admin = atLeast(useRole(), "admin");
  const { data: brands, isLoading, isError, refetch } = useQuery(q.brands());
  const [brandId, setBrandId] = useState<string | null>(null);
  const brand = brands?.find((b) => b.id === brandId) ?? brands?.find((b) => b.is_default) ?? brands?.[0];
  const [tab, setTab] = useState("layout");

  return (
    <SettingsPage
      wide
      title={t("title")}
      description={t("description")}
      actions={
        <>
          {brands && brands.length > 1 && brand && (
            <Select
              aria-label={t("brand")}
              size="sm"
              className="w-[200px]"
              value={brand.id}
              onChange={setBrandId}
              options={brands.map((b) => ({ value: b.id, label: b.name }))}
            />
          )}
          <Button size="sm" variant="ghost" icon={<BookOpen />} asChild>
            <a href={DOCS} target="_blank" rel="noreferrer">
              {t("docs")}
            </a>
          </Button>
        </>
      }
    >
      {!admin && <p className="-mb-4 text-[12.5px] text-fg-3">{t("readOnly")}</p>}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="layout">{t("tabs.layout")}</TabsTrigger>
          <TabsTrigger value="csat">{t("tabs.csat")}</TabsTrigger>
          <TabsTrigger value="variables">{t("tabs.variables")}</TabsTrigger>
        </TabsList>
        <div className="pt-6">
          {isError ? (
            <LoadError onRetry={() => void refetch()} />
          ) : isLoading || !brand ? (
            <Skeleton className="h-[480px] w-full rounded-lg" />
          ) : (
            <>
              <TabsContent value="layout">
                <BrandTemplates key={`layout-${brand.id}`} brand={brand} kind="layout" admin={admin} />
              </TabsContent>
              <TabsContent value="csat">
                <BrandTemplates key={`csat-${brand.id}`} brand={brand} kind="csat" admin={admin} />
              </TabsContent>
              <TabsContent value="variables">
                <VariablesReference />
              </TabsContent>
            </>
          )}
        </div>
      </Tabs>
    </SettingsPage>
  );
}

type Draft = { subject: string; body_html: string; options: EmailLayoutOptions };
const toDraft = (x: EmailTemplate): Draft => ({ subject: x.subject, body_html: x.body_html, options: { ...x.options } });

/** A load that failed: say so and offer to try again, rather than loading forever. */
function LoadError({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation("emailDesign");
  return (
    <EmptyState
      lit={3}
      title={t("loadError.title")}
      description={t("loadError.description")}
      action={<Button onClick={onRetry}>{t("loadError.retry")}</Button>}
    />
  );
}

/** Loads a brand's template and owns its draft, save and reset. */
function BrandTemplates({ brand, kind, admin }: { brand: Brand; kind: "layout" | "csat"; admin: boolean }) {
  const { t } = useTranslation("emailDesign");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const { data, isError, refetch } = useQuery(q.emailTemplates(brand.id));
  const saved = data ? data[kind] : undefined;
  const [draft, setDraft] = useState<Draft | null>(null);
  const [problems, setProblems] = useState<TemplateProblem[]>([]);
  const base = useRef<string>("");

  // A fresh copy from the server (first load, another admin's save) replaces the draft
  // only while it has no changes.
  useEffect(() => {
    if (!saved) return;
    const next = JSON.stringify(toDraft(saved));
    setDraft((cur) => (cur && JSON.stringify(cur) !== base.current ? cur : toDraft(saved)));
    base.current = next;
  }, [saved]);

  const dirty = !!draft && JSON.stringify(draft) !== base.current;
  const onProblems = useCallback((p: TemplateProblem[]) => setProblems(p), []);

  const save = useMutation({
    mutationFn: (d: Draft) =>
      api.emailTemplates.save(brand.id, kind, kind === "layout" ? { body_html: d.body_html, options: d.options } : { subject: d.subject, body_html: d.body_html }),
    onSuccess: (row) => {
      qc.setQueryData(qk.emailTemplates(brand.id), (old: typeof data) => (old ? { ...old, [kind]: row } : old));
      base.current = JSON.stringify(toDraft(row));
      setDraft(toDraft(row));
      toast.success(t("saved"));
    },
    onError: (e) => toast.error(e),
  });
  const reset = useMutation({
    mutationFn: () => api.emailTemplates.reset(brand.id, kind),
    onSuccess: async () => {
      base.current = "";
      setDraft(null);
      await qc.invalidateQueries({ queryKey: qk.emailTemplates(brand.id) });
      toast.success(t("resetDone"));
    },
    onError: (e) => toast.error(e),
  });

  const previewBody = useMemo(
    () =>
      draft
        ? kind === "layout"
          ? { kind, brand_id: brand.id, options: draft.options, body_html: draft.options.mode === "custom" ? draft.body_html : "" }
          : { kind, brand_id: brand.id, subject: draft.subject, body_html: draft.body_html }
        : null,
    [draft, kind, brand.id],
  );

  if (isError && !saved) return <LoadError onRetry={() => void refetch()} />;
  if (!draft || !saved || !previewBody) return <Skeleton className="h-[480px] w-full rounded-lg" />;
  const set = (p: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...p } : d));
  const setOpt = (p: Partial<EmailLayoutOptions>) => setDraft((d) => (d ? { ...d, options: { ...d.options, ...p } } : d));

  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.05fr)]">
      <div className="flex min-w-0 flex-col gap-6">
        <div className="flex items-center gap-2">
          <span className={cn("inline-flex h-[22px] items-center rounded-sm px-1.5 text-[11.5px] font-medium", saved.is_default ? "bg-hover text-fg-3" : "bg-tone-blue-soft text-tone-blue")}>
            {saved.is_default ? t("default") : t("customized")}
          </span>
          {admin && !saved.is_default && (
            <Button
              size="xs"
              variant="ghost"
              icon={<RotateCcw />}
              loading={reset.isPending}
              onClick={() =>
                confirm.show(kind === "layout" ? t("resetLayoutConfirm") : t("resetCsatConfirm"), () => reset.mutateAsync(), {
                  title: t("resetTitle"),
                  confirmLabel: t("reset"),
                })
              }
            >
              {t("reset")}
            </Button>
          )}
        </div>
        <fieldset disabled={!admin} className="contents">
          {kind === "layout" ? (
            <LayoutForm draft={draft} brand={brand} setOpt={setOpt} set={set} problems={problems} disabled={!admin} />
          ) : (
            <CsatForm draft={draft} set={set} problems={problems} disabled={!admin} />
          )}
        </fieldset>
      </div>
      <div className="min-w-0 lg:sticky lg:top-4 lg:self-start">
        <EmailPreview body={previewBody} onProblems={onProblems} height={620} />
      </div>
      {admin && (
        <div className="lg:col-span-2">
          <SaveBar dirty={dirty} saving={save.isPending} onSave={() => save.mutate(draft)} onDiscard={() => setDraft(toDraft(saved))} />
        </div>
      )}
      <UnsavedGuard when={dirty} />
    </div>
  );
}

function Field({ label, hint, children, end }: { label: ReactNode; hint?: ReactNode; children: ReactNode; end?: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-end justify-between gap-3">
        <div className="min-w-0">
          <div className="text-[12.5px] font-medium">{label}</div>
          {hint && <div className="text-[12px] leading-[17px] text-fg-3">{hint}</div>}
        </div>
        {end}
      </div>
      {children}
    </div>
  );
}

function LayoutForm({
  draft,
  brand,
  setOpt,
  set,
  problems,
  disabled,
}: {
  draft: Draft;
  brand: Brand;
  setOpt: (p: Partial<EmailLayoutOptions>) => void;
  set: (p: Partial<Draft>) => void;
  problems: TemplateProblem[];
  disabled: boolean;
}) {
  const { t } = useTranslation("emailDesign");
  const o = draft.options;
  const [accentText, setAccentText] = useState(o.accent);
  useEffect(() => setAccentText(o.accent), [o.accent]);
  const accentBad = accentText !== "" && !HEX.test(accentText);
  const brandColour = HEX.test(brand.color) ? brand.color : "#0A0A0A";

  return (
    <>
      <Field label={t("layout.mode")} hint={t("layout.modeHint")}>
        <Segmented
          value={o.mode}
          onChange={(mode) => setOpt({ mode })}
          options={[
            { value: "simple", label: t("layout.simple") },
            { value: "custom", label: t("layout.custom"), icon: <LayoutTemplate /> },
          ]}
          className="self-start"
        />
      </Field>

      {o.mode === "simple" ? (
        <>
          <Field label={t("layout.style")}>
            <div role="radiogroup" aria-label={t("layout.style")} className="grid grid-cols-2 gap-2">
              {(["plain", "branded"] as const).map((s) => (
                <button
                  key={s}
                  type="button"
                  role="radio"
                  aria-checked={o.style === s}
                  disabled={disabled}
                  onClick={() => setOpt({ style: s })}
                  className={cn(
                    "flex flex-col gap-2 rounded-lg border p-2.5 text-left outline-none transition-colors focus-visible:shadow-[0_0_0_3px_var(--focus)]",
                    o.style === s ? "border-fg-3 bg-surface shadow-xs" : "border-line hover:border-line-strong",
                  )}
                >
                  <StyleThumb style={s} accent={o.accent || brandColour} />
                  <span>
                    <span className="block text-[12.5px] font-medium">{t(`layout.${s}`)}</span>
                    <span className="block text-[11.5px] leading-4 text-fg-3">{t(`layout.${s}Hint`)}</span>
                  </span>
                </button>
              ))}
            </div>
          </Field>

          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <div className="text-[12.5px] font-medium">{t("layout.logo")}</div>
              <div className="text-[12px] text-fg-3">
                {brand.images.logo ? (
                  t("layout.logoHint")
                ) : (
                  <>
                    {t("layout.noLogo")}{" "}
                    <Link to="/settings/brands/$brandId" params={{ brandId: brand.id }} className="text-fg underline underline-offset-2">
                      {t("layout.addLogo")}
                    </Link>
                  </>
                )}
              </div>
            </div>
            <Switch aria-label={t("layout.logo")} checked={o.logo} onCheckedChange={(logo) => setOpt({ logo })} disabled={disabled} />
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label={t("layout.accent")} hint={t("layout.accentHint")}>
              <div className="flex items-center gap-2">
                <span className="size-7 shrink-0 rounded-md border border-line" style={{ background: HEX.test(accentText) ? accentText : brandColour }} aria-hidden />
                <TextInput
                  aria-label={t("layout.accent")}
                  className="mono w-[120px]"
                  value={accentText}
                  placeholder={brandColour}
                  invalid={accentBad}
                  disabled={disabled}
                  onChange={(e) => {
                    const v = e.target.value.trim();
                    setAccentText(v);
                    if (v === "" || HEX.test(v)) setOpt({ accent: v });
                  }}
                />
                {o.accent && (
                  <Button size="xs" variant="ghost" onClick={() => setOpt({ accent: "" })} disabled={disabled}>
                    {t("layout.useBrand")}
                  </Button>
                )}
              </div>
              {accentBad && <span className="text-[12px] text-tone-red">{t("layout.accentInvalid")}</span>}
            </Field>
            <Field label={t("layout.font")}>
              <Select
                aria-label={t("layout.font")}
                value={o.font}
                onChange={(font) => setOpt({ font })}
                disabled={disabled}
                options={(["system", "sans", "serif"] as const).map((f) => ({ value: f, label: t(`layout.fonts.${f}`) }))}
              />
            </Field>
          </div>

          <Field label={t("layout.footer")} hint={t("layout.footerHint")}>
            <TemplateEditor
              kind="signature"
              html
              value={o.footer_html}
              onChange={(footer_html) => setOpt({ footer_html })}
              ariaLabel={t("layout.footer")}
              placeholder={t("layout.footerPlaceholder")}
              minHeight={64}
              disabled={disabled}
            />
          </Field>
        </>
      ) : (
        <Field
          label={t("layout.customHtml")}
          hint={t("layout.customHint", { content: "{{ content }}", signature: "{{ signature }}", history: "{{ history }}", interpolation: { escapeValue: false } })}
        >
          <TemplateEditor kind="layout" htmlOnly value={draft.body_html} onChange={(body_html) => set({ body_html })} ariaLabel={t("layout.customHtml")} minHeight={320} disabled={disabled} />
        </Field>
      )}

      <Field label={t("layout.history")} hint={t("layout.historyHint")}>
        <Select
          aria-label={t("layout.history")}
          value={o.history}
          onChange={(history) => setOpt({ history })}
          disabled={disabled}
          options={[
            { value: "none", label: t("layout.historyNone") },
            { value: "last", label: t("layout.historyLast") },
            { value: "all", label: t("layout.historyAll") },
          ]}
        />
      </Field>
      {o.history !== "none" && (
        <Field
          label={t("layout.historyHeader")}
          hint={t("layout.historyHeaderHint", { author: "{{ message.author }}", date: "{{ message.date }}", interpolation: { escapeValue: false } })}
        >
          <TextInput
            aria-label={t("layout.historyHeader")}
            className="mono text-[12px]"
            value={o.history_header}
            placeholder="On {{ message.date }}, {{ message.author }} wrote:"
            maxLength={200}
            disabled={disabled}
            onChange={(e) => setOpt({ history_header: e.target.value })}
          />
        </Field>
      )}
      {problems.length > 0 && <Problems problems={problems} className="mt-0" />}
    </>
  );
}

/** A tiny drawing of each style. */
function StyleThumb({ style, accent }: { style: "plain" | "branded"; accent: string }) {
  const lines = (
    <>
      <span className="block h-1 w-3/4 rounded-full bg-fg-4/40" />
      <span className="block h-1 w-full rounded-full bg-fg-4/30" />
      <span className="block h-1 w-2/3 rounded-full bg-fg-4/30" />
    </>
  );
  if (style === "plain") return <span className="flex h-16 flex-col justify-center gap-1.5 rounded-md bg-white px-3 shadow-[inset_0_0_0_1px_var(--line)]">{lines}</span>;
  return (
    <span className="flex h-16 items-center justify-center rounded-md bg-[#F4F4F5] px-3 shadow-[inset_0_0_0_1px_var(--line)]">
      <span className="flex w-full flex-col gap-1.5 rounded-[5px] bg-white px-2 py-2 shadow-[0_0_0_1px_#E5E5E5]" style={{ borderTop: `2px solid ${accent}` }}>
        {lines}
      </span>
    </span>
  );
}

function CsatForm({ draft, set, problems, disabled }: { draft: Draft; set: (p: Partial<Draft>) => void; problems: TemplateProblem[]; disabled: boolean }) {
  const { t } = useTranslation("emailDesign");
  const { data: ws } = useQuery(q.workspace());
  const subject = useRef<HTMLInputElement>(null);
  const insertSubject = (text: string, select?: string) => {
    const el = subject.current;
    const start = el?.selectionStart ?? draft.subject.length;
    const end = el?.selectionEnd ?? start;
    set({ subject: draft.subject.slice(0, start) + text + draft.subject.slice(end) });
    const i = select ? text.indexOf(select) : -1;
    requestAnimationFrame(() => {
      el?.focus();
      if (select && i >= 0) el?.setSelectionRange(start + i, start + i + select.length);
      else el?.setSelectionRange(start + text.length, start + text.length);
    });
  };
  return (
    <>
      {ws && !ws.settings.csat_enabled && (
        <div className="rounded-md bg-tone-amber-soft px-3 py-2 text-[12.5px] text-tone-amber shadow-[inset_0_0_0_1px_var(--tone-amber-line)]">
          {t("csat.off")}{" "}
          <Link to="/settings/workspace" className="font-medium underline underline-offset-2">
            {t("csat.turnOn")}
          </Link>
        </div>
      )}
      <Field label={t("csat.subject")} hint={t("csat.subjectHint")} end={!disabled && <VariablePicker kind="subject" side="bottom" onPick={(k) => insertSubject(tagFor(k))} onInsert={insertSubject} />}>
        <TextInput ref={subject} aria-label={t("csat.subject")} value={draft.subject} maxLength={200} disabled={disabled} onChange={(e) => set({ subject: e.target.value })} />
      </Field>
      <Field label={t("csat.body")} hint={t("csat.bodyHint", tag("survey"))}>
        <TemplateEditor kind="csat" html value={draft.body_html} onChange={(body_html) => set({ body_html })} ariaLabel={t("csat.body")} minHeight={200} disabled={disabled} problems={problems} />
      </Field>
    </>
  );
}

const FILTER_GROUPS: { group: TemplateFilter["group"]; label: "text" | "lists" | "numbers" | "dates" }[] = [
  { group: "text", label: "text" },
  { group: "list", label: "lists" },
  { group: "number", label: "numbers" },
  { group: "date", label: "dates" },
];

function VariablesReference() {
  const { t } = useTranslation("emailDesign");
  const { data } = useQuery(q.templateVariables());
  const [copied, setCopied] = useState<string | null>(null);
  const copy = (code: string) => {
    void copyText(code);
    setCopied(code);
    setTimeout(() => setCopied((c) => (c === code ? null : c)), 1400);
  };
  const Code = ({ code }: { code: string }) => (
    <button
      type="button"
      onClick={() => copy(code)}
      title={t("variables.copy")}
      className="mono group mt-1 flex w-full items-start gap-2 rounded-md bg-sunken px-2 py-1.5 text-left text-[11.5px] leading-[17px] text-fg-2 transition-colors hover:bg-hover"
    >
      <span className="min-w-0 flex-1 whitespace-pre-wrap break-words">{code}</span>
      {copied === code ? <Check className="mt-0.5 size-3.5 shrink-0 text-tone-green" /> : <Copy className="mt-0.5 size-3.5 shrink-0 text-fg-4 opacity-0 group-hover:opacity-100" />}
    </button>
  );
  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <Section title={t("variables.title")} description={t("variables.description")} flush>
        <VariableList copy className="max-h-[760px]" />
      </Section>
      <div className="flex min-w-0 flex-col gap-6">
        <Section title={t("variables.logic")} description={t("variables.logicHint")}>
          <dl className="flex flex-col gap-3.5 py-3">
            {(data?.tags ?? []).map((tg) => (
              <div key={tg.name}>
                <dt className="flex items-baseline gap-2 text-[12.5px]">
                  <span className="mono font-medium">{tg.name}</span>
                  <span className="text-[12px] text-fg-3">{tg.description}</span>
                </dt>
                <dd>
                  <Code code={tg.syntax} />
                </dd>
              </div>
            ))}
          </dl>
        </Section>
        <Section title={t("variables.helpers")} description={t("variables.helpersHint")}>
          <div className="flex flex-col gap-4 py-3">
            {FILTER_GROUPS.map((g) => {
              const list = (data?.filters ?? []).filter((f) => f.group === g.group);
              if (!list.length) return null;
              return (
                <div key={g.group}>
                  <div className="label-caps pb-1.5">{t(`variables.groups.${g.label}`)}</div>
                  <dl className="flex flex-col gap-3">
                    {list.map((f) => (
                      <div key={f.name}>
                        <dt className="flex flex-wrap items-baseline gap-x-2 text-[12.5px]">
                          <span className="mono font-medium">
                            {f.name}
                            {f.args && <span className="font-normal text-fg-3">: {f.args}</span>}
                          </span>
                          <span className="text-[12px] text-fg-3">{f.description}</span>
                        </dt>
                        <dd>
                          <Code code={f.example} />
                        </dd>
                      </div>
                    ))}
                  </dl>
                </div>
              );
            })}
          </div>
        </Section>
      </div>
    </div>
  );
}
