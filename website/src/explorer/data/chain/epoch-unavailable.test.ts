import { describe, expect, it } from "vitest";
import { ChainReadError } from "./client";
import { readEpochRow } from "./aggregates";
import { chainFixture } from "./fake-gateway";
import { createChainSource } from "./source";

/** What chain/indexer's unavailableRow answers: heights, times, the reason, and none of the figures. */
const REASON =
  "the node no longer kept the chain state at the epoch's closing height (height 700), because the indexer was behind or stopped while the epoch closed; the figures read from the chain cannot be known";

function unavailableJson(epoch: number, over: Record<string, unknown> = {}) {
  return {
    epoch,
    start_height: (epoch - 1) * 100 + 1,
    start_time: "2026-10-08T00:00:00Z",
    end_height: epoch * 100,
    end_time: "2026-10-09T00:00:00Z",
    blocks: 100,
    partial: false,
    unavailable: true,
    unavailable_reason: REASON,
    rewards_recorded: false,
    ...over,
  };
}

const FIGURES: Record<string, string> = {
  max_mintable: "14848000000000",
  minted_validators: "8908800000000",
  minted_service: "5000000000",
  minted_development: "0",
  minted_faucet: "0",
  minted_total: "8913800000000",
  burned: "300",
  fees_collected: "900",
  fees_burned: "300",
  fees_distributed: "600",
  rewards_credited: "8900000000000",
  rewards_force_bonded: "8800000000",
};

function recordedJson(epoch: number, over: Record<string, unknown> = {}) {
  return { ...unavailableJson(epoch), unavailable: false, unavailable_reason: undefined, ...FIGURES, ...over };
}

describe("readEpochRow unavailable", () => {
  it("TestReadEpochRow_an_unavailable_epoch_keeps_its_heights_times_and_reason_and_has_no_figure", () => {
    const row = readEpochRow(unavailableJson(7));
    expect(row).toEqual({
      epoch: 7,
      unavailable: true,
      reason: REASON,
      startHeight: 601,
      endHeight: 700,
      startTime: "2026-10-08T00:00:00.000Z",
      endTime: "2026-10-09T00:00:00.000Z",
      blocks: 100,
      partial: false,
      rewardsRecorded: false,
      rewardsCommission: null,
      rewardsOperatorShare: null,
      rewardsDelegators: null,
    });
  });

  it("TestReadEpochRow_an_unavailable_epoch_keeps_the_payout_split_the_indexer_reads_from_events", () => {
    const row = readEpochRow(
      unavailableJson(7, { rewards_recorded: true, rewards_commission: "1", rewards_operator_share: "4", rewards_delegators: "2" }),
    );
    expect(row).toMatchObject({ unavailable: true, rewardsRecorded: true, rewardsCommission: "1", rewardsOperatorShare: "4", rewardsDelegators: "2" });
  });

  it.each(Object.keys(FIGURES))("TestReadEpochRow_an_unavailable_epoch_that_carries_%s_is_refused", (key) => {
    expect(() => readEpochRow(unavailableJson(7, { [key]: FIGURES[key] }))).toThrow(ChainReadError);
    expect(() => readEpochRow(unavailableJson(7, { [key]: "0" }))).toThrow("carries figures");
  });

  it.each([
    ["no reason", { unavailable_reason: undefined }],
    ["an empty reason", { unavailable_reason: "" }],
    ["a reason of control characters", { unavailable_reason: "\u0000\u0007" }],
    ["a reason that is not text", { unavailable_reason: 4 }],
  ])("TestReadEpochRow_an_unavailable_epoch_with_%s_is_refused", (_name, over) => {
    expect(() => readEpochRow(unavailableJson(7, over))).toThrow(ChainReadError);
  });

  it("TestReadEpochRow_the_reason_is_made_safe_to_show", () => {
    const row = readEpochRow(unavailableJson(7, { unavailable_reason: "the node\u0007 pruned‮ it" }));
    expect(row.unavailable && row.reason).toBe("the node pruned it");
  });

  it("TestReadEpochRow_an_unavailable_flag_that_is_not_a_boolean_is_refused", () => {
    expect(() => readEpochRow(unavailableJson(7, { unavailable: "yes" }))).toThrow(ChainReadError);
  });
});

describe("readEpochRow recorded", () => {
  it("TestReadEpochRow_a_recorded_epoch_has_every_figure_and_says_it_is_not_unavailable", () => {
    const row = readEpochRow(recordedJson(7));
    expect(row).toMatchObject({ epoch: 7, unavailable: false, mintedTotal: FIGURES.minted_total, rewardsForceBonded: FIGURES.rewards_force_bonded });
  });

  it("TestReadEpochRow_a_row_with_no_unavailable_flag_is_recorded", () => {
    const { unavailable: _u, ...rest } = recordedJson(7);
    expect(readEpochRow(rest).unavailable).toBe(false);
  });

  it.each(Object.keys(FIGURES))("TestReadEpochRow_a_recorded_epoch_missing_%s_is_refused", (key) => {
    expect(() => readEpochRow(recordedJson(7, { [key]: undefined }))).toThrow(ChainReadError);
  });

  it("TestReadEpochRow_a_recorded_epoch_that_gives_a_reason_for_being_unavailable_is_refused", () => {
    expect(() => readEpochRow(recordedJson(7, { unavailable_reason: REASON }))).toThrow(ChainReadError);
  });
});

describe("the source with an unavailable epoch", () => {
  it("TestGetEpochs_a_page_with_an_unavailable_epoch_reads_whole_and_keeps_the_others", async () => {
    const gw = await chainFixture();
    const source = createChainSource({ fetcher: gw.fetch });
    gw.on("index/epochs?page=1&limit=3", { page: 1, limit: 3, epochs: [recordedJson(9), unavailableJson(8), recordedJson(7)] });
    const page = await source.getEpochs({ page: 1, limit: 3 });
    expect(page.items.map((e) => [e.epoch, e.unavailable])).toEqual([[9, false], [8, true], [7, false]]);
  });

  it("TestGetEpoch_an_unavailable_epoch_is_a_row_not_a_miss", async () => {
    const gw = await chainFixture();
    const source = createChainSource({ fetcher: gw.fetch });
    gw.on("index/epochs/8", unavailableJson(8));
    expect(await source.getEpoch(8)).toMatchObject({ epoch: 8, unavailable: true, reason: REASON });
  });

  it("TestGetEpochs_one_malformed_unavailable_epoch_still_rejects_the_page", async () => {
    const gw = await chainFixture();
    const source = createChainSource({ fetcher: gw.fetch });
    gw.on("index/epochs?page=1&limit=2", { page: 1, limit: 2, epochs: [recordedJson(9), unavailableJson(8, { burned: "5" })] });
    await expect(source.getEpochs({ page: 1, limit: 2 })).rejects.toBeInstanceOf(ChainReadError);
  });
});
