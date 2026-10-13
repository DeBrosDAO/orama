import { describe, expect, it } from "vitest";
import { bars, extent, linePoints, position, stepPoints } from "./chart";

const SIZE = { width: 100, height: 50 };

describe("extent", () => {
  it("TestExtent_a_line_fits_its_values_and_bars_start_at_zero", () => {
    expect(extent([5n, 9n, 7n], false)).toEqual({ min: 5n, max: 9n });
    expect(extent([5n, 9n, 7n], true)).toEqual({ min: 0n, max: 9n });
  });

  it("TestExtent_no_values_is_an_empty_range", () => {
    expect(extent([], false)).toEqual({ min: 0n, max: 0n });
  });
});

describe("position", () => {
  it("TestPosition_is_exact_for_amounts_beyond_a_double", () => {
    const base = 10n ** 24n;
    const range = { min: base, max: base + 4n };
    expect(position(base + 1n, range, false)).toBe(0.25);
    expect(position(base + 3n, range, false)).toBe(0.75);
  });

  it("TestPosition_a_flat_series_sits_in_the_middle_of_a_line_and_on_the_floor_of_bars", () => {
    expect(position(7n, { min: 7n, max: 7n }, false)).toBe(0.5);
    expect(position(0n, { min: 0n, max: 0n }, true)).toBe(0);
  });

  it("TestPosition_a_value_outside_the_range_is_held_to_its_edge", () => {
    expect(position(99n, { min: 0n, max: 10n }, true)).toBe(1);
    expect(position(0n, { min: 5n, max: 10n }, false)).toBe(0);
  });
});

describe("linePoints", () => {
  it("TestLinePoints_runs_edge_to_edge_with_larger_values_higher", () => {
    expect(linePoints([0n, 5n, 10n], SIZE, { min: 0n, max: 10n })).toEqual(["0.0,50.0 50.0,25.0 100.0,0.0"]);
  });

  it("TestLinePoints_a_single_value_is_a_dot_and_no_values_is_nothing", () => {
    expect(linePoints([3n], SIZE, { min: 3n, max: 3n })).toEqual(["0.0,25.0 0.0,25.0"]);
    expect(linePoints([], SIZE, { min: 0n, max: 0n })).toEqual([]);
  });

  it("TestLinePoints_a_gap_breaks_the_line_and_keeps_its_slot_instead_of_drawing_zero", () => {
    const out = linePoints([0n, null, 10n, 10n], SIZE, { min: 0n, max: 10n });
    expect(out).toEqual(["0.0,50.0 0.0,50.0", "66.7,0.0 100.0,0.0"]);
  });
});

describe("bars", () => {
  it("TestBars_one_per_value_scaled_to_the_range", () => {
    const out = bars([0n, 5n, 10n], SIZE, { min: 0n, max: 10n });
    expect(out).toHaveLength(3);
    expect(out.map((b) => b.height)).toEqual([0, 25, 50]);
    expect(out.map((b) => b.y)).toEqual([50, 25, 0]);
    expect(out[0]!.x).toBeGreaterThan(0);
    expect(out[2]!.x + out[2]!.width).toBeLessThan(100);
  });

  it("TestBars_no_values_is_no_bars", () => {
    expect(bars([], SIZE, { min: 0n, max: 0n })).toEqual([]);
  });

  it("TestBars_a_gap_slot_has_no_bar_and_the_others_keep_their_places", () => {
    const out = bars([10n, null, 10n], SIZE, { min: 0n, max: 10n });
    expect(out).toHaveLength(2);
    expect(out[1]!.x).toBeGreaterThan(66);
  });
});

describe("extent over gaps", () => {
  it("TestExtent_ignores_gaps_and_a_series_of_only_gaps_is_empty", () => {
    expect(extent([null, 5n, null, 9n], false)).toEqual({ min: 5n, max: 9n });
    expect(extent([null, null], true)).toEqual({ min: 0n, max: 0n });
  });
});

describe("stepPoints", () => {
  it("TestStepPoints_holds_each_level_over_its_slot", () => {
    expect(stepPoints([10n, 5n], SIZE, { min: 0n, max: 10n })).toEqual(["0.0,0.0 50.0,0.0 50.0,25.0 100.0,25.0"]);
  });

  it("TestStepPoints_breaks_where_an_epoch_has_no_ceiling", () => {
    expect(stepPoints([10n, null, 10n], SIZE, { min: 0n, max: 10n })).toHaveLength(2);
  });
});
