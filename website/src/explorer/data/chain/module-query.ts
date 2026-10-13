import type { KeyPage, KeyQuery } from "../../model/paging";
import { ChainNotFound, ChainReadError, withQuery } from "./client";
import type { ChainClient } from "./client";
import { arr, optStr, rec } from "./wire";

/** The most one page of a paginated query may ask for (core/pkg/gateway/handlers/chainread/query.go, queryMaxPageLimit). */
export const MAX_KEY_PAGE = 100;
const MAX_KEY_LENGTH = 1024;
const BASE64_KEY = /^[A-Za-z0-9+/_-]*={0,2}$/;

/** The proxy path of a module query, with its request as the `json` the gateway reads. */
export function queryPath(method: string, request?: Record<string, unknown>): string {
  const path = `query/${method}`;
  return request === undefined ? path : withQuery(path, { json: JSON.stringify(request) });
}

/** One module query; the answer is null when the chain has nothing under the request (a 404). */
export async function lookup(client: ChainClient, method: string, request?: Record<string, unknown>): Promise<unknown> {
  try {
    return await client.get(queryPath(method, request));
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

/** The `pagination` member of a request for one page. */
export function pagination(query: KeyQuery): { limit: number; key?: string } {
  const limit = Number.isFinite(query.limit) ? Math.min(MAX_KEY_PAGE, Math.max(1, Math.floor(query.limit))) : 1;
  return query.key === null ? { limit } : { limit, key: query.key };
}

/** Where the next page starts: the `next_key` of a response's `pagination`, or null on the last page. */
export function nextKeyOf(answer: Record<string, unknown>): string | null {
  const page = answer.pagination;
  if (page === undefined || page === null) return null;
  const key = optStr(rec(page, "page information").next_key, "next page key", MAX_KEY_LENGTH);
  if (key === "") return null;
  if (!BASE64_KEY.test(key)) throw new ChainReadError("The chain sent a malformed page key.");
  return key;
}

/** A page of the list a paginated answer holds under `field`, each entry read by `read`; entries it maps to null are left out. */
export function readKeyPage<T>(answer: unknown, field: string, what: string, read: (raw: unknown) => T | null): KeyPage<T> {
  const body = rec(answer, what);
  const items = (body[field] === undefined || body[field] === null ? [] : arr(body[field], what)).map(read);
  return { items: items.filter((item): item is T => item !== null), nextKey: nextKeyOf(body) };
}
