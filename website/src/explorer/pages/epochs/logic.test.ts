import { describe, expect, it } from "vitest";
import type { RecordedEpochRow } from "../../model/epochs";
import { ceilingWindow, epochSpanMs, pageSearch, parseEpochParam, parsePageParam, sharePercent, uptimePercent, whyMissing } from "./logic";

const ORAMA = 1_000_000_000n;

function row(epoch: number, over: Partial<RecordedEpochRow> = {}): RecordedEpochRow {
  return {
    epoch,
    unavailable: false,
    startHeight: 1,
    endHeight: 2,
    startTime: "2026-10-08T00:00:00.000Z",
    endTime: "2026-10-09T00:00:00.000Z",
    blocks: 2,
    partial: false,
    maxMintable: (14_848n * ORAMA).toString(),
    mintedValidators: (8_908n * ORAMA + 800_000_000n).toString(),
    mintedService: "0",
    mintedDevelopment: "0",
    mintedFaucet: "0",
    mintedTotal: (8_908n * ORAMA + 800_000_000n).toString(),
    burned: "0",
    feesCollected: "0",
    feesBurned: "0",
    feesDistributed: "0",
    rewardsCredited: "0",
    rewardsForceBonded: "0",
    rewardsRecorded: false,
    rewardsCommission: null,
    rewardsOperatorShare: null,
    rewardsDelegators: null,
    ...over,
  };
}

describe("parseEpochParam", () => {
  it.each([
    ["1", 1],
    ["730", 730],
    ["731", 731],
  ])("TestParseEpochParam_%s", (raw, n) => {
    expect(parseEpochParam(raw)).toBe(n);
  });

  it.each([undefined, "", "0", "-1", "1.5", "12abc", "0x10", " 3", "01", "9999999999999999999"])("TestParseEpochParam_refuses_%s", (raw) => {
    expect(parseEpochParam(raw)).toBeNull();
  });
});

describe("parsePageParam", () => {
  it("TestParsePageParam_a_missing_or_odd_value_is_page_one", () => {
    expect(parsePageParam(null)).toBe(1);
    expect(parsePageParam("0")).toBe(1);
    expect(parsePageParam("abc")).toBe(1);
    expect(parsePageParam("-2")).toBe(1);
  });

  it("TestParsePageParam_is_held_to_the_indexers_last_page", () => {
    expect(parsePageParam("7")).toBe(7);
    expect(parsePageParam("5000")).toBe(1000);
  });
});

describe("pageSearch", () => {
  it("TestPageSearch_sets_the_page_and_keeps_the_rest_of_the_query", () => {
    expect(pageSearch(new URLSearchParams("vpage=2&x=1"), "vpage", 3)).toBe("?vpage=3&x=1");
    expect(pageSearch(new URLSearchParams("x=1"), "page", 4)).toBe("?x=1&page=4");
  });

  it("TestPageSearch_page_one_is_the_bare_url", () => {
    expect(pageSearch(new URLSearchParams("page=2"), "page", 1)).toBe("");
    expect(pageSearch(new URLSearchParams("page=2&x=1"), "page", 1)).toBe("?x=1");
    expect(pageSearch(new URLSearchParams(""), "page", 0)).toBe("");
  });
});

describe("sharePercent", () => {
  it("TestSharePercent_truncates_to_a_tenth", () => {
    expect(sharePercent(1n, 3n)).toBe("33.3%");
    expect(sharePercent(2n, 3n)).toBe("66.6%");
    expect(sharePercent(5n, 5n)).toBe("100.0%");
    expect(sharePercent(0n, 5n)).toBe("0.0%");
  });

  it("TestSharePercent_has_no_answer_without_a_whole_and_exceeds_100_when_minted_beats_the_ceiling", () => {
    expect(sharePercent(1n, 0n)).toBeNull();
    expect(sharePercent(3n, 2n)).toBe("150.0%");
  });

  it("TestSharePercent_is_exact_for_amounts_beyond_a_double", () => {
    expect(sharePercent(10n ** 30n / 4n, 10n ** 30n)).toBe("25.0%");
  });
});

describe("uptimePercent", () => {
  it("TestUptimePercent_from_basis_points", () => {
    expect(uptimePercent(10_000)).toBe("100.00%");
    expect(uptimePercent(9_975)).toBe("99.75%");
    expect(uptimePercent(0)).toBe("0.00%");
  });
});

describe("epochSpanMs", () => {
  it("TestEpochSpanMs_is_end_minus_start_and_never_negative", () => {
    expect(epochSpanMs(row(1))).toBe(86_400_000);
    expect(epochSpanMs({ startTime: "2026-10-09T00:00:00Z", endTime: "2026-10-08T00:00:00Z" })).toBe(0);
  });
});

describe("whyMissing", () => {
  it("TestWhyMissing_open_future_or_before_the_index", () => {
    expect(whyMissing(12, { epoch: 12 })).toBe("open");
    expect(whyMissing(13, { epoch: 12 })).toBe("future");
    expect(whyMissing(11, { epoch: 12 })).toBe("unindexed");
  });
});

describe("ceilingWindow", () => {
  it("TestCeilingWindow_sums_minted_against_the_split_ceilings", () => {
    const win = ceilingWindow([
      row(2, { mintedService: (3_712n * ORAMA).toString(), mintedTotal: (8_908n * ORAMA + 800_000_000n + 3_712n * ORAMA).toString() }),
      row(1),
    ]);
    expect(win.epochs).toBe(2);
    expect(win.validators.ceiling).toBe(2n * (8_908n * ORAMA + 800_000_000n));
    expect(win.validators.minted).toBe(2n * (8_908n * ORAMA + 800_000_000n));
    // storage 3712 + relay 1484.8 per epoch, as one figure
    expect(win.service.ceiling).toBe(2n * (3_712n * ORAMA + 1_484n * ORAMA + 800_000_000n));
    expect(win.service.minted).toBe(3_712n * ORAMA);
    expect(win.development).toEqual({ ceiling: 2n * (742n * ORAMA + 400_000_000n), minted: 0n });
    expect(win.total.ceiling).toBe(2n * 14_848n * ORAMA);
    expect(win.faucet).toBe(0n);
  });

  it("TestCeilingWindow_a_partial_epoch_is_left_out_and_counted", () => {
    const win = ceilingWindow([row(3), row(2, { partial: true, mintedTotal: "1" })]);
    expect(win.epochs).toBe(1);
    expect(win.partialSkipped).toBe(1);
    expect(win.total.ceiling).toBe(14_848n * ORAMA);
  });

  it("TestCeilingWindow_no_epochs_is_all_zeros", () => {
    const win = ceilingWindow([]);
    expect(win.epochs).toBe(0);
    expect(win.total).toEqual({ ceiling: 0n, minted: 0n });
  });

  it("TestCeilingWindow_faucet_mints_are_counted_apart_from_the_ceilings", () => {
    const win = ceilingWindow([row(1, { mintedFaucet: "500", mintedTotal: (8_908n * ORAMA + 800_000_000n + 500n).toString() })]);
    expect(win.faucet).toBe(500n);
    expect(win.validators.minted).toBe(8_908n * ORAMA + 800_000_000n);
  });
});
