// The built-in changes Ollie may make without asking (settings.ai.assistant_auto,
// contracts/ai.md "Approvals"). Each switch saves at once, optimistically, apart from
// the page's draft.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Row, Section } from "@/components/layout";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import { ASSISTANT_AUTO_TOOLS, type AIConfig, type AssistantAutoTool } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";

export function AutoRunSettings({ name, disabled }: { name: string; disabled: boolean }) {
  const { t } = useTranslation("ai");
  const qc = useQueryClient();
  const { data: config } = useQuery(q.aiConfig());
  const on = config?.features?.assistant_auto ?? [];

  const save = useMutation({
    mutationFn: (assistant_auto: AssistantAutoTool[]) => api.ai.updateSettings({ assistant_auto }),
    onMutate: async (assistant_auto) => {
      await qc.cancelQueries({ queryKey: qk.aiConfig });
      const prev = qc.getQueryData<AIConfig | null>(qk.aiConfig);
      qc.setQueryData<AIConfig | null>(qk.aiConfig, (old) => (old?.features ? { ...old, features: { ...old.features, assistant_auto } } : old));
      return { prev };
    },
    onError: (e, _v, c) => {
      if (c?.prev !== undefined) qc.setQueryData(qk.aiConfig, c.prev);
      toast.error(errorMessage(e, t("settings.save_failed")));
    },
    onSuccess: (features) =>
      qc.setQueryData<AIConfig | null>(qk.aiConfig, (old) => (old?.features ? { ...old, features: { ...old.features, assistant_auto: features.assistant_auto ?? [] } } : old)),
  });

  const toggle = (tool: AssistantAutoTool, v: boolean) => {
    // The list keeps the order of the switches.
    save.mutate(ASSISTANT_AUTO_TOOLS.filter((x) => (x === tool ? v : on.includes(x))));
  };

  return (
    <Section title={t("settings.auto_run", { name })} description={t("settings.auto_run_hint", { name })}>
      {ASSISTANT_AUTO_TOOLS.map((tool) => {
        const label = t(`settings.auto_run_tools.${tool}`);
        return (
          <Row key={tool} label={label} description={t(`settings.auto_run_tools_hint.${tool}`)}>
            <Switch checked={on.includes(tool)} disabled={disabled} onCheckedChange={(v) => toggle(tool, v)} aria-label={label} />
          </Row>
        );
      })}
      <p className="py-2.5 text-[12px] leading-[18px] text-fg-3">{t("settings.auto_run_always", { name })}</p>
    </Section>
  );
}
