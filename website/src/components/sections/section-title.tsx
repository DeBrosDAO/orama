import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export interface SectionTitleProps {
  eyebrow?: string;
  title: ReactNode;
  line?: ReactNode;
  align?: "center" | "left";
  className?: string;
}

/** Headline for a section: at most one short line under it. */
export function SectionTitle({ eyebrow, title, line, align = "center", className }: SectionTitleProps) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 mb-10 sm:mb-14 print:mb-6 print:break-inside-avoid print:break-after-avoid",
        align === "center" ? "items-center text-center" : "items-start text-left",
        className,
      )}
    >
      {eyebrow && (
        <span className="font-mono text-[11px] tracking-[0.25em] uppercase text-muted">{eyebrow}</span>
      )}
      <h2 className="font-display font-bold text-3xl sm:text-4xl tracking-tight text-fg text-balance max-w-3xl">
        {title}
      </h2>
      {line && <p className="text-muted max-w-2xl text-pretty">{line}</p>}
    </div>
  );
}
