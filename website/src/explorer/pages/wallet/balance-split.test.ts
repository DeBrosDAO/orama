import { describe, expect, it } from "vitest";
import { MIN_SEGMENT_WIDTH, splitSegments } from "./balance-split";

const bal = (available: string, staked: string, unbonding: string) => ({
  available,
  staked,
  unbonding,
  claimableRewards: "999",
  total: "0",
});

describe("splitSegments", () => {
  it("TestSplitSegments_shares_add_up", () => {
    const s = splitSegments(bal("705", "293", "2"));
    expect(s.map((x) => x.percent)).toEqual([70.5, 29.3, 0.2]);
  });

  it("TestSplitSegments_zero_total_has_zero_shares", () => {
    expect(splitSegments(bal("0", "0", "0")).every((x) => x.percent === 0)).toBe(true);
  });

  it("TestSplitSegments_ignores_claimable_rewards", () => {
    const s = splitSegments(bal("100", "0", "0"));
    expect(s[0]?.percent).toBe(100);
    expect(s.map((x) => x.id)).toEqual(["available", "staked", "unbonding"]);
  });

  it("TestSplitSegments_rejects_a_bad_amount", () => {
    expect(() => splitSegments(bal("1.5", "0", "0"))).toThrow();
  });

  it("TestSplitSegments_a_tiny_non_zero_part_stays_visible", () => {
    const s = splitSegments(bal("1000000000000", "0", "1"));
    expect(s[2]?.percent).toBe(0);
    expect(s[2]?.width).toBe(MIN_SEGMENT_WIDTH);
    expect(s[1]?.width).toBe(0);
  });

  it("TestSplitSegments_a_large_part_keeps_its_true_width", () => {
    expect(splitSegments(bal("705", "293", "2"))[0]?.width).toBe(70.5);
  });
});
