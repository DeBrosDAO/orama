import type { KeyPage } from "./paging";
import type { Norama, ValidatorRef } from "./types";

/**
 * What x/power paid for an epoch, as the chain reported it when the epoch closed (the
 * power_validator_reward and power_delegator_reward events; chain/indexer/agg_rewards.go). Every
 * figure is the chain's, in norama. An epoch that closed before the chain reported them is "not
 * recorded": the index has no per-validator figure for it and the explorer shows none.
 */

/** One validator's share of one epoch's payout. `operatorShare` plus `delegators` is `amount`. */
export interface ValidatorReward {
  epoch: number;
  ref: ValidatorRef;
  /** The validator's whole reward: what its operator and its delegators were due together. */
  amount: Norama;
  /** The part of the operator's share that is its commission rate on `amount`. */
  commission: Norama;
  /** The operator's whole share: the commission, its own cut as a delegator, and the flooring remainder. */
  operatorShare: Norama;
  /** What the delegators other than the operator were credited. */
  delegators: Norama;
  /** The part of the operator's share bonded into a founding-committee validator's own stake instead of credited. */
  forceBonded: Norama;
}

/** The validators paid in one closed epoch. When `recorded` is false the epoch's payout was not reported and `items` is empty. */
export interface EpochRewards {
  epoch: number;
  recorded: boolean;
  items: ValidatorReward[];
  page: number;
  limit: number;
  hasMore: boolean;
}

/** One closed epoch of a validator's history: whether its payout was recorded, and what the validator was paid (null: nothing). */
export interface ValidatorRewardEpoch {
  epoch: number;
  recorded: boolean;
  reward: ValidatorReward | null;
}

export interface ValidatorRewardHistory {
  items: ValidatorRewardEpoch[];
  page: number;
  limit: number;
  hasMore: boolean;
}

/** What an account was credited for one epoch by one validator. */
export interface AccountReward {
  epoch: number;
  validator: ValidatorRef;
  /** Whether the account was a delegator of the validator, or the validator's operator. */
  role: "delegator" | "operator";
  /** Credited to the account's earnings, or, for an operator, its whole share. */
  amount: Norama;
  /** The part of `amount` bonded into the operator's own stake instead of credited. */
  forceBonded: Norama;
}

/**
 * A page of what an account was credited by x/power, newest epoch first. Epochs before
 * `recordedFromEpoch` (all of them while it is 0) were not recorded. `nextKey` is the indexer's
 * cursor for the page after it.
 */
export interface AccountRewards extends KeyPage<AccountReward> {
  address: string;
  recordedFromEpoch: number;
}

/** The balance of an account's earnings: what the protocol has paid it that it has not yet moved to its public balance. */
export interface Earnings {
  address: string;
  balance: Norama;
}

export interface RewardListQuery {
  /** 1 to 1000. */
  page: number;
  /** 1 to 100. */
  limit: number;
}
