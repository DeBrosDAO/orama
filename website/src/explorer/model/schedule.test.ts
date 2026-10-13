import { describe, expect, it } from "vitest";
import type { EpochClock } from "./epochs";
import {
  bracketOf,
  brackets,
  cumulativeCeiling,
  earliestStartMs,
  epochCountdown,
  FINAL_BRACKET_EPOCH,
  formatSpan,
  maxMintableAt,
  nextCeilingChange,
  splitCeiling,
} from "./schedule";
import { NORAMA_PER_ORAMA } from "./units";

const orama = (n: bigint) => n * NORAMA_PER_ORAMA;

describe("maxMintableAt", () => {
  it.each([
    [0, 0n],
    [-3, 0n],
    [1, 14_848n],
    [730, 14_848n],
    [731, 7_424n],
    [1_460, 7_424n],
    [1_461, 3_712n],
    [2_190, 3_712n],
    [2_191, 1_856n],
    [2_920, 1_856n],
    [2_921, 928n],
    [3_650, 928n],
    [3_651, 274n],
    [1_000_000, 274n],
  ])("TestMaxMintableAt_epoch_%i", (epoch, expected) => {
    expect(maxMintableAt(epoch)).toBe(orama(expected));
  });

  it("TestMaxMintableAt_refuses_a_fraction_or_NaN_with_zero", () => {
    expect(maxMintableAt(1.5)).toBe(0n);
    expect(maxMintableAt(Number.NaN)).toBe(0n);
  });
});

describe("cumulativeCeiling", () => {
  it("TestCumulativeCeiling_matches_the_documented_totals", () => {
    expect(cumulativeCeiling(0)).toBe(0n);
    expect(cumulativeCeiling(1)).toBe(orama(14_848n));
    expect(cumulativeCeiling(730)).toBe(orama(10_839_040n));
    expect(cumulativeCeiling(731)).toBe(orama(10_839_040n + 7_424n));
    expect(cumulativeCeiling(FINAL_BRACKET_EPOCH)).toBe(orama(21_000_640n));
    expect(cumulativeCeiling(FINAL_BRACKET_EPOCH + 2)).toBe(orama(21_000_640n + 2n * 274n));
  });

  it("TestCumulativeCeiling_equals_the_sum_of_every_epochs_ceiling", () => {
    let sum = 0n;
    for (let e = 1; e <= 3_700; e++) {
      sum += maxMintableAt(e);
      if (e % 331 === 0 || e === 730 || e === 731 || e === 3_650 || e === 3_651) expect(cumulativeCeiling(e)).toBe(sum);
    }
  });
});

describe("splitCeiling", () => {
  it("TestSplitCeiling_is_60_25_10_5_and_sums_to_the_ceiling", () => {
    const total = maxMintableAt(1);
    const s = splitCeiling(total);
    expect(s.validators).toBe(orama(8_908n) + 800_000_000n);
    expect(s.storage).toBe(orama(3_712n));
    expect(s.relay).toBe(orama(1_484n) + 800_000_000n);
    expect(s.development).toBe(orama(742n) + 400_000_000n);
    expect(s.validators + s.storage + s.relay + s.development).toBe(total);
  });

  it("TestSplitCeiling_the_validator_share_takes_the_rounding_remainder", () => {
    const s = splitCeiling(1_001n);
    expect(s).toEqual({ storage: 250n, relay: 100n, development: 50n, validators: 601n });
    expect(splitCeiling(0n)).toEqual({ validators: 0n, storage: 0n, relay: 0n, development: 0n });
  });
});

describe("brackets", () => {
  it("TestBrackets_are_five_halving_rows_and_a_tail", () => {
    const rows = brackets();
    expect(rows).toHaveLength(6);
    expect(rows.map((r) => r.ceiling / NORAMA_PER_ORAMA)).toEqual([14_848n, 7_424n, 3_712n, 1_856n, 928n, 274n]);
    expect(rows[0]).toMatchObject({ firstEpoch: 1, lastEpoch: 730 });
    expect(rows[4]).toMatchObject({ firstEpoch: 2_921, lastEpoch: 3_650 });
    expect(rows[5]).toMatchObject({ firstEpoch: 3_651, lastEpoch: null });
  });

  it("TestBracketOf_the_boundaries", () => {
    expect(bracketOf(730).number).toBe(1);
    expect(bracketOf(731).number).toBe(2);
    expect(bracketOf(3_650).number).toBe(5);
    expect(bracketOf(3_651).number).toBe(6);
    expect(bracketOf(900_000).number).toBe(6);
  });
});

