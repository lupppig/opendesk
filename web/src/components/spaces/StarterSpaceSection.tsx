// Settings › General: the workspace's starter Space (contracts/spaces.md "The starter Space").
// Admins publish, edit and remove it with the Space editor; everyone in the workspace can copy
// it from the Space menu, and someone who saved no Spaces gets it in place of the generated one.

import { Pencil, Plus, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Row, Section } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { atLeast, useRole, useSession } from "@/lib/session";
import { useRemoveStarter, useStarterSpace } from "@/lib/spaces/hooks";
import { spaceUi } from "@/lib/spaces/store";
import { SpaceGlyph } from "./icons";

export function StarterSpaceSection() {
  const { t } = useTranslation("spaces");
  const wid = useSession((s) => s.workspaceId);
  const admin = atLeast(useRole(), "admin");
  const { data: starter, isLoading } = useStarterSpace(admin ? wid : null);
  const remove = useRemoveStarter();
  const confirm = useConfirm();
  if (!admin) return null;
  const rows = starter?.sections.reduce((n, s) => n + s.items.length, 0) ?? 0;
  return (
    <Section title={t("starter.title")} description={t("starter.description")}>
      {isLoading ? (
        <div className="py-3.5">
          <Skeleton className="h-8 w-64" />
        </div>
      ) : starter ? (
        <Row
          label={
            <span className="flex items-center gap-2">
              <SpaceGlyph icon={starter.icon} name={starter.name} color={starter.color} />
              {starter.name}
            </span>
          }
          description={`${t("starter.sections", { count: starter.sections.length })}, ${t("editor.rows", { count: rows })}`}
        >
          <Button icon={<Pencil />} onClick={() => spaceUi.edit({ kind: "starter" })}>
            {t("starter.edit")}
          </Button>
          <Button
            variant="ghost"
            icon={<Trash2 />}
            onClick={() =>
              confirm.show(
                t("editor.removeStarterBody"),
                async () => {
                  await remove.mutateAsync();
                  toast.success(t("editor.removed"));
                },
                { title: t("editor.removeStarterTitle"), confirmLabel: t("editor.removeStarter"), danger: true },
              )
            }
          >
            {t("starter.remove")}
          </Button>
        </Row>
      ) : (
        <Row label={t("starter.none")}>
          <Button icon={<Plus />} onClick={() => spaceUi.edit({ kind: "starter" })}>
            {t("starter.create")}
          </Button>
        </Row>
      )}
    </Section>
  );
}
