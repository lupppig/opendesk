// A CSV import's first two steps: drop a file, then say which column holds what. The
// server reads the file once on upload and guesses the columns from their headers.

import { Download, FileSpreadsheet, UploadCloud, X } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import type { CsvField, CsvMapping, CsvUpload } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { bytes, number } from "@/lib/format";

/** The fields in the order people think of them; the first group is what most files have. */
export const CSV_FIELDS: CsvField[] = [
  "ticket_id",
  "subject",
  "body",
  "created_at",
  "from_email",
  "from_name",
  "author",
  "status",
  "priority",
  "tags",
  "assignee_email",
  "team",
  "company",
  "message_id",
  "in_reply_to",
  "references",
];

const MAX_BYTES = 50 * 1024 * 1024;

const template = [
  ["Ticket ID", "Subject", "Message", "Created at", "From email", "From name", "Author type", "Status", "Priority", "Tags", "Assignee email", "Team", "Company"],
  ["1001", "Can't sign in", "Hi, since this morning I can't sign in.", "2025-03-04 09:12", "mia@northwind.io", "Mia Chen", "customer", "solved", "high", "login", "", "Support", "Northwind"],
  ["1001", "Can't sign in", "Sorry about that! I've reset your session, could you try again?", "2025-03-04 09:40", "alex@yourcompany.com", "Alex", "agent", "", "", "", "alex@yourcompany.com", "", ""],
  ["1001", "Can't sign in", "Their proxy blocked the cookie.", "2025-03-04 09:41", "alex@yourcompany.com", "Alex", "note", "", "", "", "", "", ""],
  ["1002", "Refund for a double charge", "I was charged twice in March.", "2025-03-05 14:02", "jonas@fjord.no", "Jonas Berg", "customer", "closed", "normal", "billing, refund", "", "Billing", ""],
];

function downloadTemplate() {
  const esc = (v: string) => (/[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
  const blob = new Blob([template.map((r) => r.map(esc).join(",")).join("\r\n") + "\r\n"], { type: "text/csv" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = "opendesk-import-template.csv";
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

/** A drop area for one CSV file, which also opens the file picker. */
export function CsvDrop({ onFile, uploading, error }: { onFile: (f: File) => void; uploading: File | null; error: string | null }) {
  const { t } = useTranslation("imports");
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);
  const [local, setLocal] = useState<string | null>(null);
  const take = (files: FileList | null) => {
    const f = files?.[0];
    if (!f) return;
    if (f.size > MAX_BYTES) return setLocal(t("csv.tooLarge"));
    if (!/\.(csv|tsv|txt)$/i.test(f.name)) return setLocal(t("csv.wrongType"));
    setLocal(null);
    onFile(f);
  };
  const shown = local ?? error;
  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        disabled={!!uploading}
        onClick={() => input.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          take(e.dataTransfer.files);
        }}
        className={cn(
          "flex min-h-[188px] flex-col items-center justify-center gap-2 rounded-lg border border-dashed px-6 py-8 text-center outline-none transition-colors duration-150 focus-visible:shadow-[0_0_0_3px_var(--focus)]",
          over ? "border-fg-3 bg-hover" : "border-line-strong bg-surface hover:border-fg-4 hover:bg-canvas",
          shown && "border-[color-mix(in_srgb,var(--danger)_40%,var(--line))]",
        )}
      >
        {uploading ? (
          <>
            <Spinner />
            <span className="text-[13px] font-medium text-fg">{t("csv.reading", { name: uploading.name })}</span>
            <span className="text-[12px] text-fg-3">{bytes(uploading.size)}</span>
          </>
        ) : (
          <>
            <span className="flex size-10 items-center justify-center rounded-lg bg-hover text-fg-3">
              <UploadCloud className="size-5" aria-hidden />
            </span>
            <span className="text-[13px] font-medium text-fg">{over ? t("csv.dropNow") : t("csv.drop")}</span>
            <span className="max-w-[420px] text-[12px] leading-[18px] text-fg-3">{t("csv.dropHint")}</span>
          </>
        )}
      </button>
      <input ref={input} type="file" accept=".csv,.tsv,.txt,text/csv" className="hidden" onChange={(e) => (take(e.target.files), (e.target.value = ""))} />
      {shown && (
        <p role="alert" className="animate-rise-in text-[12px] text-danger-fg">
          {shown}
        </p>
      )}
      <div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-fg-3">
        <span>{t("csv.formats")}</span>
        <Button size="xs" variant="ghost" icon={<Download />} onClick={downloadTemplate}>
          {t("csv.template")}
        </Button>
      </div>
    </div>
  );
}

const NONE = "__none";

/** Each field and the column that holds it, with a few of that column's values. */
export function CsvColumns({ upload, mapping, onChange, onReplace }: { upload: CsvUpload; mapping: CsvMapping; onChange: (m: CsvMapping) => void; onReplace: () => void }) {
  const { t } = useTranslation("imports");
  const options = [{ value: NONE, label: t("csv.notInFile") }, ...upload.columns.map((c) => ({ value: c.name, label: c.name }))];
  const samplesOf = (name?: string) => upload.columns.find((c) => c.name === name)?.samples ?? [];
  const missing = !mapping.body && !mapping.subject;
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3 rounded-lg border border-line bg-surface px-4 py-3 shadow-xs">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-hover text-fg-3">
          <FileSpreadsheet className="size-4" aria-hidden />
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate text-[13px] font-medium text-fg">{upload.filename}</div>
          <div className="text-[12px] text-fg-3">{t("csv.summary", { rows: number(upload.rows), columns: upload.columns.length })}</div>
        </div>
        <Button size="xs" variant="ghost" icon={<X />} onClick={onReplace}>
          {t("csv.replace")}
        </Button>
      </div>
      <div className="overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
        {CSV_FIELDS.map((f) => {
          const col = mapping[f];
          const samples = samplesOf(col);
          return (
            <div key={f} className="flex flex-col gap-2 border-b border-line-soft px-4 py-2.5 last:border-0 md:flex-row md:items-center">
              <div className="min-w-0 md:w-[220px] md:shrink-0">
                <div className="flex items-center gap-1.5 text-[12.5px] font-medium text-fg">
                  {t(`csv.fields.${f}.label`)}
                  {(f === "body" || f === "subject") && missing && <span className="size-1.5 rounded-full bg-danger" aria-hidden />}
                </div>
                <div className="text-[11.5px] leading-4 text-fg-3">{t(`csv.fields.${f}.hint`)}</div>
              </div>
              <div className="md:w-[200px] md:shrink-0">
                <Select
                  size="sm"
                  aria-label={t(`csv.fields.${f}.label`)}
                  value={col ?? NONE}
                  onChange={(v) => {
                    const next = { ...mapping };
                    if (v === NONE) delete next[f];
                    else next[f] = v;
                    onChange(next);
                  }}
                  options={options}
                />
              </div>
              <div className="mono min-w-0 flex-1 truncate text-[11px] text-fg-4">{samples.length ? samples.join("  ·  ") : col ? t("csv.emptyColumn") : ""}</div>
            </div>
          );
        })}
      </div>
      {missing && (
        <p role="alert" className="text-[12px] text-danger-fg">
          {t("csv.needBody")}
        </p>
      )}
      <p className="text-[12px] leading-[18px] text-fg-3">{t("csv.grouping")}</p>
    </div>
  );
}
