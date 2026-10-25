// Integrations (contracts/integrations.md): the provider mark, the connect dialog
// (the service's own sign-in, a pasted token, or any MCP server by URL) and the
// per-tool switches.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { continueInBrowser, InBrowserNote } from "@/components/desktop/InBrowser";
import { isDesktop } from "@/lib/desktop";
import { ArrowUpRight } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AppMark } from "@/components/appmark";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { Segmented } from "@/components/ui/tabs";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { Integration, IntegrationProvider } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { qk, upsertInList } from "@/lib/queries";
import { tone, type Tone } from "@/lib/tones";

const markTone: Record<string, Tone> = {
  linear: "indigo",
  github: "gray",
  atlassian: "blue",
  sentry: "purple",
  notion: "gray",
  asana: "rose",
  monday: "pink",
  slack: "violet",
  intercom: "blue",
  close: "green",
  stripe: "indigo",
  paypal: "blue",
  square: "gray",
  box: "blue",
  zapier: "orange",
};

/** A product's logo; a custom server's own icon; else its initial in its tone. */
export function ProviderMark({
  provider,
  name,
  icon,
  size = "md",
  className,
}: {
  provider: string;
  name: string;
  icon?: string | null;
  size?: "sm" | "md" | "lg";
  className?: string;
}) {
  return <AppMark app={provider} name={name} icon={icon} tint={markTone[provider] ?? "teal"} size={size} className={className} />;
}

function validServer(v: string): string | null {
  try {
    const u = new URL(v.trim());
    const local = u.hostname === "localhost" || u.hostname === "[::1]" || /^127\.\d+\.\d+\.\d+$/.test(u.hostname);
    if (u.protocol !== "https:" && !(u.protocol === "http:" && local)) return "Use an https:// URL.";
    return null;
  } catch {
    return "Enter the server's full URL, like https://mcp.example.com/mcp.";
  }
}

type Auth = "oauth" | "token" | "none";

/**
 * Connects a catalog product (`provider`) or any MCP server (`provider` null). The
 * service's sign-in leaves for its page and comes back to settings; a token or an
 * open server connects here.
 */
