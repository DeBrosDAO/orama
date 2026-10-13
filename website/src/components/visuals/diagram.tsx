import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "../../lib/utils";

/**
 * Shared pieces for the blueprint diagrams. Every diagram is an SVG with a
 * fixed viewBox that scales to its container, so one drawing serves phone and
 * desktop.
 */

export interface DiagramProps {
  viewBox: string;
  label: string;
  className?: string;
  children: ReactNode;
}

export function Diagram({ viewBox, label, className, children }: DiagramProps) {
  return (
    <svg
      viewBox={viewBox}
      role="img"
      aria-label={label}
      className={cn("w-full h-auto select-none", className)}
      fill="none"
    >
      {children}
    </svg>
  );
}

export interface NodeProps {
  x: number;
  y: number;
  r?: number;
  icon: LucideIcon;
  tone?: "bright" | "normal" | "dim";
}

const nodeTone = {
  bright: { ring: "stroke-fg", fill: "fill-surface-2", icon: "text-fg" },
  normal: { ring: "stroke-accent/60", fill: "fill-surface", icon: "text-accent" },
  dim: { ring: "stroke-border", fill: "fill-surface", icon: "text-muted/60" },
} as const;

/** A machine in the network: a ring with an icon at its centre. */
export function NodeDot({ x, y, r = 16, icon: Icon, tone = "normal" }: NodeProps) {
  const t = nodeTone[tone];
  const s = r * 0.95;
  return (
    <g>
      <circle cx={x} cy={y} r={r} className={cn(t.fill, t.ring)} strokeWidth={1} />
      <Icon
        x={x - s / 2}
        y={y - s / 2}
        width={s}
        height={s}
        strokeWidth={1.5}
        className={t.icon}
      />
    </g>
  );
}

export interface EdgeProps {
  from: [number, number];
  to: [number, number];
  /** Animate data moving along the link. */
  flowing?: boolean;
  tone?: "bright" | "normal" | "dim";
}

export function Edge({ from, to, flowing = false, tone = "normal" }: EdgeProps) {
  const base =
    tone === "bright" ? "stroke-fg/50" : tone === "dim" ? "stroke-border" : "stroke-accent/30";
  return (
    <g>
      <line x1={from[0]} y1={from[1]} x2={to[0]} y2={to[1]} className={base} strokeWidth={1} />
      {flowing && (
        <line
          x1={from[0]}
          y1={from[1]}
          x2={to[0]}
          y2={to[1]}
          className={cn("animate-flow", tone === "bright" ? "stroke-fg" : "stroke-accent-2/80")}
          strokeWidth={1.5}
        />
      )}
    </g>
  );
}

export interface LabelProps {
  x: number;
  y: number;
  children: string;
  anchor?: "start" | "middle" | "end";
  tone?: "bright" | "muted";
  size?: number;
}

export function Label({ x, y, children, anchor = "middle", tone = "muted", size = 11 }: LabelProps) {
  return (
    <text
      x={x}
      y={y}
      textAnchor={anchor}
      fontSize={size}
      className={cn("font-mono uppercase", tone === "bright" ? "fill-fg" : "fill-muted")}
      letterSpacing="0.08em"
    >
      {children}
    </text>
  );
}
