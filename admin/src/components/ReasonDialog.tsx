import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Textarea } from "@/components/ui/input";
import { errorMessage } from "@/lib/api/client";
import { Notice } from "./layout";

const MAX = 500;

/** Suspensions ask why (1..500 characters); the reason is shown on the record. */
export function ReasonDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  description?: ReactNode;
  confirmLabel: string;
  onConfirm: (reason: string) => Promise<unknown>;
}) {
  const { t } = useTranslation();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const text = reason.trim();
  const valid = text.length >= 1 && text.length <= MAX;

  const close = (o: boolean) => {
    if (busy) return;
    if (!o) {
      setReason("");
      setError(null);
    }
    onOpenChange(o);
  };

  const submit = async () => {
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      await onConfirm(text);
      setBusy(false);
      setReason("");
      onOpenChange(false);
    } catch (e) {
      setBusy(false);
      setError(errorMessage(e));
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={close}
      title={title}
      description={description}
      size="sm"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => close(false)}>
            {t("actions.cancel")}
          </Button>
          <Button variant="danger-solid" loading={busy} disabled={!valid} onClick={() => void submit()}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <Field label={t("reason.label")} hint={`${reason.length} / ${MAX}`} error={reason.length > MAX ? t("reason.tooLong", { max: MAX }) : undefined}>
          {(p) => (
            <Textarea
              {...p}
              autoFocus
              rows={4}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void submit();
              }}
              placeholder={t("reason.placeholder")}
            />
          )}
        </Field>
        {error && <Notice>{error}</Notice>}
      </div>
    </Dialog>
  );
}
