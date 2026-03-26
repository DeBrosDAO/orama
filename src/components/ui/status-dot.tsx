import { cva } from "class-variance-authority";
import type { VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

export const statusDotVariants = cva("w-2 h-2 rounded-full shrink-0", {
  variants: {
    status: {
      active: "bg-accent-2 animate-pulse-dot",
      warning: "bg-amber-500",
      error: "bg-red-500",
      neutral: "bg-muted",
    },
  },
  defaultVariants: {
    status: "neutral",
  },
});

export interface StatusDotProps extends VariantProps<typeof statusDotVariants> {
  label?: string;
  className?: string;
}

export function StatusDot({ status, label, className }: StatusDotProps) {
  if (label) {
    return (
      <span className={cn("inline-flex items-center gap-2", className)}>
        <span className={statusDotVariants({ status })} />
        <span className="text-xs font-mono text-muted">{label}</span>
      </span>
    );
  }

  return (
    <span className={cn(statusDotVariants({ status }), className)} />
  );
}
