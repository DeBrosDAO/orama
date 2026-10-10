import { describe, expect, it } from "vitest";
import {
  MAX_NORAMA_DIGITS,
  MINUS,
  NORAMA_PER_ORAMA,
  formatCompact,
  formatNorama,
  formatNoramaFixed,
  formatPct,
  formatSigned,
  parseNorama,
  shortAddress,
  shortHash,
} from "./units";

const oramaToNorama = (orama: number): string => (BigInt(orama) * NORAMA_PER_ORAMA).toString();

describe("formatNorama", () => {
  it("TestFormatNorama_splits_at_1e9_and_groups_digits", () => {
    expect(formatNorama("0")).toBe("0");
    expect(formatNorama("1")).toBe("0.000000001");
    expect(formatNorama("1500000000")).toBe("1.5");
    expect(formatNorama("21000640000000000")).toBe("21,000,640");
  });

  it("TestFormatNorama_truncates_never_rounds", () => {
    expect(formatNorama("1999999999", 2)).toBe("1.99");
    expect(formatNorama("999999999", 0)).toBe("0");
  });

  it("TestFormatNorama_negative_keeps_its_sign", () => {
    expect(formatNorama("-12500000000")).toBe(`${MINUS}12.5`);
    expect(formatNorama("-12500000000")).toBe("\u221212.5");
  });

  it("TestFormatNorama_rejects_non_integers", () => {
    for (const bad of ["01", "1.2", "", "abc", "1e9", " 1"]) expect(() => formatNorama(bad)).toThrow();
  });
});

describe("formatNoramaFixed", () => {
  it("TestFormatNoramaFixed_pads_and_truncates", () => {
    expect(formatNoramaFixed("1217000071200", 4)).toBe("1,217.0000");
    expect(formatNoramaFixed("1500000000", 4)).toBe("1.5000");
    expect(formatNoramaFixed("1999999999", 2)).toBe("1.99");
    expect(formatNoramaFixed("5", 0)).toBe("0");
  });

  it("TestFormatNoramaFixed_negative_uses_the_same_minus_as_the_others", () => {
    expect(formatNoramaFixed("-1500000000", 2)).toBe(`${MINUS}1.50`);
    expect(formatNoramaFixed("-1500000000", 0)).toBe(`${MINUS}1`);
  });
});

describe("formatSigned", () => {
  it("TestFormatSigned_reports_direction", () => {
    expect(formatSigned("12500000000")).toEqual({ sign: "+", text: "12.5" });
    expect(formatSigned("-12500000000")).toEqual({ sign: MINUS, text: "12.5" });
    expect(formatSigned("0")).toEqual({ sign: "", text: "0" });
  });
});

describe("formatCompact", () => {
  it("TestFormatCompact_picks_a_readable_unit", () => {
    expect(formatCompact(oramaToNorama(312))).toBe("312");
    expect(formatCompact(oramaToNorama(84_020))).toBe("84k");
    expect(formatCompact(oramaToNorama(41_200_000))).toBe("41.2M");
    expect(formatCompact(oramaToNorama(2_000_000_000))).toBe("2B");
  });

  it("TestFormatCompact_never_rounds_up_across_a_unit_boundary", () => {
    expect(formatCompact(oramaToNorama(999_960))).toBe("999.9k");
    expect(formatCompact(oramaToNorama(999_999_999))).toBe("999.9M");
    expect(formatCompact(oramaToNorama(9_999))).toBe("9,999");
    expect(formatCompact(oramaToNorama(10_000))).toBe("10k");
  });

  it("TestFormatCompact_edges_zero_negative_and_sub_orama", () => {
    expect(formatCompact("0")).toBe("0");
    expect(formatCompact("999999999")).toBe("0");
    expect(formatCompact(oramaToNorama(-84_020))).toBe(`${MINUS}84k`);
    expect(formatCompact(oramaToNorama(-312))).toBe(`${MINUS}312`);
  });
});

describe("misc", () => {
  it("TestParseNorama_round_trips", () => {
    expect(parseNorama(oramaToNorama(7))).toBe(7_000_000_000n);
  });

  it("TestParseNorama_rejects_more_digits_than_any_supply_needs", () => {
    expect(parseNorama("9".repeat(MAX_NORAMA_DIGITS))).toBe(10n ** BigInt(MAX_NORAMA_DIGITS) - 1n);
    expect(parseNorama(`-${"9".repeat(MAX_NORAMA_DIGITS)}`)).toBeLessThan(0n);
    expect(() => parseNorama("1".repeat(MAX_NORAMA_DIGITS + 1))).toThrow(/digits/);
    expect(() => parseNorama(`-${"1".repeat(MAX_NORAMA_DIGITS + 1)}`)).toThrow(/digits/);
  });

  it("TestFormatPct_one_decimal", () => {
    expect(formatPct(0.182)).toBe("18.2%");
    expect(formatPct(0)).toBe("0.0%");
  });

  it("TestShorteners_keep_short_values_whole", () => {
    expect(shortAddress("orama1abc")).toBe("orama1abc");
    expect(shortAddress("orama1q8w9k3v5r2m4x7h6d0n8c1p3t5y9j2u4l6e7x2")).toBe("orama1q8w9…e7x2");
    expect(shortHash("ABCDEF")).toBe("ABCDEF");
    expect(shortHash("A91F3C0000000000000000000000000000000000000000000000000000BC03BC")).toBe("A91F3C…03BC");
  });
});
