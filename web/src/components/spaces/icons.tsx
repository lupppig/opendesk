// A Space's look: its icon (SPACE_ICONS mapped to lucide) or its name's first letter, in its
// colour. Soft tinted tiles, like the app's other colour marks.

import {
  BarChart3,
  Bell,
  Bookmark,
  Briefcase,
  Coffee,
  Flag,
  Flame,
  Globe,
  Headphones,
  Heart,
  House,
  Inbox,
  Layers,
  Leaf,
  Mail,
  Moon,
  Rocket,
  Shield,
  Sparkles,
  Star,
  Sun,
  Target,
  Users,
  Zap,
  type LucideIcon,
} from "lucide-react";
import type { SpaceColor } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { isSpaceIcon, type SpaceIcon } from "@/lib/spaces/model";
import { tone, type Tone } from "@/lib/tones";

export const spaceIcons: Record<SpaceIcon, LucideIcon> = {
  sun: Sun,
  moon: Moon,
  star: Star,
  inbox: Inbox,
  mail: Mail,
  headphones: Headphones,
  users: Users,
  briefcase: Briefcase,
  rocket: Rocket,
  flame: Flame,
  zap: Zap,
  heart: Heart,
  coffee: Coffee,
  flag: Flag,
  bookmark: Bookmark,
  layers: Layers,
  target: Target,
  globe: Globe,
  bell: Bell,
  leaf: Leaf,
  home: House,
  chart: BarChart3,
  shield: Shield,
  sparkles: Sparkles,
};

/** A Space colour as the palette's tone (grey is the palette's gray). */
export const spaceTone = (c: SpaceColor): Tone => (c === "grey" ? "gray" : c);

export function SpaceGlyph({ icon, name, color, size = "md", className }: { icon: string; name: string; color: SpaceColor; size?: "sm" | "md" | "lg"; className?: string }) {
  const Icon = isSpaceIcon(icon) ? spaceIcons[icon] : null;
  const t = tone[spaceTone(color)];
  const box = size === "sm" ? "size-4 rounded-[4px] text-[9px]" : size === "lg" ? "size-7 rounded-md text-[12px]" : "size-[18px] rounded-[5px] text-[10px]";
  const svg = size === "sm" ? "size-2.5" : size === "lg" ? "size-4" : "size-3";
  return (
    <span aria-hidden className={cn("flex shrink-0 items-center justify-center font-semibold leading-none", t.soft, t.text, box, className)}>
      {Icon ? <Icon className={cn(svg, "!text-current")} strokeWidth={2.25} /> : (name.trim().charAt(0).toUpperCase() || "?")}
    </span>
  );
}
