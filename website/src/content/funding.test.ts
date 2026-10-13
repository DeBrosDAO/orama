import { describe, expect, it } from "vitest";
import {
  ALLOCATIONS,
  FUNDING_MONTHS,
  FUNDING_TOTAL_EUR,
  TIMELINE,
  formatEur,
  formatEurShort,
} from "./funding";

describe("funding plan", () => {
  it("TestAllocations_sum_to_total", () => {
    const sum = ALLOCATIONS.reduce((n, a) => n + a.amountEur, 0);
    expect(sum).toBe(FUNDING_TOTAL_EUR);
  });

  it("TestAllocations_positive_and_unique", () => {
    for (const a of ALLOCATIONS) expect(a.amountEur).toBeGreaterThan(0);
    expect(new Set(ALLOCATIONS.map((a) => a.id)).size).toBe(ALLOCATIONS.length);
  });

  it("TestTimeline_ascending_and_ends_at_horizon", () => {
    const months = TIMELINE.map((t) => t.month);
    expect(months).toEqual([...months].sort((a, b) => a - b));
    expect(months[0]).toBe(0);
    expect(months.at(-1)).toBe(FUNDING_MONTHS);
  });
});

describe("euro formatting", () => {
  it("TestFormatEurShort_thousands_and_millions", () => {
    expect(formatEurShort(400_000)).toBe("€400k");
    expect(formatEurShort(50_000)).toBe("€50k");
    expect(formatEurShort(1_000_000)).toBe("€1M");
  });

  it("TestFormatEurShort_edge_values", () => {
    expect(formatEurShort(0)).toBe("€0");
    expect(formatEurShort(999)).toBe("€1k");
    expect(formatEurShort(999_999)).toBe("€1M");
    expect(formatEurShort(2_500_000)).toBe("€2.5M");
  });

  it("TestFormatEur_is_euro_not_dollar", () => {
    const s = formatEur(FUNDING_TOTAL_EUR);
    expect(s).toContain("€");
    expect(s).not.toContain("$");
  });
});
