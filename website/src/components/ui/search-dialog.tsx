import { useState, useCallback, useEffect, useRef, useMemo } from "react";
import { useNavigate, useLocation } from "react-router";
import * as Dialog from "@radix-ui/react-dialog";
import { Search, Hash } from "lucide-react";
import { ALL_DOCS } from "../../data/docs-navigation";
import { SECTION_INDEX } from "virtual:docs-search-index";
import type { DocLink } from "../../data/docs-navigation";
import type { Persona } from "../../types/persona";
import { cn } from "../../lib/utils";

const PERSONA_LABELS: Record<Persona, string> = {
  developer: "Dev",
  operator: "Ops",
  contributor: "Contrib",
};

const PERSONA_COLORS: Record<Persona, string> = {
  developer: "text-accent bg-accent/10",
  operator: "text-accent-2 bg-accent-2/10",
  contributor: "text-muted bg-surface-2",
};

type SearchResult =
  | { kind: "page"; link: DocLink; persona: Persona }
  | {
      kind: "section";
      pageTitle: string;
      pageSlug: string;
      sectionTitle: string;
      sectionId: string;
      persona: Persona;
    };

export function SearchDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);

  const results: SearchResult[] = useMemo(() => {
    if (!query.trim()) {
      // No query — show pages only
      return ALL_DOCS.map(({ link, persona }) => ({
        kind: "page" as const,
        link,
        persona,
      }));
    }

    const q = query.trim().toLowerCase();
    const items: SearchResult[] = [];

    // Match pages
    for (const { link, persona } of ALL_DOCS) {
      if (
        link.title.toLowerCase().includes(q) ||
        link.description.toLowerCase().includes(q)
      ) {
        items.push({ kind: "page", link, persona });
      }
    }

    // Match sections
    for (const s of SECTION_INDEX) {
      if (s.sectionTitle.toLowerCase().includes(q)) {
        items.push({
          kind: "section",
          pageTitle: s.pageTitle,
          pageSlug: s.pageSlug,
          sectionTitle: s.sectionTitle,
          sectionId: s.sectionId,
          persona: s.persona,
        });
      }
    }

    return items;
  }, [query]);

  // Reset on open
  useEffect(() => {
    if (open) {
      setQuery("");
      setSelectedIndex(0);
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  // Clamp selected index
  useEffect(() => {
    if (selectedIndex >= results.length) {
      setSelectedIndex(Math.max(0, results.length - 1));
    }
  }, [results.length, selectedIndex]);

  // Global Cmd+K listener
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        onOpenChange(!open);
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [open, onOpenChange]);

  const goTo = useCallback(
    (result: SearchResult) => {
      onOpenChange(false);

      if (result.kind === "page") {
        navigate(`/docs/${result.link.slug}`);
      } else {
        const targetPath = `/docs/${result.pageSlug}`;
        if (pathname === targetPath) {
          // Same page — just scroll to section
          document
            .getElementById(result.sectionId)
            ?.scrollIntoView({ behavior: "smooth" });
        } else {
          // Navigate then scroll after render
          navigate(targetPath);
          setTimeout(() => {
            document
              .getElementById(result.sectionId)
              ?.scrollIntoView({ behavior: "smooth" });
          }, 300);
        }
      }
    },
    [navigate, onOpenChange, pathname],
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((i) => Math.min(i + 1, results.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((i) => Math.max(i - 1, 0));
      } else if (e.key === "Enter" && results[selectedIndex]) {
        e.preventDefault();
        goTo(results[selectedIndex]);
      }
    },
    [results, selectedIndex, goTo],
  );

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-bg/80 backdrop-blur-sm animate-in fade-in" />
        <Dialog.Content
          className="fixed z-50 top-[15vh] left-1/2 -translate-x-1/2 w-[90vw] max-w-xl bg-surface border border-border/60 rounded-lg shadow-[0_16px_64px_rgba(0,0,0,0.6)] overflow-hidden"
          onKeyDown={handleKeyDown}
        >
          {/* Search input */}
          <div className="flex items-center gap-3 px-4 py-3 border-b border-border/40">
            <Search size={16} className="text-muted shrink-0" />
            <Dialog.Title className="sr-only">
              Search documentation
            </Dialog.Title>
            <Dialog.Description className="sr-only">
              Type to search across all documentation pages and sections
            </Dialog.Description>
            <input
              ref={inputRef}
              type="text"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setSelectedIndex(0);
              }}
              placeholder="Search all docs..."
              className="flex-1 bg-transparent text-sm text-fg placeholder:text-muted/50 outline-none font-mono"
            />
            <kbd className="text-[10px] text-muted/40 border border-border/30 rounded px-1.5 py-0.5">
              ESC
            </kbd>
          </div>

          {/* Results */}
          <div className="max-h-[50vh] overflow-y-auto py-2">
            {results.length === 0 ? (
              <div className="px-4 py-8 text-center">
                <p className="text-xs text-muted">
                  No docs matching &quot;{query}&quot;
                </p>
              </div>
            ) : (
              <ul>
                {results.map((result, index) => {
                  const isSelected = index === selectedIndex;

                  if (result.kind === "page") {
                    const Icon = result.link.icon;
                    return (
                      <li key={`p-${result.link.slug}`}>
                        <button
                          type="button"
                          onClick={() => goTo(result)}
                          onMouseEnter={() => setSelectedIndex(index)}
                          className={cn(
                            "w-full flex items-center gap-3 px-4 py-2.5 text-left transition-colors",
                            isSelected
                              ? "bg-surface-2"
                              : "hover:bg-surface-2/50",
                          )}
                        >
                          <Icon
                            size={14}
                            className={cn(
                              "shrink-0",
                              isSelected ? "text-accent" : "text-muted/50",
                            )}
                          />
                          <div className="flex-1 min-w-0">
                            <span
                              className={cn(
                                "text-sm",
                                isSelected ? "text-fg" : "text-fg/70",
                              )}
                            >
                              {result.link.title}
                            </span>
                            <span className="text-xs text-muted/50 ml-2">
                              {result.link.description}
                            </span>
                          </div>
                          <span
                            className={cn(
                              "text-[9px] font-mono tracking-wider uppercase px-1.5 py-0.5 rounded shrink-0",
                              PERSONA_COLORS[result.persona],
                            )}
                          >
                            {PERSONA_LABELS[result.persona]}
                          </span>
                        </button>
                      </li>
                    );
                  }

                  // Section result
                  return (
                    <li key={`s-${result.pageSlug}-${result.sectionId}`}>
                      <button
                        type="button"
                        onClick={() => goTo(result)}
                        onMouseEnter={() => setSelectedIndex(index)}
                        className={cn(
                          "w-full flex items-center gap-3 px-4 py-2.5 text-left transition-colors",
                          isSelected
                            ? "bg-surface-2"
                            : "hover:bg-surface-2/50",
                        )}
                      >
                        <Hash
                          size={14}
                          className={cn(
                            "shrink-0",
                            isSelected ? "text-accent" : "text-muted/50",
                          )}
                        />
                        <div className="flex-1 min-w-0">
                          <span
                            className={cn(
                              "text-sm",
                              isSelected ? "text-fg" : "text-fg/70",
                            )}
                          >
                            {result.sectionTitle}
                          </span>
                          <span className="text-xs text-muted/40 ml-2">
                            in {result.pageTitle}
                          </span>
                        </div>
                        <span
                          className={cn(
                            "text-[9px] font-mono tracking-wider uppercase px-1.5 py-0.5 rounded shrink-0",
                            PERSONA_COLORS[result.persona],
                          )}
                        >
                          {PERSONA_LABELS[result.persona]}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {/* Footer hint */}
          <div className="flex items-center gap-4 px-4 py-2 border-t border-border/40 text-[10px] text-muted/40 font-mono">
            <span>↑↓ navigate</span>
            <span>↵ select</span>
            <span>esc close</span>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
