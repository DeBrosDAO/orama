import { Link } from "react-router";
import { ArrowLeft, ArrowRight } from "lucide-react";
import { cn } from "../../lib/utils";

export interface PaginationProps {
  page: number;
  pages: number;
  /** The address of page n. */
  pathFor: (page: number) => string;
}

const linkClass =
  "inline-flex items-center gap-2 px-4 py-2 border border-border/60 font-mono text-xs tracking-wider uppercase text-muted hover:text-fg hover:border-fg/30 transition-colors";

/** Numbered pages of a post list, with plain links so crawlers reach every page. */
export function Pagination({ page, pages, pathFor }: PaginationProps) {
  if (pages < 2) return null;
  return (
    <nav aria-label="Pages" className="flex flex-wrap items-center justify-center gap-2 pt-12">
      {page > 1 && (
        <Link to={pathFor(page - 1)} className={linkClass}>
          <ArrowLeft size={12} aria-hidden="true" /> Newer
        </Link>
      )}
      <ol className="flex items-center gap-1">
        {Array.from({ length: pages }, (_, i) => i + 1).map((n) => (
          <li key={n}>
            <Link
              to={pathFor(n)}
              aria-current={n === page ? "page" : undefined}
              aria-label={`Page ${n}`}
              className={cn(
                "flex items-center justify-center w-9 h-9 font-mono text-xs transition-colors",
                n === page ? "text-fg border border-fg/40" : "text-muted hover:text-fg",
              )}
            >
              {n}
            </Link>
          </li>
        ))}
      </ol>
      {page < pages && (
        <Link to={pathFor(page + 1)} className={linkClass}>
          Older <ArrowRight size={12} aria-hidden="true" />
        </Link>
      )}
    </nav>
  );
}
