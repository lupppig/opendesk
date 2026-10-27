import { useState } from "react";
import { cn } from "@/lib/cn";
import { tone, toneFor } from "@/lib/tones";

export function initials(name: string) {
  const parts = name.trim().split(/[\s@._-]+/).filter(Boolean);
  if (!parts.length) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

// People are round; organisations (companies, workspaces) are rounded squares,
// echoing the square modules of the mark.
const sizes = {
  xs: "size-4 text-[7.5px]",
  sm: "size-5 text-[8.5px]",
  md: "size-6 text-[9.5px]",
  lg: "size-8 text-[11px]",
  xl: "size-10 text-[13px]",
  "2xl": "size-14 text-[17px]",
};
export type Presence = "online" | "away" | "offline";
/** Presence dot colours: online green, away amber, offline grey. Shared with the availability menu. */
export const presenceDot: Record<Presence, string> = {
  online: "bg-tone-green-solid",
  away: "bg-tone-amber-solid",
  offline: "bg-tone-gray-solid",
};
const dotSizes = { xs: "size-1.5", sm: "size-[7px]", md: "size-2", lg: "size-2.5", xl: "size-[11px]", "2xl": "size-3.5" };

const SMALL = new Set(["xs", "sm", "md", "lg"]);

/**
 * Our own processed images (`GET /v1/images/{id}`) come in 256 and 64 px; ask
 * for the small one where the avatar is 32 px or less. Other URLs are left alone.
 */
export function sizedSrc(src: string | null | undefined, size: string): string | null | undefined {
  if (!src || !SMALL.has(size) || src.includes("?") || !/\/v1\/images\/[^/]+$/.test(src.split("#")[0])) return src;
  return `${src}?s=64`;
}

const squares = { xs: "rounded-xs", sm: "rounded-sm", md: "rounded-sm", lg: "rounded-md", xl: "rounded-lg", "2xl": "rounded-xl" };

export function Avatar({
  name,
  src,
  size = "md",
  className,
  ring,
  square,
  presence,
}: {
  name: string;
  src?: string | null;
  size?: keyof typeof sizes;
  className?: string;
  ring?: boolean;
  /** A company or workspace: rounded square instead of a circle. */
  square?: boolean;
  /** A presence dot at the bottom right, ringed in the surface colour. */
  presence?: Presence;
}) {
  // Keyed by URL, so a new picture starts hidden even when it loads before an effect could reset a flag.
  const [loadedUrl, setLoadedUrl] = useState<string | null>(null);
  const [brokenUrl, setBrokenUrl] = useState<string | null>(null);
  const url = sizedSrc(src, size);
  const loaded = !!url && loadedUrl === url;
  const showImg = !!url && brokenUrl !== url;
  const shade = tone[toneFor(name)].chip;
  const face = (
    <span
      className={cn(
        "relative inline-flex shrink-0 select-none items-center justify-center overflow-hidden font-semibold tracking-[-0.02em]",
        sizes[size],
        square ? squares[size] : "rounded-full",
        shade,
        ring && "ring-2 ring-surface",
        className,
      )}
      title={name}
      aria-hidden
    >
      {/* Initials hold the place; the photo fades in over them once it has loaded. */}
      <span className={cn("transition-opacity duration-200", showImg && loaded && "opacity-0")}>{initials(name)}</span>
      {showImg && (
        <img
          key={url}
          src={url ?? undefined}
          alt=""
          className={cn("absolute inset-0 size-full object-cover transition-opacity duration-200 ease-out", loaded ? "opacity-100" : "opacity-0")}
          onLoad={() => setLoadedUrl(url ?? null)}
          onError={() => setBrokenUrl(url ?? null)}
          referrerPolicy="no-referrer"
        />
      )}
    </span>
  );
  if (!presence) return face;
  return (
    <span className="relative inline-flex shrink-0" aria-hidden>
      {face}
      <span
        className={cn(
          "absolute rounded-full ring-2 ring-surface transition-colors duration-200",
          square ? "-bottom-0.5 -right-0.5" : "bottom-0 right-0",
          dotSizes[size],
          presenceDot[presence],
        )}
      />
    </span>
  );
}

/** The green ring of someone on a ticket right now; it breathes unless motion is reduced. */
export const liveRing = "shadow-[0_0_0_1.5px_var(--tone-green-solid)] animate-live-ring motion-reduce:animate-none";

export function AvatarStack({
  people,
  size = "sm",
  max = 3,
  live,
  className,
}: {
  people: { id: string; name: string; avatar?: string | null; state?: "viewing" | "replying" | "idle" }[];
  size?: "xs" | "sm" | "md";
  max?: number;
  /** People on it right now: green rings instead of the surface ring. */
  live?: boolean;
  className?: string;
}) {
  const shown = people.slice(0, max);
  const extra = people.length - shown.length;
  return (
    <span className={cn("flex items-center", live ? "-space-x-0.5" : "-space-x-1", className)}>
      {shown.map((p) => (
        <span key={p.id} className="relative">
          <Avatar name={p.name} src={p.avatar} size={size} ring={!live} className={live ? liveRing : undefined} />
          {p.state === "replying" && (
            <span className="absolute -bottom-px -right-px size-[7px] rounded-full bg-warn ring-[1.5px] ring-surface" />
          )}
        </span>
      ))}
      {extra > 0 && (
        <span className="relative inline-flex size-5 items-center justify-center rounded-full bg-hover text-[9px] font-medium text-fg-2 ring-2 ring-surface">
          +{extra}
        </span>
      )}
    </span>
  );
}
