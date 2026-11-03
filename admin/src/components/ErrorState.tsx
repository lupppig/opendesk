import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty";
import { errorMessage, isApiError } from "@/lib/api/client";

/** A failed load: what went wrong, the request id to quote, and a retry. */
export function ErrorState({ error, onRetry, compact, action }: { error: unknown; onRetry?: () => void; compact?: boolean; action?: ReactNode }) {
  const { t } = useTranslation();
  const notFound = isApiError(error, "not_found");
  const requestId = isApiError(error) ? error.requestId : null;
  return (
    <EmptyState
      compact={compact}
      art={notFound ? "notfound" : "error"}
      title={notFound ? t("errors.notFound") : t("errors.didntLoad")}
      description={notFound ? t("errors.notFoundHint") : errorMessage(error)}
      action={
        <>
          {onRetry && !notFound && (
            <Button variant="secondary" onClick={onRetry}>
              {t("actions.tryAgain")}
            </Button>
          )}
          {action}
        </>
      }
    >
      {requestId && (
        <div className="inline-flex items-center gap-1.5 rounded-md bg-sunken py-0.5 pl-2 pr-0.5 text-[11.5px] text-fg-3 shadow-[inset_0_0_0_1px_var(--line-soft)]">
          {t("errors.requestId")} <span className="mono text-fg-2">{requestId}</span>
          <CopyButton value={requestId} label={t("errors.copyRequestId")} />
        </div>
      )}
    </EmptyState>
  );
}
