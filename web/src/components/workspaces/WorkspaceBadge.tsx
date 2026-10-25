// A workspace's mark wherever things of several workspaces sit together (Together):
// its logo when it has one, else its initials on its soft identity tone. A rounded
// square, like every organisation. Its accessible name is the workspace's name.

import { useState } from "react";
import { cn } from "@/lib/cn";
import { tone, toneFor } from "@/lib/tones";
import { workspaceInitials } from "@/lib/together";

const sizes = {
  xs: "size-3.5 rounded-[3px] text-[7px]",
  sm: "size-4 rounded-xs text-[7.5px]",
  md: "size-5 rounded-sm text-[8.5px]",
  lg: "size-6 rounded-md text-[9.5px]",
};

export function WorkspaceBadge({
  name,
  logo,
  size = "sm",
  className,
  label,
  decorative,
}: {
  name: string;
  logo?: string | null;
  size?: keyof typeof sizes;
  className?: string;
  /** The accessible name; default the workspace's name. */
  label?: string;
  /** Next to the name in text already: hidden from assistive tech. */
  decorative?: boolean;
}) {
  const [broken, setBroken] = useState(false);
  const a11y = decorative ? { "aria-hidden": true as const } : { role: "img" as const, "aria-label": label ?? name, title: label ?? name };
  if (logo && !broken) {
    return (
      <span {...a11y} className={cn("inline-flex shrink-0 overflow-hidden bg-surface shadow-[inset_0_0_0_1px_var(--line)]", sizes[size], className)}>
        <img src={logo} alt="" className="size-full object-cover" onError={() => setBroken(true)} />
      </span>
    );
  }
  return (
    <span
      {...a11y}
      className={cn("inline-flex shrink-0 select-none items-center justify-center font-semibold leading-none tracking-tight", tone[toneFor(name)].chip, sizes[size], className)}
    >
      {workspaceInitials(name)}
    </span>
  );
}
