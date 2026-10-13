import { describe, expect, it } from "vitest";
import { sparklinePoints } from "./sparkline";

describe("sparklinePoints", () => {
  it("draws a flat line across the width for a single value", () => {
    expect(sparklinePoints([7], 100, 26)).toBe("0,13 100,13");
  });

  it("draws a flat line for no data", () => {
    expect(sparklinePoints([], 100, 26)).toBe("0,13 100,13");
  });

  it("spans the full width from the first to the last value", () => {
    const pts = sparklinePoints([1, 2, 3], 100, 26).split(" ");
    expect(pts).toHaveLength(3);
    expect(pts[0]?.startsWith("0.0,")).toBe(true);
    expect(pts[2]?.startsWith("100.0,")).toBe(true);
  });

  it("puts the highest value at the top and the lowest at the bottom, inside the padding", () => {
    const [low, high] = sparklinePoints([0, 10], 100, 26).split(" ");
    expect(low).toBe("0.0,24.0");
    expect(high).toBe("100.0,2.0");
  });

  it("keeps a constant series on one horizontal line", () => {
    const ys = sparklinePoints([5, 5, 5], 100, 26).split(" ").map((p) => p.split(",")[1]);
    expect(new Set(ys).size).toBe(1);
  });
});
