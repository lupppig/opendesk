import { Link, useRouter, type ErrorComponentProps } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty";
import { ErrorState } from "@/components/ErrorState";

/** A quiet bar while a route's code loads; it waits a beat so fast loads never flash. */
export function RoutePending() {
  const { t } = useTranslation();
  return (
    <div
      className="flex h-full min-h-[200px] items-start justify-center pt-24 opacity-0 animate-[fade-in_200ms_ease-out_150ms_forwards] motion-reduce:animate-none motion-reduce:opacity-100"
      role="status"
      aria-label={t("loading")}
    >
      <div className="h-0.5 w-24 overflow-hidden rounded-full bg-hover">
        <div className="h-full w-1/3 animate-[pending-bar_1s_ease-in-out_infinite] rounded-full bg-fg-4 motion-reduce:animate-none" />
      </div>
    </div>
  );
}

// After a release the old chunks are gone and the browser keeps a failed import,
// so only a full reload recovers. Reload once on its own.
const CHUNK_ERROR = /dynamically imported module|Importing a module script failed|error loading dynamically|Unable to preload CSS/i;
const RELOADED_KEY = "od-admin-chunk-reload";

function reloadOnce(): boolean {
  try {
    const last = Number(sessionStorage.getItem(RELOADED_KEY) ?? 0);
    if (Date.now() - last < 15_000) return false;
    sessionStorage.setItem(RELOADED_KEY, String(Date.now()));
  } catch {
    return false;
  }
  window.location.reload();
  return true;
}

export function RouteError({ error, reset }: ErrorComponentProps) {
  const { t } = useTranslation("shell");
  const router = useRouter();
  const chunk = error instanceof Error && CHUNK_ERROR.test(error.message);
  useEffect(() => {
    if (chunk) reloadOnce();
  }, [chunk]);
  return (
    <div className="flex h-full min-h-[60vh] items-center justify-center p-8">
      <ErrorState
        error={chunk ? new Error(t("newVersion")) : error}
        onRetry={() => {
          if (chunk) return window.location.reload();
          reset();
          void router.invalidate();
        }}
        action={
          <Button variant="ghost" asChild>
            <Link to="/">{t("nav.overview")}</Link>
          </Button>
        }
      />
    </div>
  );
}

export function NotFound() {
  const { t } = useTranslation("shell");
  return (
    <div className="flex min-h-[60vh] flex-1 items-center justify-center p-8">
      <EmptyState
        art="notfound"
        title={t("notFound.title")}
        description={t("notFound.description")}
        action={
          <Button variant="secondary" asChild>
            <Link to="/">{t("notFound.action")}</Link>
          </Button>
        }
      />
    </div>
  );
}
