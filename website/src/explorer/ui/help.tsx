import { useId, useState } from "react";
import type { KeyboardEvent } from "react";
import { cn } from "../../lib/utils";

/** Whether this key dismisses an open tip (WCAG 1.4.13: the reader can hide it without moving the pointer or focus). */
export function dismissesTip(key: string): boolean {
  return key === "Escape";
}

const TIP_SHOWN = "hidden group-hover/help:block group-focus-within/help:block";

/**
 * A small "?" that explains a term in one plain sentence. Opens on hover and on
 * keyboard focus, closes on Escape; the sentence is also the button's accessible
 * description. The button's touch area is 24 by 24 pixels (the circle is smaller).
 */
export function Help({ tip, className }: { tip: string; className?: string }) {
  const id = useId();
  const [dismissed, setDismissed] = useState(false);
  const onKeyDown = (event: KeyboardEvent) => {
    if (dismissesTip(event.key)) setDismissed(true);
  };
  return (
    <span
      className={cn("relative inline-flex group/help align-middle", className)}
      onKeyDown={onKeyDown}
      onMouseLeave={() => setDismissed(false)}
      onBlur={() => setDismissed(false)}
    >
      <button
        type="button"
        aria-describedby={id}
        aria-label="What does this mean?"
        className="relative ml-1.5 grid h-4 w-4 place-items-center rounded-full border border-border text-[10px] leading-none text-muted hover:text-fg hover:border-fg/40 focus-visible:text-fg focus-visible:border-fg/60 cursor-help normal-case tracking-normal font-medium before:absolute before:-inset-1 before:content-['']"
      >
        ?
      </button>
      <span
        role="tooltip"
        id={id}
        className={cn(
          "pointer-events-none absolute left-1/2 top-6 z-30 w-60 -translate-x-1/2 rounded-lg border border-border bg-surface-3 px-3 py-2 text-xs font-normal normal-case leading-relaxed tracking-normal text-fg shadow-xl",
          dismissed ? "hidden" : TIP_SHOWN,
        )}
      >
        {tip}
      </span>
    </span>
  );
}
