import { describe, expect, it } from "vitest";
import { counterparty } from "./fixtures";
import { layoutMoneyMap, MAP_MAX_NODES, MAP_MAX_STROKE, MAP_MIN_STROKE } from "./money-map-layout";

const SIZE = { width: 310, height: 220 };
const many = (n: number) => Array.from({ length: n }, (_, i) => counterparty(String((i + 1) * 100), `orama1n${i}`));

describe("layoutMoneyMap", () => {
  it("TestLayoutMoneyMap_no_counterparties_has_only_the_centre", () => {
    const l = layoutMoneyMap([], SIZE);
    expect(l.nodes).toEqual([]);
    expect(l.center.x).toBe(SIZE.width / 2);
  });

  it("TestLayoutMoneyMap_one_counterparty_sits_at_the_top_with_full_stroke", () => {
    const l = layoutMoneyMap([counterparty("5")], SIZE);
    const n = l.nodes[0];
    expect(n?.x).toBeCloseTo(l.center.x);
    expect(n?.y).toBeLessThan(l.center.y);
    expect(n?.strokeWidth).toBe(MAP_MAX_STROKE);
  });

  it("TestLayoutMoneyMap_five_are_evenly_spaced_starting_at_the_top", () => {
    const l = layoutMoneyMap(many(5), SIZE);
    expect(l.nodes).toHaveLength(5);
    expect(l.nodes[0]?.x).toBeCloseTo(l.center.x);
    const mirror = (i: number, j: number) => {
      expect((l.nodes[i]?.x ?? 0) - l.center.x).toBeCloseTo(l.center.x - (l.nodes[j]?.x ?? 0));
      expect(l.nodes[i]?.y).toBeCloseTo(l.nodes[j]?.y ?? NaN);
    };
    mirror(1, 4);
    mirror(2, 3);
    expect(l.nodes[0]?.y).toBeLessThan(l.center.y);
    expect(l.nodes[2]?.y).toBeGreaterThan(l.center.y);
  });

  it("TestLayoutMoneyMap_caps_at_five_nodes", () => {
    expect(layoutMoneyMap(many(9), SIZE).nodes).toHaveLength(MAP_MAX_NODES);
  });

  it("TestLayoutMoneyMap_stroke_is_proportional_to_volume", () => {
    const l = layoutMoneyMap([counterparty("100"), counterparty("50"), counterparty("0")], SIZE);
    const [a, b, c] = l.nodes.map((n) => n.strokeWidth) as [number, number, number];
    expect(a).toBe(MAP_MAX_STROKE);
    expect(c).toBe(MAP_MIN_STROKE);
    expect(b).toBeCloseTo((MAP_MAX_STROKE + MAP_MIN_STROKE) / 2);
  });

  it("TestLayoutMoneyMap_equal_volumes_share_the_max_stroke", () => {
    const l = layoutMoneyMap([counterparty("7"), counterparty("7")], SIZE);
    expect(l.nodes.every((n) => n.strokeWidth === MAP_MAX_STROKE)).toBe(true);
  });

  it("TestLayoutMoneyMap_all_zero_volumes_are_thin_and_finite", () => {
    const l = layoutMoneyMap([counterparty("0"), counterparty("0")], SIZE);
    expect(l.nodes.every((n) => n.strokeWidth === MAP_MIN_STROKE)).toBe(true);
    expect(l.nodes.every((n) => Number.isFinite(n.x) && Number.isFinite(n.y))).toBe(true);
  });

  it("TestLayoutMoneyMap_keeps_every_node_inside_the_box", () => {
    const l = layoutMoneyMap(many(5), SIZE);
    for (const n of l.nodes) {
      expect(n.x).toBeGreaterThan(0);
      expect(n.x).toBeLessThan(SIZE.width);
      expect(n.y).toBeGreaterThan(0);
      expect(n.y).toBeLessThan(SIZE.height);
    }
  });
});
