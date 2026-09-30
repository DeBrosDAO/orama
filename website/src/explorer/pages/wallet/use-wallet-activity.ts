import { useCallback, useEffect, useReducer, useRef } from "react";
import { useSource } from "../../data/provider";
import type { ActivityItem, WalletFilter } from "../../model/types";
import { activityReducer, startState, stateForKey } from "./activity-state";
import type { ActivityPhase } from "./activity-state";
import { ACTIVITY_PAGE_SIZE } from "./constants";

export interface WalletActivity {
  items: ActivityItem[];
  hasMore: boolean;
  phase: ActivityPhase;
  error: Error | null;
  /** Fetch the next page and append it. */
  loadMore: () => void;
  /** Try the failed request again. */
  retry: () => void;
}

const INITIAL = startState("");

/**
 * A wallet's activity, one page at a time, appended in order. Changing the
 * address, filter or counterparty starts again from the first page; a response
 * for an older request is dropped (see `activityReducer`).
 */
export function useWalletActivity(address: string, filter: WalletFilter, counterparty: string | null): WalletActivity {
  const source = useSource();
  const key = JSON.stringify([address, filter, counterparty]);
  const [state, dispatch] = useReducer(activityReducer, INITIAL);
  const lastRequest = useRef(0);
  const current = stateForKey(state, key);

  const fetchPage = useCallback(
    (cursor: string | null) => {
      const request = ++lastRequest.current;
      dispatch(cursor === null ? { type: "restart", key, request } : { type: "more", request });
      source
        .getWalletActivity(address, { filter, counterparty, cursor, limit: ACTIVITY_PAGE_SIZE })
        .then((page) => dispatch({ type: "page", request, cursor, page }))
        .catch((err: unknown) => {
          const error = err instanceof Error ? err : new Error(String(err));
          dispatch({ type: "failed", request, cursor, error });
        });
    },
    [source, address, filter, counterparty, key],
  );

  useEffect(() => {
    fetchPage(null);
  }, [fetchPage]);

  const loadMore = useCallback(() => {
    if (current.nextCursor !== null) fetchPage(current.nextCursor);
  }, [current.nextCursor, fetchPage]);

  const retry = useCallback(() => {
    if (current.items.length > 0) loadMore();
    else fetchPage(null);
  }, [current.items.length, loadMore, fetchPage]);

  return { items: current.items, hasMore: current.nextCursor !== null, phase: current.phase, error: current.error, loadMore, retry };
}
