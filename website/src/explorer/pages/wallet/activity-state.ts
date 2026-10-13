import type { ActivityItem, Page } from "../../model/types";

export type ActivityPhase = "loading" | "ready" | "loading-more" | "error";

export interface ActivityState {
  /** The (address, filter, counterparty) these rows answer; rows for another key are never shown. */
  key: string;
  items: ActivityItem[];
  nextCursor: string | null;
  phase: ActivityPhase;
  error: Error | null;
  /** The one request whose answer this state is waiting for; any other answer is stale. */
  request: number;
}

export type ActivityAction =
  | { type: "restart"; key: string; request: number }
  | { type: "more"; request: number }
  | { type: "page"; request: number; cursor: string | null; page: Page<ActivityItem> }
  | { type: "failed"; request: number; cursor: string | null; error: Error };

export function startState(key: string, request = 0): ActivityState {
  return { key, items: [], nextCursor: null, phase: "loading", error: null, request };
}

/** What to show for `key`: the loaded state when it answers `key`, otherwise a fresh start. */
export function stateForKey(state: ActivityState, key: string): ActivityState {
  return state.key === key ? state : startState(key);
}

/**
 * The transitions of a wallet's paged activity. "restart" begins again from
 * the first page; "more" asks for the next one. An answer whose request is no
 * longer the awaited one is dropped, so a slow old response can never
 * overwrite a newer query.
 */
export function activityReducer(state: ActivityState, action: ActivityAction): ActivityState {
  switch (action.type) {
    case "restart":
      return startState(action.key, action.request);
    case "more":
      return { ...state, phase: "loading-more", error: null, request: action.request };
    case "page":
      if (action.request !== state.request) return state;
      return {
        ...state,
        items: action.cursor === null ? action.page.items : [...state.items, ...action.page.items],
        nextCursor: action.page.nextCursor,
        phase: "ready",
        error: null,
      };
    case "failed":
      if (action.request !== state.request) return state;
      return { ...(action.cursor === null ? startState(state.key, state.request) : state), phase: "error", error: action.error };
  }
}
