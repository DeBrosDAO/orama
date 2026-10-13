import { Fragment } from "react";
import { Link } from "react-router";
import { ChevronRight } from "lucide-react";
import { cn } from "../../lib/utils";
import type { Crumb } from "../../content/pages";

export interface BreadcrumbsProps {
  crumbs: Crumb[];
  className?: string;
}

/**
 * The trail from the home page to this one. Search engines read the same
 * trail as BreadcrumbList structured data (src/content/seo.ts); this is the
 * visible half. The last crumb is the current page, so it is not a link.
 */
export function Breadcrumbs({ crumbs, className }: BreadcrumbsProps) {
  if (crumbs.length < 2) return null;
  return (
    <nav aria-label="Breadcrumb" className={cn("no-print", className)}>
      <ol className="flex flex-wrap items-center gap-x-1.5 gap-y-1 font-mono text-[11px] tracking-wider uppercase text-muted">
        {crumbs.map((c, i) => {
          const isLast = i === crumbs.length - 1;
          return (
            <Fragment key={c.path}>
              {i > 0 && (
                <li aria-hidden="true" className="flex items-center text-muted/50">
                  <ChevronRight size={11} />
                </li>
              )}
              <li className="min-w-0">
                {isLast ? (
                  <span aria-current="page" className="block truncate max-w-[60vw] sm:max-w-md text-fg/80">
                    {c.name}
                  </span>
                ) : (
                  <Link to={c.path} className="hover:text-fg transition-colors">
                    {c.name}
                  </Link>
                )}
              </li>
            </Fragment>
          );
        })}
      </ol>
    </nav>
  );
}
