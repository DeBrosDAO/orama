import { cva } from "class-variance-authority";
import type { VariantProps } from "class-variance-authority";
import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export const badgeVariants = cva(
  "inline-flex items-center px-2.5 py-0.5 text-xs font-mono tracking-wider",
  {
    variants: {
      variant: {
        default: "border border-dashed border-border text-muted",
        outline: "border border-dashed border-accent text-accent",
        accent: "text-accent tracking-widest",
        status: "border border-dashed border-accent-2 text-accent-2",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
);

export interface BadgeProps
  extends HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <span className={cn(badgeVariants({ variant, className }))} {...props} />
  );
}