export function ConnectDialog({ provider, onClose, onConnected }: { provider: IntegrationProvider | null; onClose: () => void; onConnected: (x: Integration) => void }) {
  const { t } = useTranslation("settingsDev");
  const { t: tShell } = useTranslation("shell");
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const [name, setName] = useState("");
  const [auth, setAuth] = useState<Auth>(provider?.auth ?? "oauth");
  const [token, setToken] = useState("");
  const [urlError, setUrlError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const custom = !provider;
  const label = provider?.name ?? t("integrations.custom.name");

  const connect = useMutation({
    mutationFn: () =>
      api.integrations.create(
        custom
          ? { provider: "custom", url: url.trim(), auth, name: name.trim() || undefined, token: auth === "token" ? token.trim() : undefined }
          : { provider: provider.key, token: auth === "token" ? token.trim() : undefined },
      ),
    onSuccess: (r) => {
      upsertInList(qc, qk.integrations, r.integration, { prepend: true });
      if (r.authorize_url) {
        window.location.assign(r.authorize_url);
        return;
      }
      onConnected(r.integration);
    },
    onError: (e) => setError(errorMessage(e)),
  });

  const submit = () => {
    setError(null);
    // The desktop app connects OAuth services from the browser (contracts/native.md).
    if (isDesktop && auth === "oauth") {
      continueInBrowser("/settings/integrations");
      onClose();
      return;
    }
    if (custom) {
      const ue = validServer(url);
      setUrlError(ue);
      if (ue) return;
    }
    if (auth === "token" && !token.trim()) return setError(t("integrations.connect.tokenRequired"));
    connect.mutate();
  };

  const leaving = connect.isPending && auth === "oauth";
  return (
    <Dialog
      open
      size="md"
      onOpenChange={(o) => !o && onClose()}
      title={custom ? t("integrations.custom.title") : t("integrations.connect.title", { name: label })}
      description={custom ? t("integrations.custom.description") : provider.description}
      footer={
        <>
          <span className="mr-auto text-xs text-danger-fg">{error}</span>
          <Button variant="ghost" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant="primary" loading={connect.isPending} onClick={submit}>
            {auth === "oauth"
              ? isDesktop
                ? tShell("desktop.continueInBrowser")
                : t("integrations.connect.signIn", { name: custom ? t("integrations.custom.server") : label })
              : t("integrations.connect.connect")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4 pb-1"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        {custom && (
          <>
            <Field label={t("integrations.custom.url")} error={urlError} hint={t("integrations.custom.urlHint")}>
              {(p) => (
                <TextInput
                  {...p}
                  autoFocus
                  className="mono"
                  value={url}
                  placeholder="https://mcp.example.com/mcp"
                  onChange={(e) => {
                    setUrl(e.target.value);
                    setUrlError(null);
                  }}
                  onBlur={() => url && setUrlError(validServer(url))}
                />
              )}
            </Field>
            <Field label={t("integrations.custom.nameLabel")} optional>
              {(p) => <TextInput {...p} value={name} maxLength={64} placeholder={t("integrations.custom.namePlaceholder")} onChange={(e) => setName(e.target.value)} />}
            </Field>
            <div className="flex flex-col gap-1.5">
              <span className="text-[12.5px] font-medium text-fg-2">{t("integrations.custom.auth")}</span>
              <Segmented<Auth>
                value={auth}
                onChange={setAuth}
                className="self-start"
                options={[
                  { value: "oauth", label: t("integrations.auth.oauth") },
                  { value: "token", label: t("integrations.auth.token") },
                  { value: "none", label: t("integrations.auth.none") },
                ]}
              />
            </div>
          </>
        )}
        {auth === "token" && (
          <Field
            label={t("integrations.connect.token")}
            hint={
              provider?.token_help ? (
                <>
                  {provider.token_help}{" "}
                  {provider.token_url && (
                    <a href={provider.token_url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
                      {t("integrations.connect.createToken")}
                      <ArrowUpRight className="size-3" aria-hidden />
                    </a>
                  )}
                </>
              ) : (
                t("integrations.connect.tokenHint")
              )
            }
          >
            {(p) => (
              <TextInput {...p} autoFocus={!custom} type="password" autoComplete="off" spellCheck={false} className="mono" value={token} onChange={(e) => setToken(e.target.value)} />
            )}
          </Field>
        )}
        {auth === "oauth" && isDesktop && <InBrowserNote name={custom ? t("integrations.custom.server") : label} />}
        {auth === "oauth" && !isDesktop && (
          <p className="rounded-lg border border-line bg-sunken px-3 py-2.5 text-[12.5px] leading-[18px] text-fg-2">
            {leaving ? t("integrations.connect.leaving", { name: label }) : t("integrations.connect.oauthHint", { name: custom ? t("integrations.custom.server") : label })}
          </p>
        )}
        <p className="text-[12px] leading-[18px] text-fg-3">{t("integrations.connect.approval")}</p>
      </form>
    </Dialog>
  );
}

/**
 * Each tool with its switch. Lookups run on their own unless an admin sets them to ask
 * first; everything else asks first unless an admin lets it run without asking.
 */
export function ToolList({
  x,
  disabled,
  onToggle,
  onAsk,
  onAuto,
}: {
  x: Integration;
  disabled?: boolean;
  onToggle: (name: string, on: boolean) => void;
  /** Sets a tool its server marks read-only to wait for approval. */
  onAsk: (name: string, ask: boolean) => void;
  /** Lets a write run without the user's approval. */
  onAuto: (name: string, on: boolean) => void;
}) {
  const { t } = useTranslation("settingsDev");
  const reduce = useReducedMotion();
  const [all, setAll] = useState(false);
  // Sorted by the server's own hint, so a row stays put when it is set to ask first.
  const tools = [...x.tools].sort((a, b) => Number(b.read_only_hint) - Number(a.read_only_hint) || a.title.localeCompare(b.title));
  const shown = all ? tools : tools.slice(0, 8);
  if (!tools.length) return <p className="px-4 py-3 text-[12.5px] text-fg-3">{t("integrations.tools.none")}</p>;
  return (
    <div className="flex flex-col">
      <AnimatePresence initial={false}>
        {shown.map((tl) => (
          <motion.div
            key={tl.name}
            layout={reduce ? false : "position"}
            initial={reduce ? false : { opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            className="flex items-center gap-3 border-t border-line-soft py-2 pl-[60px] pr-4 first:border-t-0"
          >
            <div className={cn("min-w-0 flex-1", !tl.enabled && "opacity-55")}>
              <div className="flex items-center gap-2">
                <span className="truncate text-[12.5px] font-medium text-fg">{tl.title}</span>
                <span
                  className={cn(
                    "inline-flex h-[18px] shrink-0 items-center rounded-sm px-1.5 text-[10.5px] font-medium",
                    tl.read_only ? tone.gray.chip : tl.auto_run ? tone.violet.chip : tone.amber.chip,
                  )}
                >
                  {tl.read_only ? t("integrations.tools.lookup") : tl.auto_run ? t("integrations.tools.auto") : t("integrations.tools.asks")}
                </span>
              </div>
              {tl.description && <p className="mt-0.5 line-clamp-1 text-[11.5px] text-fg-3">{tl.description}</p>}
            </div>
            {tl.read_only_hint ? (
              <label className={cn("flex shrink-0 items-center gap-1.5 text-[11.5px] text-fg-3", !disabled && tl.enabled && "cursor-pointer hover:text-fg-2", !tl.enabled && "opacity-55")}>
                {t("integrations.tools.askFirst")}
                <Switch
                  checked={tl.ask_first}
                  disabled={disabled || !tl.enabled}
                  onCheckedChange={(ask) => onAsk(tl.name, ask)}
                  aria-label={t("integrations.tools.askFirstFor", { name: tl.title })}
                />
              </label>
            ) : (
              <label className={cn("flex shrink-0 items-center gap-1.5 text-[11.5px] text-fg-3", !disabled && tl.enabled && "cursor-pointer hover:text-fg-2", !tl.enabled && "opacity-55")}>
                {t("integrations.tools.autoRun")}
                <Switch
                  checked={tl.auto_run}
                  disabled={disabled || !tl.enabled}
                  onCheckedChange={(on) => onAuto(tl.name, on)}
                  aria-label={t("integrations.tools.autoRunFor", { name: tl.title })}
                />
              </label>
            )}
            <Switch checked={tl.enabled} disabled={disabled} onCheckedChange={(on) => onToggle(tl.name, on)} aria-label={t("integrations.tools.toggle", { name: tl.title })} />
          </motion.div>
        ))}
      </AnimatePresence>
      {tools.length > 8 && (
        <button type="button" onClick={() => setAll((v) => !v)} className="border-t border-line-soft py-2 pl-[60px] text-left text-[12px] text-fg-3 transition-colors hover:text-fg">
          {all ? t("integrations.tools.fewer") : t("integrations.tools.more", { count: tools.length - 8 })}
        </button>
      )}
    </div>
  );
}
