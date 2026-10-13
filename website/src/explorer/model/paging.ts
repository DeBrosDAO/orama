/**
 * The paging of the cosmos-sdk and wasmd queries: a page is asked for with a limit and the key the
 * last page ended at, and the answer says where the next one starts. A key is opaque to the page
 * that holds it. The indexer's long lists (blocks, transactions, an owner's assets, an account's
 * rewards) are paged the same way, by the cursor they name; its short ones by number (IndexPage, epochs.ts).
 */
export interface KeyQuery {
  /** 1 to 100, the most the gateway serves in one page. */
  limit: number;
  /** Where the last page ended; null for the first page. */
  key: string | null;
}

export interface KeyPage<T> {
  items: T[];
  /** Pass back as `key` for the next page; null when this was the last one. */
  nextKey: string | null;
}

/** A whole number written with thousands separators, for counts and token units that have no decimals. */
export function groupedInteger(digits: string): string {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/**
 * A page of records the chain numbers 1, 2, 3 … and never deletes (collections, proposals), newest
 * first. The chain has no list of them, so the newest id is found by asking, and a page is the ids
 * below `from` read one by one.
 */
export interface IdPage<T> {
  /** The newest id the chain has; 0 when there is none. */
  newest: number;
  items: T[];
  /** The id the next older page starts at; null when this page reaches id 1. */
  next: number | null;
}
