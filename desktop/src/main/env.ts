// Build-wide constants and the development switches. Development behaviour
// (http to local addresses, the URL override, devtools, screenshots) is on only
// in an unpackaged build.

import { app } from "electron";

export const APP_ID = "sh.opendesk.desktop";
export const PROTOCOL = "opendesk";
export const CLOUD_ORIGIN = "https://app.opendesk.sh";
/** Addresses people type for Cloud. */
export const CLOUD_HOSTS = new Set(["opendesk.sh", "www.opendesk.sh", "app.opendesk.sh"]);
export const UPDATE_INTERVAL_MS = 6 * 60 * 60 * 1000;
/** Each desktop release's notes, by version. */
export const RELEASE_NOTES_URL = (version: string) => `https://github.com/useopendesk/opendesk/releases/tag/desktop-v${encodeURIComponent(version)}`;
export const OAUTH_TIMEOUT_MS = 10 * 60 * 1000;

export const isDev = !app.isPackaged;

/** `--name=value` from the command line, development builds only. */
export function devArg(name: string): string | undefined {
  if (!isDev) return undefined;
  const prefix = `--${name}=`;
  const hit = process.argv.find((a) => a.startsWith(prefix));
  return hit ? hit.slice(prefix.length) : undefined;
}

/**
 * `OPENDESK_DESKTOP_SANDBOX=<dir>` (development builds): a throwaway run for automated testing.
 * Its settings live in <dir>, it registers no opendesk:// handler, shows no OS notification or
 * badge, takes no Dock icon or focus, and its windows are invisible to the person at the machine
 * (fully transparent, click-through) while still painting for the recorder.
 */
export const sandboxDir = isDev ? process.env.OPENDESK_DESKTOP_SANDBOX || undefined : undefined;

/** `OPENDESK_DESKTOP_URL`: open this instance directly, skipping the server picker (development builds). */
export function devInstanceUrl(): string | undefined {
  if (!isDev) return undefined;
  return process.env.OPENDESK_DESKTOP_URL || devArg("url");
}
