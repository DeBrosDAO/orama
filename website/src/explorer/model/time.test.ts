import { describe, expect, it } from "vitest";
import { TIME, formatCountdown, formatDayHeading, formatRelative, formatUtc } from "./time";

const NOW = Date.UTC(2026, 8, 29, 14, 0, 0);
const ago = (ms: number) => new Date(NOW - ms).toISOString();

describe("formatRelative", () => {
  it("TestFormatRelative_scales_units", () => {
    expect(formatRelative(ago(2_000), NOW)).toBe("just now");
    expect(formatRelative(ago(14_000), NOW)).toBe("14 s ago");
    expect(formatRelative(ago(3 * 60_000), NOW)).toBe("3 min ago");
    expect(formatRelative(ago(2 * 3_600_000), NOW)).toBe("2 h ago");
    expect(formatRelative(ago(5 * 86_400_000), NOW)).toBe("5 d ago");
  });

  it("TestFormatRelative_old_dates_show_the_date", () => {
    expect(formatRelative(ago(60 * 86_400_000), NOW)).toBe("31 Jul 2026");
  });

  it("TestFormatRelative_future_and_garbage", () => {
    expect(formatRelative(new Date(NOW + 5_000).toISOString(), NOW)).toBe("just now");
    expect(formatRelative("not a date", NOW)).toBe("");
  });
});

describe("day headings and countdowns", () => {
  it("TestFormatDayHeading_today_yesterday_then_date", () => {
    expect(formatDayHeading(ago(1_000), NOW)).toBe("Today");
    expect(formatDayHeading(ago(86_400_000), NOW)).toBe("Yesterday");
    expect(formatDayHeading(ago(3 * 86_400_000), NOW)).toBe("26 Sep");
  });

  it("TestFormatCountdown_hours_then_minutes_and_never_negative", () => {
    expect(formatCountdown(new Date(NOW + 9 * 3_600_000 + 22 * 60_000).toISOString(), NOW)).toBe("9 h 22 m");
    expect(formatCountdown(new Date(NOW + 5 * 60_000).toISOString(), NOW)).toBe("5 m");
    expect(formatCountdown(new Date(NOW - 5_000).toISOString(), NOW)).toBe("0 m");
  });

  it("TestFormatUtc_is_exact", () => {
    expect(formatUtc("2026-09-29T14:02:11.000Z")).toBe("2026-09-29 14:02:11 UTC");
  });
});

describe("TIME", () => {
  it("TestTime_units_are_exact", () => {
    expect(TIME.SECOND).toBe(1000);
    expect(TIME.HOUR).toBe(3_600_000);
    expect(TIME.DAY).toBe(86_400_000);
    expect(TIME.DAY).toBe(24 * TIME.HOUR);
  });
});
