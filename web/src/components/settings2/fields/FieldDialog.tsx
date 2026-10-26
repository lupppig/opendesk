// Create or edit a custom field: name and key, a type picker (create only,
// since type and key never change), description, options with colours for
// select fields, and "required to solve" for ticket fields.

import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Label as FieldLabel, Textarea, TextInput } from "@/components/ui/input";
import { Popover } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { errorMessage } from "@/lib/api/client";
import { cn } from "@/lib/cn";
import { TAG_TONES, tone as tones, type Tone } from "@/lib/tones";
import { ColorSwatchPicker } from "../ColorSwatchPicker";
import { IconTile } from "../bits";
import { rowSpring } from "../motion";
import { FIELD_TYPES, hasOptions, keyRe, slugKey, slugOption, typeIcon, typeTone, type CustomField, type FieldEntity, type FieldInput, type FieldOption, type FieldType } from "./model";
import { inSentence } from "@/i18n";

type DraftOption = FieldOption & { uid: number };

export function FieldDialog({
  field,
  entity,
  existing,
  onClose,
  onSave,
}: {
  field: CustomField | null;
  entity: FieldEntity;
  existing: CustomField[];
  onClose: () => void;
  onSave: (v: FieldInput) => Promise<unknown>;
}) {
  const { t } = useTranslation("settings");
  const reduce = useReducedMotion();
  const uid = useRef(0);
  const [name, setName] = useState(field?.name ?? "");
  const [key, setKey] = useState(field?.key ?? "");
  const [keyTouched, setKeyTouched] = useState(!!field);
  const [type, setType] = useState<FieldType>(field?.type ?? "text");
  const [description, setDescription] = useState(field?.description ?? "");
  const [required, setRequired] = useState(field?.required_on_solve ?? false);
  const [options, setOptions] = useState<DraftOption[]>(() => (field?.options ?? []).map((o) => ({ ...o, uid: uid.current++ })));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const effectiveKey = keyTouched ? key : slugKey(name);
  const keyTaken = !field && existing.some((f) => f.entity === entity && f.key === effectiveKey);
  const keyError = !field && effectiveKey && !keyRe.test(effectiveKey) ? t("fields.dialog.keyRule") : keyTaken ? t("fields.dialog.keyTaken") : null;

  const addOption = () => {
    const color = TAG_TONES[(options.length + 1) % TAG_TONES.length];
    setOptions((cur) => [...cur, { id: "", label: "", color, uid: uid.current++ }]);
  };
  const patchOption = (u: number, p: Partial<FieldOption>) => setOptions((cur) => cur.map((o) => (o.uid === u ? { ...o, ...p } : o)));
  const moveOption = (i: number, d: -1 | 1) =>
    setOptions((cur) => {
      const j = i + d;
      if (j < 0 || j >= cur.length) return cur;
      const next = cur.slice();
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });

  const submit = async () => {
    if (!name.trim()) return setError(t("fields.dialog.nameRequired"));
    if (keyError) return setError(keyError);
    const opts = options.filter((o) => o.label.trim());
    if (hasOptions(type) && !opts.length) return setError(t("fields.dialog.optionsRequired"));
    const ids = new Set<string>();
    const clean: FieldOption[] = opts.map((o) => {
      let id = o.id || slugOption(o.label);
      while (ids.has(id)) id = `${id}-2`;
      ids.add(id);
      return { id, label: o.label.trim(), color: o.color };
    });
    setBusy(true);
    setError(null);
    try {
      await onSave({
        entity,
        key: field ? undefined : effectiveKey,
        name: name.trim(),
        type,
        options: hasOptions(type) ? clean : undefined,
        required_on_solve: entity === "ticket" ? required : undefined,
        description: description.trim(),
      });
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open
      size="lg"
      onOpenChange={(o) => !o && onClose()}
      title={field ? t("fields.dialog.editTitle") : t("fields.dialog.newTitle", { entity: inSentence(t(`fields.entity.${entity}`)) })}
      description={field ? t("fields.dialog.editDescription") : t("fields.dialog.newDescription")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant="primary" loading={busy} onClick={() => void submit()}>
            {field ? t("fields.dialog.save") : t("fields.dialog.create")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-5 pb-1"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <div className="grid gap-4 sm:grid-cols-[1fr_200px]">
          <Field label={t("fields.dialog.name")} error={error}>
            {({ id, invalid }) => (
              <TextInput
                id={id}
                autoFocus
                invalid={invalid}
                value={name}
                maxLength={60}
                placeholder={t("fields.dialog.namePlaceholder")}
                onChange={(e) => {
                  setName(e.target.value);
                  setError(null);
                }}
              />
            )}
          </Field>
          <Field label={t("fields.dialog.key")} hint={field ? t("fields.dialog.keyFixed") : t("fields.dialog.keyHint")}>
            {({ id }) => (
              <TextInput
                id={id}
                className="mono"
                value={effectiveKey}
                disabled={!!field}
                invalid={!!keyError}
                maxLength={40}
                placeholder="plan_tier"
                onChange={(e) => {
                  setKeyTouched(true);
                  setKey(e.target.value.toLowerCase());
                }}
              />
            )}
          </Field>
        </div>

        <div className="flex flex-col gap-2">
          <FieldLabel>{t("fields.dialog.type")}</FieldLabel>
          <div role="radiogroup" aria-label={t("fields.dialog.type")} className="grid grid-cols-2 gap-1.5 sm:grid-cols-5">
            {FIELD_TYPES.filter((ft) => !field || ft === type).map((ft) => {
              const on = ft === type;
              const locked = !!field && !on;
              return (
                <button
                  key={ft}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  disabled={locked}
                  onClick={() => {
                    setType(ft);
                    if (hasOptions(ft) && !options.length) setOptions([{ id: "", label: "", color: "blue", uid: uid.current++ }]);
                  }}
                  className={cn(
                    "flex items-center gap-2 rounded-md border px-2 py-1.5 text-left text-[12px] transition-[border-color,background-color,opacity] active:scale-[0.985]",
                    on ? "border-fg-3 bg-sunken text-fg shadow-[0_0_0_1px_var(--fg-3)]" : "border-line text-fg-2 hover:border-line-strong",
                    locked && "opacity-35",
                  )}
                >
                  <IconTile tone={typeTone[ft]} size="sm">
                    {typeIcon[ft]}
                  </IconTile>
                  <span className="truncate">{t(`fields.type.${ft}`)}</span>
                </button>
              );
            })}
          </div>
          {field && <p className="-mt-0.5 text-xs text-fg-3">{t("fields.dialog.typeFixed")}</p>}
        </div>

        {hasOptions(type) && (
          <div className="flex flex-col gap-2">
            <FieldLabel>{t("fields.dialog.options")}</FieldLabel>
            <div className="overflow-hidden rounded-md border border-line">
              <AnimatePresence initial={false}>
                {options.map((o, i) => (
                  <motion.div
                    key={o.uid}
                    layout={reduce ? false : "position"}
                    initial={reduce ? false : { opacity: 0, y: -4 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, transition: { duration: 0.12 } }}
                    transition={rowSpring}
                    className="group flex h-10 items-center gap-2 border-b border-line-soft px-2 last:border-0"
                  >
                    <OptionColour value={o.color} onChange={(c) => patchOption(o.uid, { color: c })} label={o.label || t("fields.dialog.optionPlaceholder")} />
                    <input
                      value={o.label}
                      maxLength={60}
                      aria-label={t("fields.dialog.optionLabel", { n: i + 1 })}
                      placeholder={t("fields.dialog.optionPlaceholder")}
                      onChange={(e) => patchOption(o.uid, { label: e.target.value })}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          addOption();
                        }
                      }}
                      autoFocus={!o.label && i === options.length - 1 && i > 0}
                      className="h-7 min-w-0 flex-1 bg-transparent px-1 text-[13px] outline-none placeholder:text-fg-4"
                    />
                    <span className="flex opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
                      <IconButton label={t("fields.dialog.moveUp")} disabled={i === 0} onClick={() => moveOption(i, -1)}>
                        <ArrowUp />
                      </IconButton>
                      <IconButton label={t("fields.dialog.moveDown")} disabled={i === options.length - 1} onClick={() => moveOption(i, 1)}>
                        <ArrowDown />
                      </IconButton>
                      <IconButton label={t("fields.dialog.removeOption")} onClick={() => setOptions((cur) => cur.filter((x) => x.uid !== o.uid))}>
                        <X />
                      </IconButton>
                    </span>
                  </motion.div>
                ))}
              </AnimatePresence>
              <button
                type="button"
                onClick={addOption}
                className={cn("flex h-9 w-full items-center gap-2 px-3 text-[12.5px] text-fg-3 transition-colors hover:bg-sunken hover:text-fg", options.length && "border-t border-line-soft")}
              >
                <Plus className="size-3.5" />
                {t("fields.dialog.addOption")}
              </button>
            </div>
            {field && <p className="text-xs text-fg-3">{t("fields.dialog.removeHint")}</p>}
          </div>
        )}

        <Field label={t("fields.dialog.description")} optional hint={t("fields.dialog.descriptionHint")}>
          {({ id }) => <Textarea id={id} rows={2} maxLength={300} value={description} onChange={(e) => setDescription(e.target.value)} />}
        </Field>

        {entity === "ticket" && (
          <label className="flex cursor-pointer items-start justify-between gap-4 rounded-md border border-line px-3 py-2.5">
            <span>
              <span className="block text-[12.5px] font-medium">{t("fields.dialog.required")}</span>
              <span className="mt-0.5 block text-xs text-fg-3">{t("fields.dialog.requiredHint")}</span>
            </span>
            <Switch checked={required} onCheckedChange={setRequired} aria-label={t("fields.dialog.required")} />
          </label>
        )}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

function OptionColour({ value, onChange, label }: { value: Tone; onChange: (t: Tone) => void; label: string }) {
  const { t } = useTranslation("settings");
  const [open, setOpen] = useState(false);
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      className="p-2.5"
      trigger={
        <button
          type="button"
          aria-label={t("fields.dialog.optionColour", { name: label })}
          className="flex size-7 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-hover"
        >
          <span className={cn("size-3 rounded-xs", tones[value].solid)} />
        </button>
      }
    >
      <ColorSwatchPicker
        value={value}
        onChange={(c) => {
          onChange(c);
          setOpen(false);
        }}
        size="sm"
        className="w-[124px]"
      />
    </Popover>
  );
}

function IconButton({ label, children, onClick, disabled }: { label: string; children: React.ReactNode; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className="flex size-6 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-30 [&_svg]:size-3.5"
    >
      {children}
    </button>
  );
}
