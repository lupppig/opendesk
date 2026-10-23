// What customers see of a member in this workspace (contracts/templates.md
// "Signatures"): job title, phone, pronouns and the personal signature. They
// fill the agent.* template variables. Used by Profile (your own) and Members
// (an admin editing anyone's).

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Row } from "@/components/layout";
import { SignatureEditor } from "@/components/templates/SignatureEditor";
import { Field, TextInput } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { Member, MemberProfile, TemplatePreviewBody, TemplateProblem } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { qk } from "@/lib/queries";

export const emptyProfile: MemberProfile = { title: "", phone: "", pronouns: "", signature_html: "" };

/** A member's profile, with every field present (older servers leave it out). */
export function profileOf(m: Member | undefined): MemberProfile {
  return { ...emptyProfile, ...(m?.profile ?? {}) };
}

/** The fields that differ from `base`, trimmed, for the PATCH. */
export function profileChanges(draft: MemberProfile, base: MemberProfile): Partial<MemberProfile> {
  const out: Partial<MemberProfile> = {};
  for (const k of ["title", "phone", "pronouns"] as const) if (draft[k].trim() !== base[k]) out[k] = draft[k].trim();
  if (draft.signature_html !== base.signature_html) out.signature_html = draft.signature_html;
  return out;
}

export const profileDirty = (draft: MemberProfile, base: MemberProfile) => Object.keys(profileChanges(draft, base)).length > 0;

/**
 * Template problems in a refused save (`422` with `errors[{ message, value }]`).
 * With `field`, errors located on another field are left out.
 */
export function templateProblems(e: unknown, field?: string): TemplateProblem[] {
  if (!isApiError(e) || e.status !== 422) return [];
  return (e.errors ?? [])
    .filter((x) => x.message && (!field || !x.location || x.location.includes(field)))
    .map((x) => ({ message: x.message!, token: typeof x.value === "string" ? x.value : "" }));
}

/** Saves a member's profile ("me" for your own), updating the members list at once. */
export function useSaveProfile(userId: string, self: boolean) {
  const { t } = useTranslation("settings");
  const qc = useQueryClient();
  const [problems, setProblems] = useState<TemplateProblem[]>([]);
  const mutation = useMutation({
    mutationFn: (changes: Partial<MemberProfile>) => api.members.updateProfile(self ? "me" : userId, changes),
    onMutate: async (changes) => {
      setProblems([]);
      await qc.cancelQueries({ queryKey: qk.members });
      const prev = qc.getQueryData<Member[]>(qk.members);
      if (prev) qc.setQueryData<Member[]>(qk.members, prev.map((m) => (m.user.id === userId ? { ...m, profile: { ...profileOf(m), ...changes } } : m)));
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.members, ctx.prev);
      setProblems(templateProblems(e, "signature"));
      toast.error(e);
    },
    onSuccess: (m) => {
      qc.setQueryData<Member[]>(qk.members, (old) => old?.map((x) => (x.user.id === m.user.id ? m : x)));
      toast.success(t("memberProfile.saved"));
    },
  });
  return { ...mutation, problems, clearProblems: () => setProblems([]) };
}

/**
 * The fields. `rows` lays them out as settings rows (inside a Section),
 * `stacked` as a form (drawers). `problems` are a refused save's.
 */
export function MemberProfileFields({
  value,
  onChange,
  problems = [],
  disabled,
  variant = "rows",
  other,
}: {
  value: MemberProfile;
  onChange: (p: MemberProfile) => void;
  problems?: TemplateProblem[];
  disabled?: boolean;
  variant?: "rows" | "stacked";
  /** Editing someone else's: the preview fills the sender with you. */
  other?: boolean;
}) {
  const { t } = useTranslation("settings");
  const [previewProblems, setPreviewProblems] = useState<TemplateProblem[]>([]);
  const onProblems = useCallback((p: TemplateProblem[]) => setPreviewProblems(p), []);
  const signature = value.signature_html;
  const body = useMemo<TemplatePreviewBody>(() => ({ kind: "personal_signature", body_html: signature }), [signature]);
  const shown = previewProblems.length ? previewProblems : problems;
  const set = <K extends keyof MemberProfile>(k: K, v: MemberProfile[K]) => onChange({ ...value, [k]: v });

  const inputs = [
    { key: "title" as const, label: t("memberProfile.jobTitle"), placeholder: t("memberProfile.jobTitlePlaceholder"), max: 80, tag: "{{ agent.title }}" },
    { key: "phone" as const, label: t("memberProfile.phone"), placeholder: t("memberProfile.phonePlaceholder"), max: 40, tag: "{{ agent.phone }}" },
    { key: "pronouns" as const, label: t("memberProfile.pronouns"), placeholder: t("memberProfile.pronounsPlaceholder"), max: 40, tag: "{{ agent.pronouns }}" },
  ];

  const signatureBlock = (
    <SignatureEditor
      kind="personal_signature"
      value={signature}
      onChange={(v) => set("signature_html", v)}
      ariaLabel={t("memberProfile.signature")}
      placeholder={t("memberProfile.signaturePlaceholder")}
      disabled={disabled}
      problems={shown}
      previewTitle={t("memberProfile.preview")}
      previewHeight={360}
      previewBody={body}
      onProblems={onProblems}
      footer={other && <p className="text-[11.5px] leading-4 text-fg-4">{t("memberProfile.previewOther")}</p>}
    />
  );

  if (variant === "stacked")
    return (
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          {inputs.map((f) => (
            <Field key={f.key} label={f.label}>
              {(p) => (
                <TextInput {...p} value={value[f.key]} maxLength={f.max} placeholder={f.placeholder} disabled={disabled} onChange={(e) => set(f.key, e.target.value)} />
              )}
            </Field>
          ))}
        </div>
        <div className="flex flex-col gap-1.5">
          <span className="text-[12.5px] font-medium">{t("memberProfile.signature")}</span>
          <span className="text-[12px] leading-[18px] text-fg-3">{t("memberProfile.signatureHint", { tag: "{{ agent.signature }}" })}</span>
          <div className="mt-1">{signatureBlock}</div>
        </div>
      </div>
    );

  return (
    <>
      {inputs.map((f) => (
        <Row key={f.key} label={f.label} htmlFor={`member-profile-${f.key}`} description={t("memberProfile.fills", { tag: f.tag })}>
          <TextInput
            id={`member-profile-${f.key}`}
            className="w-[280px]"
            value={value[f.key]}
            maxLength={f.max}
            placeholder={f.placeholder}
            disabled={disabled}
            onChange={(e) => set(f.key, e.target.value)}
          />
        </Row>
      ))}
      <Row stack label={t("memberProfile.signature")} description={t("memberProfile.signatureHint", { tag: "{{ agent.signature }}" })} className={cn(disabled && "opacity-80")}>
        {signatureBlock}
      </Row>
    </>
  );
}
