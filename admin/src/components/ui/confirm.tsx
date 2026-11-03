import { AlertDialog as A } from "radix-ui";
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { Button } from "./button";
import { TextInput } from "./input";
import { errorMessage } from "@/lib/api/client";
import { useTranslation, Trans } from "react-i18next";

type ConfirmOptions = {
  title?: string;
  confirmLabel?: string;
  danger?: boolean;
  /** Require typing this exact text (e.g. the workspace name) to enable the button. */
  typeToConfirm?: string;
};

type Pending = ConfirmOptions & { text: ReactNode; onSubmit: () => unknown | Promise<unknown> };

type ConfirmApi = { show: (text: ReactNode, onSubmit: () => unknown | Promise<unknown>, opts?: ConfirmOptions) => void };

const Ctx = createContext<ConfirmApi | null>(null);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [typed, setTyped] = useState("");

  const show = useCallback<ConfirmApi["show"]>((text, onSubmit, opts) => {
    setError(null);
    setTyped("");
    setPending({ text, onSubmit, ...opts });
  }, []);
  const api = useMemo(() => ({ show }), [show]);

  const run = async () => {
    if (!pending) return;
    setBusy(true);
    setError(null);
    try {
      await pending.onSubmit();
      setPending(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const blocked = !!pending?.typeToConfirm && typed !== pending.typeToConfirm;

  return (
    <Ctx.Provider value={api}>
      {children}
      <A.Root open={!!pending} onOpenChange={(o) => !o && !busy && setPending(null)}>
        <A.Portal>
          <A.Overlay className="fixed inset-0 z-[70] bg-scrim backdrop-blur-[1.5px] data-[state=open]:animate-fade-in" />
          <A.Content className="fixed left-1/2 top-[22vh] z-[70] w-[calc(100vw-32px)] max-w-[420px] -translate-x-1/2 overflow-hidden rounded-xl border border-line bg-raised shadow-float outline-none data-[state=open]:animate-dialog-in">
            <div className="px-5 pb-4 pt-5"><A.Title className="text-[15px] font-semibold tracking-[-0.015em]">{pending?.title ?? t("ui.areYouSure")}</A.Title>
            <A.Description className="mt-1.5 text-[13px] leading-5 text-fg-2">{pending?.text}</A.Description>
            {pending?.typeToConfirm && (
              <div className="mt-4 flex flex-col gap-1.5">
                <label className="text-xs text-fg-3">
                  <Trans t={t} i18nKey="ui.typeToConfirm" values={{ text: pending.typeToConfirm }} components={{ v: <span className="mono font-medium text-fg" /> }} />
                </label>
                <TextInput autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={t("ui.confirmText")} />
              </div>
            )}
            {error && <p className="mt-3 text-xs text-danger-fg">{error}</p>}
            </div>
            <div className="flex justify-end gap-2 border-t border-line bg-sunken px-5 py-3">
              <A.Cancel asChild>
                <Button variant="ghost" disabled={busy}>
                  {t("actions.cancel")}
                </Button>
              </A.Cancel>
              <Button
                variant={pending?.danger ? "danger-solid" : "primary"}
                loading={busy}
                disabled={blocked}
                onClick={run}
                autoFocus={!pending?.typeToConfirm}
              >
                {pending?.confirmLabel ?? t("actions.confirm")}
              </Button>
            </div>
          </A.Content>
        </A.Portal>
      </A.Root>
    </Ctx.Provider>
  );
}

export function useConfirm(): ConfirmApi {
  const c = useContext(Ctx);
  if (!c) throw new Error("useConfirm outside ConfirmProvider");
  return c;
}
