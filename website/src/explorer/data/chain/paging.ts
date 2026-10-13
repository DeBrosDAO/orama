import { ChainNotFound, ChainReadError, withQuery } from "./client";
import type { ChainClient } from "./client";
import { optArr, optStr, rec } from "./wire";

/** The shape of an indexer cursor of the block and transaction lists (chain/indexer/cursor.go): 16 characters of the URL-safe alphabet. */
const INDEX_CURSOR = /^[A-Za-z0-9_-]{16}$/;
/** The shape of the cursor of an owner's assets (chain/indexer/cursor_keys.go): the 44 bytes of an asset's location, 59 characters. */
export const ASSET_CURSOR = /^[A-Za-z0-9_-]{59}$/;
/** The shape of the cursor of an account's rewards (chain/indexer/cursor_keys.go): an epoch and a validator operator address, 39 to 580 characters. */
export const REWARD_CURSOR = /^[A-Za-z0-9_-]{39,580}$/;
/** The longest cursor any list names (REWARD_CURSOR). */
const MAX_CURSOR = 580;

/**
 * The indexer's cursor for the page after the one just read. A full page names it (`next_cursor`) and
 * a short page is the last, so there is none. A full page without one means a gateway whose indexer
 * pages by number only, which the explorer does not fall back to: that would skip rows.
 */
export function nextCursor(answer: Record<string, unknown>, rows: number, limit: number, shape: RegExp = INDEX_CURSOR): string | null {
  if (rows < limit) return null;
  const next = optStr(answer.next_cursor, "next page cursor", MAX_CURSOR);
  if (!shape.test(next)) throw new ChainReadError("This gateway's chain indexer pages its lists by number only; the explorer needs its cursor.");
  return next;
}

/** The most entries one list query answers: the gateway refuses a larger page (core/pkg/gateway/handlers/chainread, queryMaxPageLimit). */
const LIST_PAGE_SIZE = 100;

export interface Listed<T> {
  items: T[];
  /** True when the chain has more entries than the pages read. */
  more: boolean;
}

interface ListOptions {
  /** The most pages read; the rest is reported as `more`. */
  maxPages: number;
  /** Newest first, for a list keyed by an increasing id. */
  reverse?: boolean;
  /** Fields of the request besides its pagination, for a list that is asked about one thing (an epoch's payouts). */
  request?: Record<string, unknown>;
}

/** One page. A 404 on a list query is a gateway that does not serve it (an older version), not an empty list. */
async function getPage(
  client: ChainClient,
  method: string,
  field: string,
  pagination: Record<string, unknown>,
  request: Record<string, unknown>,
): Promise<unknown> {
  try {
    return await client.get(withQuery(method, { json: JSON.stringify({ ...request, pagination }) }));
  } catch (err) {
    if (err instanceof ChainNotFound) throw new ChainReadError(`This gateway does not serve the list of ${field} yet; it runs an older version.`);
    throw err;
  }
}

/**
 * Reads a paged list query of an Orama module (Nodes, Deals, Relays) by following the page key
 * each answer carries. `field` is the answer's list; `read` checks one entry.
 */
export async function listAll<T>(
  client: ChainClient,
  method: string,
  field: string,
  read: (raw: unknown) => T,
  options: ListOptions,
): Promise<Listed<T>> {
  const items: T[] = [];
  let key = "";
  for (let page = 0; page < options.maxPages; page++) {
    const pagination: Record<string, unknown> = { limit: LIST_PAGE_SIZE };
    if (key !== "") pagination.key = key;
    if (options.reverse) pagination.reverse = true;
    const body = rec(await getPage(client, method, field, pagination, options.request ?? {}), method);
    items.push(...optArr(body[field], field).map(read));
    key = body.pagination === null || body.pagination === undefined ? "" : optStr(rec(body.pagination, "page").next_key, "page key");
    if (key === "") return { items, more: false };
  }
  return { items, more: true };
}
