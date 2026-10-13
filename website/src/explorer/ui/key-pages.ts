import type { KeyPage } from "../model/paging";

/** What was appended after a list's first page. It belongs to that first page: another first page drops it. */
export interface Appended<T> {
  first: KeyPage<T>;
  items: T[];
  nextKey: string | null;
}

/** The part of `appended` that belongs to `first`: null for a list that was started over, since its appended pages answered another first page. */
export function ownedBy<T>(first: KeyPage<T> | null, appended: Appended<T> | null): Appended<T> | null {
  return first !== null && appended !== null && appended.first === first ? appended : null;
}

/** Every item loaded so far, the first page's and then each appended page's. */
export function allItems<T>(first: KeyPage<T> | null, appended: Appended<T> | null): T[] {
  if (first === null) return [];
  return [...first.items, ...(ownedBy(first, appended)?.items ?? [])];
}

/** Where the next page starts: after the last appended page, or after the first when none was appended; null when the chain has no more. */
export function nextKeyOf<T>(first: KeyPage<T> | null, appended: Appended<T> | null): string | null {
  const mine = ownedBy(first, appended);
  return mine !== null ? mine.nextKey : (first?.nextKey ?? null);
}

/** `appended` with one more page after what it already holds. */
export function withPage<T>(first: KeyPage<T>, appended: Appended<T> | null, page: KeyPage<T>): Appended<T> {
  return { first, items: [...(ownedBy(first, appended)?.items ?? []), ...page.items], nextKey: page.nextKey };
}
