import { describe, expect, it } from "vitest";
import type { EpochRow, SupplyPoint } from "../../model/epochs";
import { composition, compositionAddsUp, epochSeries, permilleText, supplySeries, supplySlots, supplySpan } from "./model";

const ORAMA = 1_000_000_000n;
const o = (n: bigint) => (n * ORAMA).toString();

function point(epoch: number, over: Partial<SupplyPoint> = {}): SupplyPoint {
  return {
    epoch,
    height: epoch * 100,
    time: "2026-10-09T00:00:00.000Z",
    totalSupply: o(1_000n),
    mintedToDate: o(100n),
    burnedToDate: "42",
    genesisSupply: o(900n),
    bonded: o(400n),
    unbonding: o(10n),
    earningsPools: o(20n),
    storageEscrow: o(30n),
    otherProtocol: o(40n),
    circulating: o(500n),
    ...over,
  };
}

describe("composition", () => {
  it("TestComposition_six_slices_that_add_up_to_the_total_with_exact_shares", () => {
    const slices = composition(point(3));
    expect(slices.map((s) => s.key)).toEqual(["circulating", "bonded", "unbonding", "earningsPools", "storageEscrow", "otherProtocol"]);
    expect(slices.map((s) => s.permille)).toEqual([500, 400, 10, 20, 30, 40]);
    expect(compositionAddsUp(point(3))).toBe(true);
  });

  it("TestComposition_a_breakdown_that_does_not_add_up_is_detected_not_hidden", () => {
    expect(compositionAddsUp(point(3, { circulating: o(499n) }))).toBe(false);
  });

  it("TestComposition_zero_supply_has_zero_shares", () => {
    const empty = point(1, { totalSupply: "0", circulating: "0", bonded: "0", unbonding: "0", earningsPools: "0", storageEscrow: "0", otherProtocol: "0" });
    expect(composition(empty).every((s) => s.permille === 0)).toBe(true);
    expect(compositionAddsUp(empty)).toBe(true);
  });

  it("TestComposition_is_exact_for_a_supply_beyond_a_double", () => {
    const big = 10n ** 30n;
    const p = point(1, { totalSupply: big.toString(), circulating: (big / 4n).toString(), bonded: ((big * 3n) / 4n).toString(), unbonding: "0", earningsPools: "0", storageEscrow: "0", otherProtocol: "0" });
    expect(composition(p).map((s) => s.permille).slice(0, 2)).toEqual([250, 750]);
    expect(compositionAddsUp(p)).toBe(true);
  });

  it("TestComposition_a_tiny_slice_truncates_to_zero_permille", () => {
    expect(composition(point(1, { unbonding: "1" }))[2]?.permille).toBe(0);
  });
});

describe("series", () => {
  it("TestSupplySeries_runs_oldest_first_though_the_index_answers_newest_first", () => {
    const newestFirst = [point(3, { totalSupply: "30" }), point(2, { totalSupply: "20" }), point(1, { totalSupply: "10" })];
    expect(supplySeries(newestFirst, (p) => p.totalSupply)).toEqual([10n, 20n, 30n]);
    expect(newestFirst[0]?.epoch).toBe(3);
  });

  it("TestEpochSeries_runs_oldest_first", () => {
    const rows = [{ epoch: 2, unavailable: false, burned: "7" }, { epoch: 1, unavailable: false, burned: "5" }] as EpochRow[];
    expect(epochSeries(rows, (r) => r.burned)).toEqual([5n, 7n]);
  });

  it("TestEpochSeries_an_unavailable_epoch_is_a_null_gap_never_zero", () => {
    const rows = [
      { epoch: 3, unavailable: false, burned: "7" },
      { epoch: 2, unavailable: true, reason: "pruned" },
      { epoch: 1, unavailable: false, burned: "0" },
    ] as EpochRow[];
    expect(epochSeries(rows, (r) => r.burned)).toEqual([0n, null, 7n]);
  });

  it("TestSupplySeries_epochs_missing_between_points_are_one_null_gap_per_run", () => {
    const points = [point(9, { totalSupply: "90" }), point(5, { totalSupply: "50" }), point(4, { totalSupply: "40" }), point(1, { totalSupply: "10" })];
    expect(supplySeries(points, (p) => p.totalSupply)).toEqual([10n, null, 40n, 50n, null, 90n]);
  });

  it("TestSupplySlots_names_the_epochs_a_gap_stands_for", () => {
    const slots = supplySlots([point(5), point(2)]);
    expect(slots).toHaveLength(3);
    expect(slots[1]).toEqual({ gapFrom: 3, gapTo: 4 });
  });

  it("TestSupplySlots_contiguous_epochs_have_no_gap", () => {
    expect(supplySlots([point(3), point(2), point(1)]).every((s) => "point" in s)).toBe(true);
  });

  it("TestSeries_no_points_is_an_empty_series", () => {
    expect(supplySeries([], (p) => p.totalSupply)).toEqual([]);
    expect(supplySpan([])).toBeNull();
  });

  it("TestSupplySpan_is_the_oldest_and_newest_epoch", () => {
    expect(supplySpan([point(9), point(8), point(7)])).toEqual({ first: 7, last: 9 });
    expect(supplySpan([point(4)])).toEqual({ first: 4, last: 4 });
  });
});

describe("permilleText", () => {
  it("TestPermilleText_one_decimal", () => {
    expect(permilleText(0)).toBe("0.0%");
    expect(permilleText(34)).toBe("3.4%");
    expect(permilleText(1000)).toBe("100.0%");
  });
});
