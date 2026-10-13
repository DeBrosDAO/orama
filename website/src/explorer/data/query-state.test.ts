import { describe, expect, it } from "vitest";
import { LOADING, resolveQuery, sameKey, settleQuery } from "./query-state";
import type { Stored } from "./query-state";

const SOURCE = { name: "source" };
const key = (...deps: unknown[]) => [SOURCE, ...deps];
const ready = <T>(k: readonly unknown[], data: T): Stored<T> => ({ key: k, state: { status: "ready", data, error: null } });

describe("sameKey", () => {
  it("TestSameKey_compares_each_part_by_identity", () => {
    expect(sameKey(key("a", 1), key("a", 1))).toBe(true);
    expect(sameKey(key("a"), key("b"))).toBe(false);
    expect(sameKey(key({}), key({}))).toBe(false);
    expect(sameKey(key(NaN), key(NaN))).toBe(true);
  });

  it("TestSameKey_a_different_source_or_length_is_a_different_query", () => {
    expect(sameKey([{ a: 1 }, "x"], [{ a: 1 }, "x"])).toBe(false);
    expect(sameKey(key("a"), key("a", undefined))).toBe(false);
  });
});

describe("resolveQuery", () => {
  it("TestResolveQuery_matching_key_returns_the_stored_state", () => {
    const stored = ready(key("a"), 42);
    expect(resolveQuery(stored, key("a"))).toBe(stored.state);
  });

  it("TestResolveQuery_changed_key_is_loading_not_the_old_answer", () => {
    expect(resolveQuery(ready(key("a"), 42), key("b"))).toBe(LOADING);
  });

  it("TestResolveQuery_nothing_stored_is_loading", () => {
    expect(resolveQuery(null, key())).toBe(LOADING);
  });
});

describe("settleQuery", () => {
  it("TestSettleQuery_the_latest_request_stores_its_data_under_its_key", () => {
    const next = settleQuery(null, key("a"), { id: 3, latest: 3 }, { data: "hello" });
    expect(next).toEqual({ key: key("a"), state: { status: "ready", data: "hello", error: null } });
  });

  it("TestSettleQuery_an_older_request_never_overwrites_a_newer_one", () => {
    const stored = ready(key("b"), "newer");
    expect(settleQuery(stored, key("a"), { id: 1, latest: 2 }, { data: "older" })).toBe(stored);
    expect(settleQuery(null, key("a"), { id: 1, latest: 2 }, { error: new Error("late") })).toBeNull();
  });

  it("TestSettleQuery_an_error_stays_an_Error_of_the_same_type", () => {
    const boom = new TypeError("indexer down");
    const next = settleQuery(null, key("a"), { id: 1, latest: 1 }, { error: boom });
    expect(next?.state).toEqual({ status: "error", data: null, error: boom });
    expect(next?.state.error).toBe(boom);
  });

  it("TestSettleQuery_a_thrown_string_becomes_an_Error", () => {
    const next = settleQuery(null, key("a"), { id: 1, latest: 1 }, { error: "nope" });
    expect(next?.state.error).toBeInstanceOf(Error);
    expect(next?.state.error?.message).toBe("nope");
  });

  it("TestSettleQuery_a_refetch_replaces_the_data_under_the_same_key", () => {
    const next = settleQuery(ready(key("a"), 1), key("a"), { id: 2, latest: 2 }, { data: 2 });
    expect(resolveQuery(next, key("a"))).toEqual({ status: "ready", data: 2, error: null });
  });
});
