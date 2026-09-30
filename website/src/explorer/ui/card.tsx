import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export interface CardProps {
  title?: ReactNode;
  /** Right-aligned control in the title row: a link, a range switch. */
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}

export function Card({ title, action, children, className }: CardProps) {
  return (
    <section className={cn("rounded-xl border border-border bg-surface p-4", className)}>
      {(title || action) && (
        <div className="mb-3 flex items-center justify-between gap-3">
          {title && <h2 className="text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">{title}</h2>}
          {action}
        </div>
      )}
      {children}
    </section>
  );
}
