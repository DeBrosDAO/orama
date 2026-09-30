import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export type BadgeTone = "ok" | "bad" | "signal" | "mute";

const TONES: Record<BadgeTone, string> = {
  ok: "bg-gain/15 text-gain",
  bad: "bg-loss/15 text-loss",
  signal: "bg-signal/15 text-signal",
  mute: "bg-surface-3 text-muted",
};

export function Badge({ tone = "mute", children, className }: { tone?: BadgeTone; children: ReactNode; className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-semibold", TONES[tone], className)}>
      {children}
    </span>
  );
}
