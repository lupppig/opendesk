// Buttons and menu items behind a permission: disabled, with a tooltip naming
// the role that is needed, when the signed-in admin does not hold it. The API
// enforces the same rule either way.

import { forwardRef } from "react";
import { useTranslation } from "react-i18next";
import { Button, type ButtonProps } from "@/components/ui/button";
import { MenuItem } from "@/components/ui/menu";
import { Tooltip } from "@/components/ui/tooltip";
import type { AdminPermission } from "@/lib/api/types";
import { deniedReason } from "@/lib/permissions";
import { useSession } from "@/lib/session";

export function useDenied(perm: AdminPermission): string | null {
  const me = useSession((s) => s.me);
  return deniedReason(me, perm);
}

export const ActionButton = forwardRef<HTMLButtonElement, ButtonProps & { perm: AdminPermission; blocked?: string | null }>(function ActionButton(
  { perm, blocked, disabled, ...rest },
  ref,
) {
  const { t } = useTranslation();
  const denied = useDenied(perm);
  const reason = denied ?? blocked ?? null;
  if (!reason) return <Button ref={ref} disabled={disabled} {...rest} />;
  // A disabled button gets no pointer events; the wrapper carries the tooltip.
  return (
    <Tooltip content={reason}>
      <span tabIndex={0} className="inline-flex rounded-md outline-none" aria-label={`${typeof rest.children === "string" ? rest.children : t("action")}: ${reason}`}>
        <Button ref={ref} disabled {...rest} />
      </span>
    </Tooltip>
  );
});

export function ActionMenuItem({
  perm,
  blocked,
  children,
  ...rest
}: React.ComponentProps<typeof MenuItem> & { perm: AdminPermission; blocked?: string | null }) {
  const denied = useDenied(perm);
  const reason = denied ?? blocked ?? null;
  return (
    <MenuItem {...rest} disabled={!!reason || rest.disabled}>
      <span className="flex min-w-0 flex-col">
        <span className="truncate">{children}</span>
        {reason && <span className="truncate text-[11px] text-fg-4">{reason}</span>}
      </span>
    </MenuItem>
  );
}
