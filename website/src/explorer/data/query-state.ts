import { toError } from "./errors";

export type QueryState<T> =
  | { status: "loading"; data: null; error: null }
  | { status: "ready"; data: T; error: null }
  | { status: "error"; data: null; error: Error };

export const LOADING = { status: "loading", data: null, error: null } as const;

/** What a query last settled to, and the identity (source and deps) it answered. */
export interface Stored<T> {
  key: readonly unknown[];
  state: QueryState<T>;
}

export type Outcome<T> = { data: T } | { error: unknown };

/** Two keys are the same query when every part is the same value. */
export function sameKey(a: readonly unknown[], b: readonly unknown[]): boolean {
  return a.length === b.length && a.every((part, i) => Object.is(part, b[i]));
}

/**
 * The state to show for the query `key` names now. A stored answer belongs to
 * the key it was fetched for; once the key changes it is not shown, not even
 * for the one render before the effect that starts the new fetch runs.
 */
export function resolveQuery<T>(stored: Stored<T> | null, key: readonly unknown[]): QueryState<T> {
  return stored !== null && sameKey(stored.key, key) ? stored.state : LOADING;
}

/**
 * Fold a finished request into the stored answer. Only the newest request may
 * write: an older one that finishes late leaves `stored` untouched.
 */
export function settleQuery<T>(
  stored: Stored<T> | null,
  key: readonly unknown[],
  request: { id: number; latest: number },
  outcome: Outcome<T>,
): Stored<T> | null {
  if (request.id !== request.latest) return stored;
  if ("error" in outcome) return { key, state: { status: "error", data: null, error: toError(outcome.error) } };
  return { key, state: { status: "ready", data: outcome.data, error: null } };
}
