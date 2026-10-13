import { Link, useSearchParams } from "react-router";

const LINK = "rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30";

/** The query that shows the page of an id-numbered list starting at `from`; null is the newest page, the bare URL. */
export function fromSearch(current: URLSearchParams, param: string, from: number | null): string {
  const next = new URLSearchParams(current);
  if (from === null) next.delete(param);
  else next.set(param, String(from));
  const text = next.toString();
  return text === "" ? "" : `?${text}`;
}

/**
 * Newest and older links for a list the chain numbers by id (collections, proposals). The page
 * lives in the URL (`?from=12`), so it can be shared; `next` is the id the older page starts at.
 */
export function IdPager({ param, atNewest, next }: { param: string; atNewest: boolean; next: number | null }) {
  const [params] = useSearchParams();
  if (atNewest && next === null) return null;
  return (
    <nav aria-label="Pages" className="flex items-center justify-between gap-3 pt-3">
      {atNewest ? (
        <span />
      ) : (
        <Link to={{ search: fromSearch(params, param, null) }} className={LINK} rel="first">
          Newest
        </Link>
      )}
      {next !== null && (
        <Link to={{ search: fromSearch(params, param, next) }} className={LINK} rel="next">
          Older
        </Link>
      )}
    </nav>
  );
}
