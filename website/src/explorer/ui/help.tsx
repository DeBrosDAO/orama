import { useId } from "react";
import { cn } from "../../lib/utils";

/**
 * A small "?" that explains a term in one plain sentence. Opens on hover and on
 * keyboard focus; the sentence is also the button's accessible description.
 */
export function Help({ tip, className }: { tip: string; className?: string }) {
  const id = useId();
  return (
    <span className={cn("relative inline-flex group/help align-middle", className)}>
      <button
        type="button"
        aria-describedby={id}
        aria-label="What does this mean?"
        className="ml-1.5 grid h-4 w-4 place-items-center rounded-full border border-border text-[10px] leading-none text-muted hover:text-fg hover:border-fg/40 focus-visible:text-fg focus-visible:border-fg/60 cursor-help normal-case tracking-normal font-medium"
      >
        ?
      </button>
      <span
        role="tooltip"
        id={id}
        className="pointer-events-none absolute left-1/2 top-6 z-30 hidden w-60 -translate-x-1/2 rounded-lg border border-border bg-surface-3 px-3 py-2 text-xs font-normal normal-case leading-relaxed tracking-normal text-fg shadow-xl group-hover/help:block group-focus-within/help:block"
      >
        {tip}
      </span>
    </span>
  );
}