describe("nextCeilingChange", () => {
  it("TestNextCeilingChange_in_the_middle_of_the_first_bracket", () => {
    expect(nextCeilingChange(100)).toEqual({ firstEpoch: 731, epochsLeft: 631, from: orama(14_848n), to: orama(7_424n), halving: true });
  });

  it("TestNextCeilingChange_epoch_730_is_the_last_one_before_the_halving", () => {
    expect(nextCeilingChange(730)).toMatchObject({ firstEpoch: 731, epochsLeft: 1, halving: true });
    expect(nextCeilingChange(731)).toMatchObject({ firstEpoch: 1_461, epochsLeft: 730, from: orama(7_424n), to: orama(3_712n) });
  });

  it("TestNextCeilingChange_the_step_to_the_tail_is_not_a_halving", () => {
    const change = nextCeilingChange(3_650);
    expect(change).toMatchObject({ firstEpoch: 3_651, epochsLeft: 1, from: orama(928n), to: orama(274n), halving: false });
  });

  it("TestNextCeilingChange_none_once_the_tail_applies_or_for_nonsense", () => {
    expect(nextCeilingChange(3_651)).toBeNull();
    expect(nextCeilingChange(0)).toBeNull();
    expect(nextCeilingChange(Number.NaN)).toBeNull();
  });
});

const T0 = "2026-10-08T00:00:00Z";
const T0_MS = Date.parse(T0);
const clock = (over: Partial<EpochClock> = {}): EpochClock => ({
  epoch: 12,
  startedAt: T0,
  durationSeconds: 86_400,
  minBlocks: 14_400,
  blocksInEpoch: 3_600,
  readAtHeight: 5_000,
  ...over,
});

describe("epochCountdown", () => {
  it("TestEpochCountdown_a_quarter_in_on_both_conditions", () => {
    const c = epochCountdown(clock(), T0_MS + 21_600_000, 5_000);
    expect(c).toMatchObject({ timeLeftMs: 64_800_000, blocksLeft: 10_800, blocksSoFar: 3_600, due: false });
    expect(c.progress).toBeCloseTo(0.25);
  });

  it("TestEpochCountdown_blocks_made_since_the_read_count", () => {
    const c = epochCountdown(clock(), T0_MS, 5_400);
    expect(c.blocksSoFar).toBe(4_000);
    expect(c.blocksLeft).toBe(10_400);
  });

  it("TestEpochCountdown_a_head_behind_the_read_does_not_take_blocks_away", () => {
    expect(epochCountdown(clock(), T0_MS, 4_000).blocksSoFar).toBe(3_600);
    expect(epochCountdown(clock(), T0_MS, null).blocksSoFar).toBe(3_600);
  });

  it("TestEpochCountdown_the_slower_condition_sets_the_progress", () => {
    const timeDone = epochCountdown(clock(), T0_MS + 90_000_000, 5_000);
    expect(timeDone.timeLeftMs).toBe(0);
    expect(timeDone.due).toBe(false);
    expect(timeDone.progress).toBeCloseTo(0.25);
    const blocksDone = epochCountdown(clock({ blocksInEpoch: 20_000 }), T0_MS + 1_000, 5_000);
    expect(blocksDone.blocksLeft).toBe(0);
    expect(blocksDone.progress).toBeLessThan(0.01);
  });

  it("TestEpochCountdown_due_when_both_hold", () => {
    const c = epochCountdown(clock({ blocksInEpoch: 14_400 }), T0_MS + 86_400_000, 5_000);
    expect(c).toMatchObject({ timeLeftMs: 0, blocksLeft: 0, due: true, progress: 1 });
  });

  it("TestEpochCountdown_a_future_start_never_goes_negative", () => {
    const c = epochCountdown(clock(), T0_MS - 10_000, 5_000);
    expect(c.progress).toBe(0);
    expect(c.timeLeftMs).toBe(86_400_000 + 10_000);
  });
});

describe("earliestStartMs", () => {
  it("TestEarliestStart_counts_one_duration_per_epoch_left", () => {
    expect(earliestStartMs(clock({ durationSeconds: 300 }), 1)).toBe(T0_MS + 300_000);
    expect(earliestStartMs(clock({ durationSeconds: 300 }), 631)).toBe(T0_MS + 631 * 300_000);
  });
});

describe("formatSpan", () => {
  it.each([
    [0, "now"],
    [-5, "now"],
    [4_000, "4 s"],
    [250_000, "4 m 10 s"],
    [33_720_000, "9 h 22 m"],
    [12 * 86_400_000 + 4 * 3_600_000, "12 d 4 h"],
    [(365 * 2 + 41) * 86_400_000, "2 y 41 d"],
  ])("TestFormatSpan_%i", (ms, text) => {
    expect(formatSpan(ms)).toBe(text);
  });
});
