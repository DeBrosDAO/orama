import { describe, expect, it } from "vitest";
import { dealCounts, isRunning, oldestUnarchivedBounds, retainLag, unarchivedRangeCount } from "./storage";
import type { StorageDeal } from "./storage";

const deal = (over: Partial<StorageDeal> = {}): StorageDeal => ({
  id: 1,
  class: "public_pin",
  protocol: false,
  client: "orama1c",
  repairDelegate: null,
  replicas: 3,
  pricePerEpoch: "1",
  durationEpochs: 1,
  startEpoch: 1,
  endEpoch: 2,
  escrow: "1",
  status: "active",
  createdHeight: 1,
  ...over,
});

describe("retainLag", () => {
  it("TestRetainLag_is_how_far_the_archive_holds_the_retain_height_below_the_window", () => {
    expect(retainLag(1450, 400, 1000)).toBe(50);
    expect(retainLag(1450, 450, 1000)).toBe(0);
  });

  it("TestRetainLag_a_chain_younger_than_its_window_has_none", () => {
    expect(retainLag(500, 0, 1000)).toBe(0);
    expect(retainLag(1000, 0, 1000)).toBe(0);
  });
});

describe("unarchivedRangeCount", () => {
  it("TestUnarchivedRangeCount_counts_complete_ranges_between_the_prefix_and_the_tip", () => {
    expect(unarchivedRangeCount(1450, 400, 100)).toBe(10);
    expect(unarchivedRangeCount(1400, 1400, 100)).toBe(0);
    expect(unarchivedRangeCount(250, 0, 100)).toBe(2);
  });

  it("TestUnarchivedRangeCount_never_goes_below_zero_and_a_zero_width_has_none", () => {
    expect(unarchivedRangeCount(100, 500, 100)).toBe(0);
    expect(unarchivedRangeCount(1000, 0, 0)).toBe(0);
  });
});

describe("oldestUnarchivedBounds", () => {
  it("TestOldestUnarchivedBounds_are_the_canonical_ranges_after_the_prefix", () => {
    expect(oldestUnarchivedBounds(1450, 400, 100, 3)).toEqual([
      { start: 401, end: 500 },
      { start: 501, end: 600 },
      { start: 601, end: 700 },
    ]);
  });

  it("TestOldestUnarchivedBounds_stop_at_the_tip_and_at_the_count", () => {
    expect(oldestUnarchivedBounds(250, 0, 100, 5)).toEqual([
      { start: 1, end: 100 },
      { start: 101, end: 200 },
    ]);
    expect(oldestUnarchivedBounds(250, 0, 100, 0)).toEqual([]);
  });
});

describe("deals", () => {
  it("TestDealCounts_counts_each_class", () => {
    expect(dealCounts([deal(), deal({ class: "archive" }), deal({ class: "archive" })])).toEqual({ private: 0, public_pin: 1, archive: 2 });
    expect(dealCounts([])).toEqual({ private: 0, public_pin: 0, archive: 0 });
  });

  it("TestIsRunning_open_and_active_deals_run_and_expired_and_refunded_do_not", () => {
    expect(["open", "active"].map((status) => isRunning(deal({ status: status as StorageDeal["status"] })))).toEqual([true, true]);
    expect(["expired", "refunded"].map((status) => isRunning(deal({ status: status as StorageDeal["status"] })))).toEqual([false, false]);
  });
});
