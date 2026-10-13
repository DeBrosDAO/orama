import { describe, expect, it } from "vitest";
import {
  MAX_LABEL_QUERY_LENGTH,
  buildRows,
  hitRow,
  labelQueryOf,
  labelRows,
  moveSelection,
  optionId,
  quickRows,
  shouldSearchLabels,
} from "./palette-model";

const ADDRESS = "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq";
const HASH = "A".repeat(64);

describe("shouldSearchLabels", () => {
  it("searches names for free text", () => {
    expect(shouldSearchLabels("val-3", null)).toBe(true);
  });

  it("skips the search when the query is already an exact hit", () => {
    expect(shouldSearchLabels(ADDRESS, { kind: "wallet", address: ADDRESS })).toBe(false);
    expect(shouldSearchLabels("42", { kind: "block", height: 42 })).toBe(false);
    expect(shouldSearchLabels("u1qqqqqqqq", { kind: "shielded-address" })).toBe(false);
  });

  it("skips the search for an empty or blank query", () => {
    expect(shouldSearchLabels("", null)).toBe(false);
    expect(shouldSearchLabels("   ", null)).toBe(false);
  });
});

describe("labelQueryOf", () => {
  it("passes a short query through", () => {
    expect(labelQueryOf("val-3")).toBe("val-3");
  });

  it("caps a long query", () => {
    expect(labelQueryOf("x".repeat(500))).toHaveLength(MAX_LABEL_QUERY_LENGTH);
  });
});

describe("hitRow", () => {
  it("opens the matching page for each exact hit", () => {
    expect(hitRow({ kind: "block", height: 1234 })?.to).toContain("/block/1234");
    expect(hitRow({ kind: "tx", hash: HASH })?.to).toContain(HASH);
    expect(hitRow({ kind: "wallet", address: ADDRESS })?.to).toContain(ADDRESS);
  });

  it("has no row for a shielded address", () => {
    expect(hitRow({ kind: "shielded-address" })).toBeNull();
  });
});

describe("labelRows", () => {
  it("shows the short address next to every name", () => {
    const [row] = labelRows([{ address: ADDRESS, label: "Alice" }]);
    expect(row?.text).toBe("Alice");
    expect(row?.code).toBeTruthy();
    expect(row?.code).toContain("…");
    expect(row?.seed).toBe(ADDRESS);
  });

  it("two wallets with the same name stay distinguishable", () => {
    const other = ADDRESS.slice(0, -4) + "zzzz";
    const rows = labelRows([
      { address: ADDRESS, label: "Alice" },
      { address: other, label: "Alice" },
    ]);
    expect(rows[0]?.code).not.toBe(rows[1]?.code);
    expect(rows[0]?.key).not.toBe(rows[1]?.key);
  });

  it("is empty for no matches", () => {
    expect(labelRows([])).toEqual([]);
  });
});

describe("quickRows", () => {
  it("lists both examples and the validators shortcut", () => {
    const rows = quickRows({ latestTxHash: HASH, busyWalletAddress: ADDRESS });
    expect(rows.map((r) => r.key)).toEqual(["ex-tx", "ex-wallet", "validators"]);
  });

  it("omits an example the source does not have", () => {
    expect(quickRows({ latestTxHash: null, busyWalletAddress: ADDRESS }).map((r) => r.key)).toEqual(["ex-wallet", "validators"]);
    expect(quickRows({ latestTxHash: HASH, busyWalletAddress: null }).map((r) => r.key)).toEqual(["ex-tx", "validators"]);
  });

  it("keeps only the validators shortcut before examples load", () => {
    expect(quickRows(null).map((r) => r.key)).toEqual(["validators"]);
  });
});

describe("buildRows", () => {
  it("puts an exact hit first and shows no shortcuts once something is typed", () => {
    const rows = buildRows({ trimmed: "42", hit: { kind: "block", height: 42 }, labels: [], examples: null });
    expect(rows.map((r) => r.key)).toEqual(["block"]);
  });

  it("shows name matches for free text", () => {
    const rows = buildRows({ trimmed: "ali", hit: null, labels: [{ address: ADDRESS, label: "Alice" }], examples: null });
    expect(rows).toHaveLength(1);
    expect(rows[0]?.hint).toBe("Named wallet");
  });

  it("has no rows for a shielded address or an unmatched query", () => {
    expect(buildRows({ trimmed: "u1qqqqqqqq", hit: { kind: "shielded-address" }, labels: [], examples: null })).toEqual([]);
    expect(buildRows({ trimmed: "zzz", hit: null, labels: [], examples: null })).toEqual([]);
  });

  it("shows shortcuts for an empty query", () => {
    expect(buildRows({ trimmed: "", hit: null, labels: [], examples: null }).length).toBeGreaterThan(0);
  });
});

describe("moveSelection", () => {
  it("moves and clamps at both ends", () => {
    expect(moveSelection(0, 1, 3)).toBe(1);
    expect(moveSelection(2, 1, 3)).toBe(2);
    expect(moveSelection(0, -1, 3)).toBe(0);
  });

  it("stays at 0 for an empty list", () => {
    expect(moveSelection(0, 1, 0)).toBe(0);
    expect(moveSelection(0, -1, 0)).toBe(0);
  });
});

describe("optionId", () => {
  it("is unique per index", () => {
    expect(optionId(":r1:", 0)).not.toBe(optionId(":r1:", 1));
  });
});
