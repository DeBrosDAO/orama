import { Link, useSearchParams } from "react-router";
import { pageSearch } from "./logic";

const LINK = "rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30";
const OFF = "rounded-lg border border-border px-3 py-1.5 text-sm text-muted opacity-50";

/**
 * Newer and older links for a list the indexer pages through. The page lives in the URL
 * (`?page=2`), so a page can be shared. The indexer does not count rows, so "older" is offered
 * whenever this page was full; the next page may then be empty and says so.
 */
export function Pager({ param, page, hasMore }: { param: string; page: number; hasMore: boolean }) {
  const [params] = useSearchParams();
  if (page === 1 && !hasMore) return null;
  return (
    <nav aria-label="Pages" className="flex items-center justify-between gap-3 pt-3">
      {page > 1 ? (
        <Link to={{ search: pageSearch(params, param, page - 1) }} className={LINK} rel="prev">
          Newer
        </Link>
      ) : (
        <span className={OFF} aria-disabled="true">
          Newer
        </span>
      )}
      <span className="text-xs text-muted tabular-nums">Page {page}</span>
      {hasMore ? (
        <Link to={{ search: pageSearch(params, param, page + 1) }} className={LINK} rel="next">
          Older
        </Link>
      ) : (
        <span className={OFF} aria-disabled="true">
          Older
        </span>
      )}
    </nav>
  );
}
