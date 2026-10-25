// Templates in the reply composer (contracts/templates.md): what a picked
// variable writes.

import { tagFor } from "@/components/templates/catalog";

/**
 * What picking a variable writes into a reply: this ticket's value as plain text,
 * so the agent reads real words. The tag itself when there is no value yet (the
 * server fills it when the reply is created), when the value is HTML (it is
 * filled as HTML on send), or when the value reads like a tag (the reply is
 * filled once more on send, and a value is never read again as a template).
 */
export function variableText(key: string, value: string | undefined, html = false): string {
  if (!value?.trim() || html || /\{\{|\{%/.test(value)) return tagFor(key);
  return value;
}
