import { describe, expect, it } from "vitest";
import type { RecordedEpochRow } from "../../model/epochs";
import type { ValidatorReward, ValidatorRewardEpoch } from "../../model/rewards";
import { operatorOwn, recordedFrom, splitOf, summarise } from "./logic";

const ORAMA = 1_000_000_000n;

function row(over: Partial<RecordedEpochRow> = {}): RecordedEpochRow {
  return {
    epoch: 9, unavailable: false, startHeight: 1, endHeight: 2, startTime: "2026-10-08T00:00:00.000Z", endTime: "2026-10-09T00:00:00.000Z", blocks: 2, partial: false,
    maxMintable: "0", mintedValidators: "0", mintedService: "0", mintedDevelopment: "0", mintedFaucet: "0", mintedTotal: "0",
    burned: "0", feesCollected: "0", feesBurned: "0", feesDistributed: "0", rewardsCredited: "0", rewardsForceBonded: "0",
    rewardsRecorded: true, rewardsCommission: (10n * ORAMA).toString(), rewardsOperatorShare: (70n * ORAMA).toString(), rewardsDelegators: (30n * ORAMA).toString(),
    ...over,
  };
}

const ref = { moniker: "Alpha", operator: "orama1x" };
const reward = (over: Partial<ValidatorReward> = {}): ValidatorReward => ({
  epoch: 9, ref, amount: "1000", commission: "100", operatorShare: "730", delegators: "270", forceBonded: "0", ...over,
});

describe("splitOf", () => {
  it("TestSplitOf_divides_what_validators_were_paid_into_commission_operators_own_and_delegators", () => {
    expect(splitOf(row())).toEqual({
      total: (100n * ORAMA).toString(),
      commission: (10n * ORAMA).toString(),
      operatorsOwn: (60n * ORAMA).toString(),
      delegators: (30n * ORAMA).toString(),
      commissionPct: "10.0%",
      operatorsOwnPct: "60.0%",
      delegatorsPct: "30.0%",
    });
  });

  it("TestSplitOf_an_epoch_that_is_not_recorded_has_no_split_not_a_zero_one", () => {
    expect(splitOf(row({ rewardsRecorded: false, rewardsCommission: null, rewardsOperatorShare: null, rewardsDelegators: null }))).toBeNull();
  });

  it("TestSplitOf_an_epoch_that_paid_nothing_is_recorded_with_a_zero_total_and_no_percentages", () => {
    expect(splitOf(row({ rewardsCommission: "0", rewardsOperatorShare: "0", rewardsDelegators: "0" }))).toEqual({
      total: "0", commission: "0", operatorsOwn: "0", delegators: "0", commissionPct: null, operatorsOwnPct: null, delegatorsPct: null,
    });
  });

  it("TestSplitOf_percentages_are_truncated_like_every_amount", () => {
    const s = splitOf(row({ rewardsCommission: "1", rewardsOperatorShare: "2", rewardsDelegators: "1" }));
    expect(s?.operatorsOwnPct).toBe("33.3%");
    expect(splitOf(row({ rewardsCommission: "0", rewardsOperatorShare: "1", rewardsDelegators: "5" }))?.operatorsOwnPct).toBe("16.6%");
  });
});

describe("operatorOwn", () => {
  it("TestOperatorOwn_is_the_operators_share_less_the_commission", () => {
    expect(operatorOwn(reward())).toBe("630");
    expect(operatorOwn(reward({ commission: "0", operatorShare: "1000", delegators: "0" }))).toBe("1000");
  });
});

describe("summarise", () => {
  const item = (epoch: number, recorded: boolean, r: ValidatorReward | null): ValidatorRewardEpoch => ({ epoch, recorded, reward: r });

  it("TestSummarise_adds_the_recorded_epochs_and_counts_the_others_apart", () => {
    const total = summarise([item(9, true, reward()), item(8, true, null), item(7, false, null), item(6, true, reward({ amount: "500", commission: "0", operatorShare: "500", delegators: "0" }))]);
    expect(total).toEqual({ epochs: 3, notRecorded: 1, paid: "1500", commission: "100", delegators: "270" });
  });

  it("TestSummarise_an_empty_list_is_all_zero", () => {
    expect(summarise([])).toEqual({ epochs: 0, notRecorded: 0, paid: "0", commission: "0", delegators: "0" });
  });

  it("TestSummarise_epochs_not_recorded_add_nothing", () => {
    expect(summarise([item(5, false, null), item(4, false, null)])).toMatchObject({ epochs: 0, notRecorded: 2, paid: "0" });
  });
});

describe("recordedFrom", () => {
  it("TestRecordedFrom_says_where_the_record_begins_or_that_it_has_not", () => {
    expect(recordedFrom(0)).toContain("No epoch's payout has been recorded yet");
    expect(recordedFrom(1234)).toContain("from epoch 1,234");
    expect(recordedFrom(1234)).toContain("not shown as zero");
  });
});
