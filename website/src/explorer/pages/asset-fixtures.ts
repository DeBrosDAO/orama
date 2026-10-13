import { renderToStaticMarkup } from "react-dom/server";
import type { ReactNode } from "react";
import { createElement } from "react";
import { MemoryRouter } from "react-router";
import type { QueryState } from "../data/query-state";
import { LOADING } from "../data/query-state";
import type { KeyPage } from "../model/paging";
import type { KeyPaged } from "../ui/use-key-paged";

/** Test fixtures shared by the asset pages' tests. */
export const render = (node: ReactNode, url = "/") => renderToStaticMarkup(createElement(MemoryRouter, { initialEntries: [url] }, node));

export const ready = <T>(data: T): QueryState<T> => ({ status: "ready", data, error: null });
export const failed = <T>(message: string): QueryState<T> => ({ status: "error", data: null, error: new Error(message) });

/** A key-paged list in a given state: ready with items, loading, or failed. */
export function keyPaged<T>(first: QueryState<KeyPage<T>>, over: Partial<KeyPaged<T>> = {}): KeyPaged<T> {
  const items = first.status === "ready" ? first.data.items : [];
  return {
    first,
    items,
    hasMore: first.status === "ready" && first.data.nextKey !== null,
    loadingMore: false,
    moreError: null,
    loadMore: () => undefined,
    retry: () => undefined,
    ...over,
  };
}

export const readyPaged = <T>(items: T[], nextKey: string | null = null) => keyPaged(ready<KeyPage<T>>({ items, nextKey }));
export const loadingPaged = <T>() => keyPaged<T>(LOADING);
export const failedPaged = <T>(message: string) => keyPaged<T>(failed<KeyPage<T>>(message));
