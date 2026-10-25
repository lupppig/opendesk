// The products an import reads (contracts/imports.md "Credentials"): what each asks for,
// where its people find it, and its logo.

import { FileSpreadsheet } from "lucide-react";
import type { ImportSource, ImportStatus } from "@/lib/api/types";
import { AppMark } from "@/components/appmark";
import type { ItemState } from "@/components/settings2/bits";
import type { Tone } from "@/lib/tones";

export const SOURCES: ImportSource[] = ["zendesk", "helpscout", "freshdesk", "intercom", "front", "csv"];

export const sourceName: Record<ImportSource, string> = {
  zendesk: "Zendesk",
  helpscout: "Help Scout",
  freshdesk: "Freshdesk",
  intercom: "Intercom",
  front: "Front",
  csv: "CSV file",
};

export type CredentialField = {
  key: string;
  label: string;
  /** Where to find the value, in the source's own words. */
  hint: string;
  secret?: boolean;
  optional?: boolean;
  /** Shown after the input, for an account address: ".zendesk.com". */
  suffix?: string;
  placeholder?: string;
  /** The source's help page that shows the value. */
  help?: string;
  /** A choice instead of text: value and label. */
  choices?: [string, string][];
};

export const credentialFields: Record<Exclude<ImportSource, "csv">, CredentialField[]> = {
  zendesk: [
    { key: "subdomain", label: "Zendesk address", hint: "The part before .zendesk.com in your Zendesk links.", suffix: ".zendesk.com", placeholder: "acme" },
    { key: "email", label: "Admin email", hint: "The email you sign in to Zendesk with. It must be an admin.", placeholder: "you@company.com" },
    {
      key: "token",
      label: "API token",
      hint: "In Admin Center, open Apps and integrations, then Zendesk API. Turn on token access and add a token.",
      secret: true,
      help: "https://support.zendesk.com/hc/en-us/articles/4408889192858",
    },
  ],
  helpscout: [
    {
      key: "app_id",
      label: "App ID",
      hint: "In Help Scout, open your profile, then My apps, and create an app. Any redirect URL works.",
      help: "https://developer.helpscout.com/mailbox-api/overview/authentication/#client-credentials-flow",
    },
    { key: "app_secret", label: "App secret", hint: "Shown next to the App ID.", secret: true },
    {
      key: "docs_key",
      label: "Docs API key",
      hint: "To bring your Docs articles too. In your profile, open Authentication, then API keys.",
      secret: true,
      optional: true,
      help: "https://developer.helpscout.com/docs-api/",
    },
  ],
  freshdesk: [
    { key: "domain", label: "Freshdesk address", hint: "The part before .freshdesk.com in your Freshdesk links.", suffix: ".freshdesk.com", placeholder: "acme" },
    {
      key: "api_key",
      label: "API key",
      hint: "Click your picture in Freshdesk, open Profile settings and choose View API key. Use an admin's key.",
      secret: true,
      help: "https://support.freshdesk.com/support/solutions/articles/215517",
    },
  ],
  intercom: [
    {
      key: "region",
      label: "Where your workspace is hosted",
      hint: "Most workspaces are in the US. Your Intercom address says if it is in Europe or Australia.",
      choices: [
        ["us", "United States"],
        ["eu", "Europe"],
        ["au", "Australia"],
      ],
    },
    {
      key: "token",
      label: "Access token",
      hint: "In the Developer Hub, open your app, then Configure and Authentication. A private app in your workspace is enough.",
      secret: true,
      help: "https://developers.intercom.com/docs/build-an-integration/learn-more/authentication",
    },
  ],
  front: [
    {
      key: "token",
      label: "API token",
      hint: "An admin opens Settings, then Developers and API Tokens, and creates a token that can read shared resources in all shared workspaces.",
      secret: true,
      help: "https://dev.frontapp.com/docs/create-and-revoke-api-tokens",
    },
  ],
};

export const defaultCredentials = (src: ImportSource): Record<string, string> => (src === "intercom" ? { region: "us" } : {});

const markTone: Record<ImportSource, Tone> = { zendesk: "green", helpscout: "blue", freshdesk: "teal", intercom: "indigo", front: "purple", csv: "gray" };

/** A source's logo, or a sheet for a CSV file. */
export function SourceMark({ source, size = "md", className }: { source: ImportSource; size?: "sm" | "md" | "lg"; className?: string }) {
  return (
    <AppMark
      app={source === "csv" ? null : source}
      name={sourceName[source]}
      tint={markTone[source]}
      glyph={source === "csv" ? <FileSpreadsheet /> : undefined}
      size={size}
      className={className}
    />
  );
}

export const statusState: Record<ImportStatus, ItemState> = {
  running: "active",
  paused: "paused",
  completed: "active",
  failed: "failing",
  canceled: "disabled",
  undoing: "pending",
  undone: "disabled",
};

/** Whether an import holds the workspace's one import slot. */
export const isActive = (s: ImportStatus) => s === "running" || s === "paused" || s === "undoing";

export const DOCS = "https://docs.opendesk.sh/guides/importing/";

/** Where an imported ticket came from (its custom.import), as "Zendesk #1234" with a link back. */
export function importedFrom(custom: Record<string, unknown> | undefined): { label: string; url: string | null } | null {
  const x = custom?.import as { source?: string; ref?: string; url?: string } | undefined;
  if (!x || typeof x !== "object" || !x.source) return null;
  const name = SOURCES.includes(x.source as ImportSource) ? sourceName[x.source as ImportSource] : x.source;
  const label = [name, typeof x.ref === "string" ? x.ref : ""].filter(Boolean).join(" ");
  const url = typeof x.url === "string" && x.url.startsWith("https://") ? x.url : null;
  return { label, url };
}
