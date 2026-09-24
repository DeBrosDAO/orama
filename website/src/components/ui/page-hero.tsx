import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export interface PageHeroProps {
  eyebrow: string;
  title: ReactNode;
  line?: ReactNode;
  children?: ReactNode;
  className?: string;
}

/** The top of every inner page: a label, one big line, one small line. */
export function PageHero({
  eyebrow,
  title,
  line,
  children,
  className,
}: PageHeroProps) {
  return (
    <section
      className={cn(
        "max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 pt-16 sm:pt-24 pb-10 sm:pb-14",
        className,
      )}
    >
      <div className="flex flex-col items-center text-center gap-5">
        <span className="font-mono text-[11px] tracking-[0.25em] uppercase text-muted">
          {eyebrow}
        </span>
        <h1 className="font-display font-bold text-4xl sm:text-5xl lg:text-6xl tracking-tight text-fg text-balance max-w-4xl leading-[1.05]">
          {title}
        </h1>
        {line && (
          <p className="text-muted text-base sm:text-lg max-w-2xl text-pretty">
            {line}
          </p>
        )}
        {children}
      </div>
    </section>
  );
}
