import { useCallback } from "react";
import { useActiveHeading } from "../../hooks/useActiveHeading";
import { cn } from "../../lib/utils";

export function TableOfContents() {
  const { headings, activeId } = useActiveHeading();

  // Only show h2 (main sections), not h3 subtitles
  const sections = headings.filter((h) => h.level === 2);

  const scrollTo = useCallback((id: string) => {
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth" });
  }, []);

  if (sections.length === 0) return null;

  return (
    <nav
      aria-label="Table of contents"
      className="hidden xl:flex fixed right-6 top-1/2 -translate-y-1/2 z-20 flex-col items-center"
    >
      {sections.map((h, i) => {
        const isActive = h.id === activeId;

        return (
          <div key={h.id} className="flex flex-col items-center">
            {/* Connecting line */}
            {i > 0 && (
              <div
                className={cn(
                  "w-px h-5",
                  isActive
                    ? "bg-accent/30"
                    : "border-l border-dotted border-muted/20",
                )}
              />
            )}

            {/* Dot + tooltip */}
            <div className="relative group flex items-center cursor-pointer">
              <button
                type="button"
                onClick={() => scrollTo(h.id)}
                aria-label={h.text}
                className={cn(
                  "rounded-full transition-all duration-200 shrink-0 cursor-pointer",
                  isActive
                    ? "w-3.5 h-3.5 bg-accent shadow-[0_0_10px_rgba(65,105,225,0.5)]"
                    : "w-2.5 h-2.5 bg-muted/25 group-hover:bg-accent/50 group-hover:w-3.5 group-hover:h-3.5",
                )}
              />

              {/* Tooltip */}
              <div className="absolute right-full mr-4 pointer-events-none opacity-0 group-hover:opacity-100 transition-all duration-150 whitespace-nowrap translate-x-1 group-hover:translate-x-0">
                <span
                  className={cn(
                    "text-xs font-mono px-2.5 py-1.5 rounded-md bg-surface-2 border border-border/50 shadow-[0_4px_16px_rgba(0,0,0,0.4)] backdrop-blur-sm",
                    isActive ? "text-accent" : "text-fg/80",
                  )}
                >
                  {h.text}
                </span>
              </div>
            </div>
          </div>
        );
      })}
    </nav>
  );
}
