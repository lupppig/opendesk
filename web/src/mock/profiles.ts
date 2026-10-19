// Members' profiles (contracts/templates.md): what signatures and the agent template
// variables read, per workspace and member.

import type { MemberProfile } from "@/lib/api/types";
import type { WsData } from "./db";

const profiles = new WeakMap<WsData, Record<string, MemberProfile>>();

export function profileOf(ws: WsData, uid: string): MemberProfile {
  let p = profiles.get(ws);
  if (!p) profiles.set(ws, (p = {}));
  return (p[uid] ??= { title: "", phone: "", pronouns: "", signature_html: "" });
}
