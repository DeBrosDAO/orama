import { describe, expect, it } from "vitest";
import type { ActivityItem, Page } from "../../model/types";
import { activityReducer, startState, stateForKey } from "./activity-state";
import type { ActivityAction, ActivityState } from "./activity-state";
import { sendItem } from "./fixtures";

const item = (hash: string): ActivityItem => sendItem({ hash });
const page = (hashes: string[], nextCursor: string | null): Page<ActivityItem> => ({ items: hashes.map(item), nextCursor });
const run = (actions: ActivityAction[], from: ActivityState = startState("")) => actions.reduce(activityReducer, from);
const hashes = (s: ActivityState) => s.items.map((i) => i.hash);

describe("activityReducer", () => {
  it("TestActivityReducer_first_page_replaces_and_records_the_cursor", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1", "H2"], "c1") },
    ]);
    expect(hashes(s)).toEqual(["H1", "H2"]);
    expect(s).toMatchObject({ key: "a", nextCursor: "c1", phase: "ready", error: null });
  });

  it("TestActivityReducer_load_more_appends_in_order_and_ends_the_list", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], "c1") },
      { type: "more", request: 2 },
      { type: "page", request: 2, cursor: "c1", page: page(["H2", "H3"], null) },
    ]);
    expect(hashes(s)).toEqual(["H1", "H2", "H3"]);
    expect(s.nextCursor).toBeNull();
    expect(s.phase).toBe("ready");
  });

  it("TestActivityReducer_load_more_shows_loading_more_and_keeps_rows", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], "c1") },
      { type: "more", request: 2 },
    ]);
    expect(s.phase).toBe("loading-more");
    expect(hashes(s)).toEqual(["H1"]);
  });

  it("TestActivityReducer_a_new_key_restarts_from_nothing", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], "c1") },
      { type: "restart", key: "b", request: 2 },
    ]);
    expect(s).toEqual(startState("b", 2));
  });

  it("TestActivityReducer_a_stale_page_is_dropped", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "restart", key: "b", request: 2 },
      { type: "page", request: 1, cursor: null, page: page(["OLD"], null) },
    ]);
    expect(s).toEqual(startState("b", 2));
  });

  it("TestActivityReducer_a_stale_load_more_page_is_dropped_after_a_restart", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], "c1") },
      { type: "more", request: 2 },
      { type: "restart", key: "b", request: 3 },
      { type: "page", request: 2, cursor: "c1", page: page(["OLD"], null) },
    ]);
    expect(s.key).toBe("b");
    expect(s.items).toEqual([]);
  });

  it("TestActivityReducer_a_stale_failure_is_dropped", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "restart", key: "b", request: 2 },
      { type: "failed", request: 1, cursor: null, error: new Error("old") },
    ]);
    expect(s.phase).toBe("loading");
    expect(s.error).toBeNull();
  });

  it("TestActivityReducer_first_page_failure_is_an_error_with_no_rows", () => {
    const error = new Error("indexer down");
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "failed", request: 1, cursor: null, error },
    ]);
    expect(s).toMatchObject({ key: "a", items: [], nextCursor: null, phase: "error", error });
  });

  it("TestActivityReducer_load_more_failure_keeps_rows_and_the_cursor_for_a_retry", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], "c1") },
      { type: "more", request: 2 },
      { type: "failed", request: 2, cursor: "c1", error: new Error("boom") },
    ]);
    expect(hashes(s)).toEqual(["H1"]);
    expect(s).toMatchObject({ nextCursor: "c1", phase: "error" });
    expect(s.error?.message).toBe("boom");
  });

  it("TestActivityReducer_retry_after_an_error_clears_it_and_can_succeed", () => {
    const failed = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], "c1") },
      { type: "more", request: 2 },
      { type: "failed", request: 2, cursor: "c1", error: new Error("boom") },
    ]);
    const s = run(
      [
        { type: "more", request: 3 },
        { type: "page", request: 3, cursor: "c1", page: page(["H2"], null) },
      ],
      failed,
    );
    expect(hashes(s)).toEqual(["H1", "H2"]);
    expect(s).toMatchObject({ phase: "ready", error: null });
  });

  it("TestActivityReducer_an_empty_first_page_is_ready_with_no_rows", () => {
    const s = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page([], null) },
    ]);
    expect(s).toMatchObject({ items: [], phase: "ready", nextCursor: null });
  });
});

describe("stateForKey", () => {
  it("TestStateForKey_shows_the_loaded_state_for_its_own_key", () => {
    const loaded = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], null) },
    ]);
    expect(stateForKey(loaded, "a")).toBe(loaded);
  });

  it("TestStateForKey_never_shows_rows_of_another_key", () => {
    const loaded = run([
      { type: "restart", key: "a", request: 1 },
      { type: "page", request: 1, cursor: null, page: page(["H1"], null) },
    ]);
    const shown = stateForKey(loaded, "b");
    expect(shown.items).toEqual([]);
    expect(shown.phase).toBe("loading");
    expect(shown.key).toBe("b");
  });
});
