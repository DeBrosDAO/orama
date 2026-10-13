import { useCallback, useState } from "react";
import { toError } from "../data/errors";
import { useSource } from "../data/provider";
import type { ExplorerDataSource } from "../data/source";
import { useQuery } from "../data/use-query";
import type { QueryState } from "../data/use-query";
import type { KeyPage, KeyQuery } from "../model/paging";
import { allItems, nextKeyOf, withPage } from "./key-pages";
import type { Appended } from "./key-pages";

/** Entries a list asks for at a time. */
export const KEY_PAGE_SIZE = 20;

export interface KeyPaged<T, Extra = unknown> {
  /** The first page's state; the list shows once it is ready. */
  first: QueryState<KeyPage<T> & Extra>;
  /** The first page and every page loaded after it. */
  items: T[];
  /** Whether the chain has more entries after the last one loaded. */
  hasMore: boolean;
  loadingMore: boolean;
  /** Why the last "show more" failed; cleared by the next try. */
  moreError: Error | null;
  loadMore: () => void;
  retry: () => void;
}

/**
 * A list read a page at a time with the key the chain gives. The first page is an ordinary query
 * (loading, error, ready); "show more" appends the page after the last one, and asking again for
 * the first page (a new `deps`, or a retry) starts the list over. `Extra` is what the first page
 * carries besides the entries (an account's first recorded epoch, say).
 */
export function useKeyPaged<T, Extra = unknown>(
  read: (source: ExplorerDataSource, query: KeyQuery) => Promise<KeyPage<T> & Extra>,
  deps: readonly unknown[],
  limit: number = KEY_PAGE_SIZE,
): KeyPaged<T, Extra> {
  const source = useSource();
  const { state, refetch } = useQuery((s) => read(s, { limit, key: null }), deps);
  const [appended, setAppended] = useState<Appended<T> | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<Error | null>(null);

  const first = state.status === "ready" ? state.data : null;
  const nextKey = nextKeyOf(first, appended);

  const loadMore = useCallback(() => {
    if (first === null || nextKey === null || loadingMore) return;
    setLoadingMore(true);
    setMoreError(null);
    read(source, { limit, key: nextKey }).then(
      (page) => {
        setAppended((prev) => withPage(first, prev, page));
        setLoadingMore(false);
      },
      (error: unknown) => {
        setMoreError(toError(error));
        setLoadingMore(false);
      },
    );
  }, [first, nextKey, loadingMore, read, source, limit]);

  const retry = useCallback(() => {
    setAppended(null);
    setMoreError(null);
    refetch();
  }, [refetch]);

  return { first: state, items: allItems(first, appended), hasMore: nextKey !== null, loadingMore, moreError, loadMore, retry };
}
