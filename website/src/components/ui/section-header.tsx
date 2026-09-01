import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export interface SectionHeaderProps {
  title: string;
  subtitle?: ReactNode;
  className?: string;
}

export function SectionHeader({
  title,
  subtitle,
  className,
}: SectionHeaderProps) {
  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <div className="flex items-center gap-4">
        {/* No `whitespace-nowrap`: a long title has to be allowed to wrap, or it
            pushes past the viewport on narrow screens. `min-w-0` lets the rule
            shrink instead of forcing overflow. */}
        <h2 className="font-display text-xl font-bold text-fg tracking-tight text-balance">
          {title}
        </h2>
        <div className="flex-1 min-w-0 border-t border-dashed border-border" />
      </div>

      {subtitle && (
        <p className="text-sm text-muted">{subtitle}</p>
      )}
    </div>
  );
}
