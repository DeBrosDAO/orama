import type { ReactNode } from "react";
import { useInView } from "../../hooks/useInView";
import { cn } from "../../lib/utils";

/**
 * Fades its children in when they scroll into view. The hidden state is
 * keyed on data-animate and only applies under html.js (set by an inline
 * script in index.html), so the prerendered page, and any reader without
 * JavaScript, shows everything.
 */
export function AnimateIn({ children, className }: { children: ReactNode; className?: string }) {
  const { ref, isInView } = useInView(0.1);
  return (
    <div ref={ref} data-animate={isInView ? "in" : "out"} className={cn("animate-in", className)}>
      {children}
    </div>
  );
}
