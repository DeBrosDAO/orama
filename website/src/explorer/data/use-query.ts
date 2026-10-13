import { useCallback, useEffect, useRef, useState } from "react";
import { useHead, useSource } from "./provider";
import { resolveQuery, settleQuery } from "./query-state";
import type { Outcome, QueryState, Stored } from "./query-state";
import type { ExplorerDataSource } from "./source";

export type { QueryState };

export interface UseQueryResult<T> {
  state: QueryState<T>;
  /** Fetch again without going back to the loading state. */
  refetch: () => void;
}

/**
 * Run one read against the data source. `deps` is the query's identity: the
 * moment it changes the state is loading (never the previous query's answer)
 * and a fetch starts. A newer request always wins over a slower older one. A
 * background refetch keeps showing the old data until the new data arrives,
 * so a live page never flashes.
 */
export function useQuery<T>(
  read: (source: ExplorerDataSource) => Promise<T>,
  deps: readonly unknown[],
): UseQueryResult<T> {
  const source = useSource();
  const [stored, setStored] = useState<Stored<T> | null>(null);
  const [tick, setTick] = useState(0);
  const latest = useRef(0);
  const readRef = useRef(read);
  readRef.current = read;
  const key = [source, ...deps];

  useEffect(() => {
    const id = ++latest.current;
    const settle = (outcome: Outcome<T>) => {
      const request = { id, latest: latest.current };
      setStored((prev) => settleQuery(prev, key, request, outcome));
    };
    readRef.current(source).then(
      (data) => settle({ data }),
      (error: unknown) => settle({ error }),
    );
  }, [source, tick, ...deps]);

  const refetch = useCallback(() => setTick((n) => n + 1), []);
  return { state: resolveQuery(stored, key), refetch };
}

/**
 * useQuery that also refetches whenever the chain head advances, keeping the
 * old data on screen until the new data lands. For pages that show "now".
 */
export function useLiveQuery<T>(
  read: (source: ExplorerDataSource) => Promise<T>,
  deps: readonly unknown[],
): UseQueryResult<T> {
  const result = useQuery(read, deps);
  const height = useHead()?.height;
  const seen = useRef<number | undefined>(undefined);
  const { refetch } = result;

  useEffect(() => {
    if (height === undefined) return;
    if (seen.current !== undefined && seen.current !== height) refetch();
    seen.current = height;
  }, [height, refetch]);

  return result;
}
