// PostHog, only when a key is configured. Autocapture and route pageviews;
// session replay masks every input and anything marked data-ph-mask or
// .ph-no-capture (ticket and message bodies). Custom properties never carry
// PII: ids and enums only.

import type { PostHog } from "posthog-js/dist/module.full.no-external";
import { config } from "./config";

let ph: PostHog | null = null;
let waiting = false;

/** Query parameters that carry secrets: one-time links, OAuth codes and states, signed URLs. */
const SECRET_PARAMS = ["token", "code", "state", "link_token", "sig", "exp", "invite_token", "mfa_token"];

export function hasSecretParams(search: string): boolean {
  const q = new URLSearchParams(search);
  return SECRET_PARAMS.some((k) => q.has(k));
}

/** The URL with no query or fragment, so no token in it reaches analytics. */
export function stripUrl(v: string): string {
  return v.split(/[?#]/)[0];
}

/** Drops the query from every URL-like property, including person properties ($set, $set_once). */
export function sanitize(props: Record<string, unknown>): Record<string, unknown> {
  for (const [k, v] of Object.entries(props)) {
    if (typeof v === "string" && /^https?:\/\//.test(v)) props[k] = stripUrl(v);
    else if ((k === "$set" || k === "$set_once") && v && typeof v === "object") sanitize(v as Record<string, unknown>);
  }
  return props;
}

export async function initAnalytics() {
  if (!config.posthogKey || ph) return;
  // A page opened from a one-time link (reset, invite, verify, OAuth) starts analytics
  // only once the URL no longer carries the secret: the first pageview after it.
  if (hasSecretParams(location.search)) {
    waiting = true;
    return;
  }
  waiting = false;
  // The build with every extension inside: nothing is loaded from PostHog's servers at
  // run time, so all script the app runs is its own (no third-party script without SRI).
  const { default: posthog } = await import("posthog-js/dist/module.full.no-external");
  posthog.init(config.posthogKey, {
    api_host: config.posthogHost,
    autocapture: true,
    capture_pageview: false,
    capture_pageleave: true,
    capture_exceptions: true,
    person_profiles: "identified_only",
    mask_personal_data_properties: true,
    disable_external_dependency_loading: true,
    mask_all_text: false,
    session_recording: {
      maskAllInputs: true,
      maskTextSelector: "[data-ph-mask], .ph-no-capture",
      blockSelector: ".ph-no-capture img",
    },
    // URLs can carry tokens (reset, invite, verify, OAuth codes): drop every query.
    sanitize_properties: (props) => sanitize(props) as typeof props,
  });
  ph = posthog;
}

/** A path as analytics may keep it: a survey link of an earlier version carried its token in the path. */
export function pagePath(path: string): string {
  return path.startsWith("/csat/") ? "/csat" : path;
}

export function pageview(path: string) {
  path = pagePath(path);
  if (waiting && !hasSecretParams(location.search)) void initAnalytics().then(() => pageview(path));
  ph?.capture("$pageview", { $current_url: location.origin + path });
}

export function identify(userId: string, workspaceId: string | null) {
  if (!ph) return;
  ph.identify(userId);
  if (workspaceId) ph.group("workspace", workspaceId);
}

export function resetAnalytics() {
  ph?.reset();
}

/** Product events. Properties must be ids, counts or enums, never names, emails or content. */
export function track(event: string, props?: Record<string, string | number | boolean | null>) {
  ph?.capture(event, props);
}
