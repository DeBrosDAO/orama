import type { EpochRow } from "../../model/epochs";
import type { ValidatorReward, ValidatorRewardEpoch } from "../../model/rewards";
import type { Norama } from "../../model/types";
import { sharePercent } from "../epochs/logic";

/** What the epoch's payout was split into, in norama and as a share of what the validators were paid. */
export interface EpochSplit {
  /** Everything validators and their delegators were paid: the operators' share plus the delegators'. */
  total: Norama;
  /** The validators' commission. */
  commission: Norama;
  /** The operators' own cut as delegators of themselves, and the flooring remainder: their share less the commission. */
  operatorsOwn: Norama;
  delegators: Norama;
  commissionPct: string | null;
  operatorsOwnPct: string | null;
  delegatorsPct: string | null;
}

/**
 * The split of an epoch's payout; null for an epoch whose payout was not recorded, because a figure the chain
 * did not report is not zero. An epoch that paid nothing is recorded, with a total of 0 and no percentages.
 */
export function splitOf(row: EpochRow): EpochSplit | null {
  if (!row.rewardsRecorded || row.rewardsCommission === null || row.rewardsOperatorShare === null || row.rewardsDelegators === null) return null;
  const commission = BigInt(row.rewardsCommission);
  const operators = BigInt(row.rewardsOperatorShare);
  const delegators = BigInt(row.rewardsDelegators);
  const total = operators + delegators;
  const own = operators - commission;
  return {
    total: total.toString(),
    commission: commission.toString(),
    operatorsOwn: own.toString(),
    delegators: delegators.toString(),
    commissionPct: sharePercent(commission, total),
    operatorsOwnPct: sharePercent(own, total),
    delegatorsPct: sharePercent(delegators, total),
  };
}

/** What a validator keeps of its reward beyond the commission: its own stake's cut and the remainder. */
export function operatorOwn(reward: ValidatorReward): Norama {
  return (BigInt(reward.operatorShare) - BigInt(reward.commission)).toString();
}

export interface RewardSummary {
  /** Epochs counted: the recorded ones. */
  epochs: number;
  /** Epochs in the list that were not recorded. */
  notRecorded: number;
  paid: Norama;
  commission: Norama;
  delegators: Norama;
}

/** Totals over the recorded epochs of a validator's list; epochs not recorded are counted apart and add nothing. */
export function summarise(items: readonly ValidatorRewardEpoch[]): RewardSummary {
  let paid = 0n;
  let commission = 0n;
  let delegators = 0n;
  let epochs = 0;
  let notRecorded = 0;
  for (const item of items) {
    if (!item.recorded) {
      notRecorded += 1;
      continue;
    }
    epochs += 1;
    if (item.reward === null) continue;
    paid += BigInt(item.reward.amount);
    commission += BigInt(item.reward.commission);
    delegators += BigInt(item.reward.delegators);
  }
  return { epochs, notRecorded, paid: paid.toString(), commission: commission.toString(), delegators: delegators.toString() };
}

/** Where an account's recorded rewards begin, in words. */
export function recordedFrom(epoch: number): string {
  return epoch === 0
    ? "No epoch's payout has been recorded yet, so none can be shown."
    : `Payouts are recorded from epoch ${epoch.toLocaleString("en-US")}; an earlier epoch's are not, and are not shown as zero.`;
}
