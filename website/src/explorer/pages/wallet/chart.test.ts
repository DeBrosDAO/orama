import { describe, expect, it } from "vitest";
import { balanceChartLabel, buildChartPaths, CHART_PAD } from "./chart";

const pt = (day: number, total: string) => ({ time: `2026-09-${String(day).padStart(2, "0")}T00:00:00.000Z`, total });
const W = 100;
const H = 50;

describe("buildChartPaths", () => {
  it("TestBuildChartPaths_no_points", () => {
    expect(buildChartPaths([], W, H)).toEqual({ line: "", area: "", min: "0", max: "0" });
  });

  it("TestBuildChartPaths_one_point_is_a_flat_line_across", () => {
    const r = buildChartPaths([pt(1, "5")], W, H);
    expect(r.line).toBe("M0.00,25.00 L100.00,25.00");
    expect(r.min).toBe("5");
    expect(r.max).toBe("5");
  });

  it("TestBuildChartPaths_rising_series_goes_bottom_left_to_top_right", () => {
    const r = buildChartPaths([pt(1, "0"), pt(2, "50"), pt(3, "100")], W, H);
    expect(r.line).toBe(`M0.00,${H - CHART_PAD}.00 L50.00,25.00 L100.00,${CHART_PAD}.00`);
    expect(r.min).toBe("0");
    expect(r.max).toBe("100");
  });

  it("TestBuildChartPaths_area_closes_down_to_the_baseline", () => {
    const r = buildChartPaths([pt(1, "0"), pt(2, "10")], W, H);
    expect(r.area.startsWith(r.line)).toBe(true);
    expect(r.area.endsWith("L100.00,50.00 L0.00,50.00 Z")).toBe(true);
  });

  it("TestBuildChartPaths_flat_series_is_drawn_in_the_middle", () => {
    const r = buildChartPaths([pt(1, "7"), pt(2, "7"), pt(3, "7")], W, H);
    expect(r.line).toBe("M0.00,25.00 L50.00,25.00 L100.00,25.00");
  });

  it("TestBuildChartPaths_places_points_by_time_not_by_index", () => {
    const r = buildChartPaths([pt(1, "0"), pt(2, "5"), pt(11, "10")], W, H);
    expect(r.line).toContain("L10.00,");
  });

  it("TestBuildChartPaths_same_timestamp_falls_back_to_even_spacing", () => {
    const same = { time: "2026-09-01T00:00:00.000Z" };
    const r = buildChartPaths([{ ...same, total: "0" }, { ...same, total: "5" }, { ...same, total: "10" }], W, H);
    expect(r.line).toContain("L50.00,");
  });

  it("TestBuildChartPaths_min_and_max_are_exact_for_huge_values", () => {
    const big = "123456789012345678901";
    const r = buildChartPaths([pt(1, big), pt(2, "1")], W, H);
    expect(r.max).toBe(big);
    expect(r.min).toBe("1");
  });

  it("TestBuildChartPaths_rejects_a_bad_amount", () => {
    expect(() => buildChartPaths([pt(1, "abc")], W, H)).toThrow();
  });
});

describe("balanceChartLabel", () => {
  it("TestBalanceChartLabel_reads_in_orama_not_norama", () => {
    const points = [pt(1, "1204500000000"), pt(2, "900000000000"), pt(3, "1707700000000")];
    expect(balanceChartLabel(points, "30d")).toBe("Balance from 1,204.5 to 1,707.7 ORAMA over the last 30 days");
  });

  it("TestBalanceChartLabel_names_the_range", () => {
    const points = [pt(1, "1000000000"), pt(2, "2000000000")];
    expect(balanceChartLabel(points, "7d")).toContain("over the last 7 days");
    expect(balanceChartLabel(points, "all")).toContain("over all time");
  });

  it("TestBalanceChartLabel_one_point_has_the_same_start_and_end", () => {
    expect(balanceChartLabel([pt(1, "5000000000")], "all")).toBe("Balance from 5 to 5 ORAMA over all time");
  });

  it("TestBalanceChartLabel_no_points_says_there_is_no_history", () => {
    expect(balanceChartLabel([], "7d")).toBe("No balance history over the last 7 days");
  });

  it("TestBalanceChartLabel_truncates_dust_and_never_prints_a_raw_integer", () => {
    expect(balanceChartLabel([pt(1, "1"), pt(2, "1000000001")], "30d")).toBe("Balance from 0 to 1 ORAMA over the last 30 days");
  });
});
