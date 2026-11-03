import { QueryClientProvider } from "@tanstack/react-query";
import { Outlet, useMatches } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmProvider } from "@/components/ui/confirm";
import { Toaster } from "@/components/ui/toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import { queryClient } from "@/lib/queryClient";

export const TITLE_SUFFIX = "opendesk admin";

export function pageTitle(title: string | null | undefined) {
  return title ? `${title} · ${TITLE_SUFFIX}` : TITLE_SUFFIX;
}

function useRouteTitle() {
  const { t } = useTranslation("shell");
  const matches = useMatches();
  const key = [...matches].reverse().find((m) => m.staticData?.title)?.staticData.title;
  const title = key ? t(`titles.${key}`) : null;
  useEffect(() => {
    document.title = pageTitle(title);
  }, [title]);
}

export function RootLayout() {
  useRouteTitle();
  return (
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={400} skipDelayDuration={200}>
        <ConfirmProvider>
          <Outlet />
          <Toaster />
        </ConfirmProvider>
      </TooltipProvider>
    </QueryClientProvider>
  );
}

/** A page-specific tab title (a workspace's name, a user's email). */
export function useDocumentTitle(title: string | null | undefined) {
  useEffect(() => {
    if (!title) return;
    const prev = document.title;
    document.title = pageTitle(title);
    return () => {
      document.title = prev;
    };
  }, [title]);
}
