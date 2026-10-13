import { cn } from "../../lib/utils";

/**
 * The site's honesty marker. "now" things are grey-white and solid; "future"
 * things carry the one signal colour and a dashed border, so a reader can tell
 * shipped from planned at a glance without reading a word.
 */
export type BadgeTone = "now" | "soon" | "future";

const toneClass: Record<BadgeTone, string> = {
  now: "border-fg/25 text-fg",
  soon: "border-dashed border-fg/25 text-accent",
  future: "border-dashed border-signal/40 text-signal",
};

const dotClass: Record<BadgeTone, string> = {
  now: "bg-fg animate-pulse-dot text-fg",
  soon: "bg-accent",
  future: "bg-signal",
};

export interface StatusBadgeProps {
  tone: BadgeTone;
  children: string;
  className?: string;
}

export function StatusBadge({ tone, children, className }: StatusBadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full border text-[10px] font-mono tracking-widest uppercase whitespace-nowrap",
        toneClass[tone],
        className,
      )}
    >
      <span className={cn("w-1.5 h-1.5 rounded-full", dotClass[tone])} />
      {children}
    </span>
  );
}
