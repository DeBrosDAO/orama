import { describe, expect, it } from "vitest";
import { activityToCsv, csvFilename, CSV_HEADER } from "./csv";
import type { ActivityItem } from "../../model/types";
import { ALICE, BOB, sendItem } from "./fixtures";

const lines = (csv: string) => csv.split("\r\n");

describe("activityToCsv", () => {
  it("TestActivityToCsv_empty_is_header_only", () => {
    expect(activityToCsv([])).toBe(`${CSV_HEADER.join(",")}\r\n`);
    expect(CSV_HEADER.join(",")).toBe("time,direction,type,counterparty,amount_ORAMA,status,tx_hash");
  });

  it("TestActivityToCsv_one_row", () => {
    const item = sendItem({ time: "2026-09-29T13:59:00.000Z", hash: "AB12" });
    const [, row] = lines(activityToCsv([item]));
    expect(row).toBe("2026-09-29T13:59:00.000Z,out,send,Alice,-12.5,ok,AB12");
  });

  it("TestActivityToCsv_uses_address_when_no_label", () => {
    const [, row] = lines(activityToCsv([sendItem({ counterparty: BOB })]));
    expect(row).toContain(",orama1bob,");
  });

  it("TestActivityToCsv_no_counterparty_is_empty_cell", () => {
    const [, row] = lines(activityToCsv([sendItem({ counterparty: null })]));
    expect(row.split(",")[3]).toBe("");
  });

  it("TestActivityToCsv_quotes_commas_and_doubles_quotes", () => {
    const label = { ...ALICE, label: 'Smith, "Bob" & Co' };
    const [, row] = lines(activityToCsv([sendItem({ counterparty: label })]));
    expect(row).toContain(',"Smith, ""Bob"" & Co",');
  });

  it("TestActivityToCsv_quotes_line_breaks", () => {
    const label = { ...ALICE, label: "two\nlines" };
    expect(activityToCsv([sendItem({ counterparty: label })])).toContain('"two\nlines"');
  });

  it.each(["=SUM(A1)", "+1", "-1", "@cmd", "\tx", "\rx"])("TestActivityToCsv_neutralises_formula_label_%j", (label) => {
    const csv = activityToCsv([sendItem({ counterparty: { ...ALICE, label } })]);
    expect(csv).toContain(`'${label}`);
    expect(csv).not.toMatch(new RegExp(`,${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")},`));
  });

  it("TestActivityToCsv_neutralises_formula_in_failure_reason_and_keeps_amount_numeric", () => {
    const item = sendItem({ status: { ok: false, reason: "=HYPERLINK(1)" } });
    const [, row] = lines(activityToCsv([item]));
    expect(row).toContain(",-12.5,");
    expect(row).toContain("failed: =HYPERLINK(1)");
    expect(row).not.toMatch(/,=HYPERLINK/);
  });

  it("TestActivityToCsv_defuses_a_formula_in_every_string_column", () => {
    const evil = "=HYPERLINK(1)";
    const item = sendItem({
      time: evil,
      hash: evil,
      direction: evil as ActivityItem["direction"],
      status: { ok: false, reason: evil },
      counterparty: { ...ALICE, label: evil },
    });
    const [, row] = lines(activityToCsv([item]));
    expect(row).not.toMatch(/(^|,)"?=/);
    expect(row.match(/'=HYPERLINK\(1\)/g)).toHaveLength(4);
    expect(row).toContain(",send,");
    expect(row).toContain(",-12.5,");
  });

  it("TestActivityToCsv_type_column_is_defused_too", () => {
    const item = sendItem({ message: { type: "=x" as never, from: ALICE, to: BOB, amount: "1" } });
    expect(lines(activityToCsv([item]))[1]).toContain(",'=x,");
  });

  it("TestActivityToCsv_has_no_comment_row", () => {
    const out = lines(activityToCsv([sendItem()]));
    expect(out[0]).toBe(CSV_HEADER.join(","));
    expect(out).toHaveLength(3);
    expect(activityToCsv([sendItem()])).not.toContain("#");
  });

  it("TestActivityToCsv_amount_has_no_digit_grouping", () => {
    const [, row] = lines(activityToCsv([sendItem({ amount: "1234567000000000", direction: "in" })]));
    expect(row).toContain(",1234567,");
  });

  it("TestActivityToCsv_rejects_a_non_integer_amount", () => {
    expect(() => activityToCsv([sendItem({ amount: "12.5" })])).toThrow(/norama integer/);
  });

  it("TestActivityToCsv_one_line_per_row_in_order", () => {
    const a = sendItem({ hash: "H1" });
    const b = sendItem({ hash: "H2" });
    const out = lines(activityToCsv([a, b]));
    expect(out).toHaveLength(4);
    expect(out[1]).toMatch(/H1$/);
    expect(out[2]).toMatch(/H2$/);
  });
});

describe("csvFilename", () => {
  it("TestCsvFilename_is_the_address_and_a_suffix", () => {
    expect(csvFilename("orama1abc")).toBe("orama1abc-activity.csv");
  });
});
