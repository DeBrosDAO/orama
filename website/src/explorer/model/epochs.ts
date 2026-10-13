import type { Norama, ValidatorRef } from "./types";

/**
 * What every closed epoch row holds, with its figures or without (chain/indexer/agg_model.go,
 * EpochRow): where it began and ended, and the payout split. The split comes from the closing
 * block's events, not from state, so an epoch whose state was pruned still has it.
 */
export interface EpochSpan {
  epoch: number;
  startHeight: number;
  endHeight: number;
  /** ISO times: when the epoch began and when the block that closed it was made. */
  startTime: string;
  endTime: string;
  blocks: number;
  /** The index began inside this epoch: its totals cover only the blocks the index saw. */
  partial: boolean;
  /**
   * The payout is known validator by validator (the chain reported it when the epoch closed). False
   * for an epoch that closed before it did: the three figures below are then null, and the
   * explorer says "not recorded", never zero.
   */
  rewardsRecorded: boolean;
  /** The validators' commission on what they were paid; null unless recorded. */
  rewardsCommission: Norama | null;
  /** The operators' whole share, commission included; null unless recorded. */
  rewardsOperatorShare: Norama | null;
  /** What delegators other than the operators were credited; null unless recorded. */
  rewardsDelegators: Norama | null;
}

/**
 * An epoch whose figures the index read. Every amount is the chain's own figure in norama. Minted
 * and burned amounts are what happened during the epoch's blocks, not what the schedule allows.
 */
export interface RecordedEpochRow extends EpochSpan {
  unavailable: false;
  /** The schedule's ceiling for the epoch (x/emission). */
  maxMintable: Norama;
  /** The validator and delegator share, minted when the epoch closed. */
  mintedValidators: Norama;
  /** Storage and relay together: the chain keeps one running total for both. */
  mintedService: Norama;
  mintedDevelopment: Norama;
  mintedFaucet: Norama;
  mintedTotal: Norama;
  burned: Norama;
  feesCollected: Norama;
  feesBurned: Norama;
  feesDistributed: Norama;
  /** What x/power credited to earnings accounts when the epoch closed. */
  rewardsCredited: Norama;
  /** What x/power bonded into bootstrap committee members' self-delegations instead. */
  rewardsForceBonded: Norama;
}

/**
 * An epoch the index could not read: the node had pruned the state its figures come from (or it is
 * the epoch after such a one, whose change in the chain's totals would also hold the one before).
 * It has no figure at all, which is not zero, and `reason` is the indexer's own account of why.
 */
export interface UnavailableEpochRow extends EpochSpan {
  unavailable: true;
  reason: string;
}

/** One closed epoch: with its figures, or without them and saying why. */
export type EpochRow = RecordedEpochRow | UnavailableEpochRow;

/** Whether the epoch has its figures; narrows the row to the one that does. */
export function hasFigures(row: EpochRow): row is RecordedEpochRow {
  return !row.unavailable;
}

/** One page of a list the indexer pages through. The indexer does not count, so `hasMore` is "this page was full". */
export interface IndexPage<T> {
  items: T[];
  page: number;
  limit: number;
  hasMore: boolean;
}

export interface EpochListQuery {
  /** 1 to 1000. */
  page: number;
  /** 1 to 100. */
  limit: number;
}

/** One validator in one closed epoch (chain/indexer/agg_model.go, ValidatorEpoch). */
export interface EpochValidator {
  ref: ValidatorRef;
  /** CometBFT voting power in that epoch. */
  power: number;
  signedBlocks: number;
  missedBlocks: number;
  /** Signed over signed plus missed, in basis points; 0 to 10000. */
  uptimeBps: number;
  slashes: number;
  slashedBurned: Norama;
  jailed: boolean;
  /** x/staking's name for the status: bonded, unbonding or unbonded. */
  status: string;
}

/**
 * The supply breakdown after an epoch's closing block (chain/indexer/agg_model.go, SupplyPoint).
 * The index keeps none for an epoch that closed unavailable, so the epochs of a list of points
 * can skip numbers; the skipped ones are gaps, not zeros.
 */
export interface SupplyPoint {
  epoch: number;
  height: number;
  time: string;
  totalSupply: Norama;
  mintedToDate: Norama;
  burnedToDate: Norama;
  genesisSupply: Norama;
  bonded: Norama;
  unbonding: Norama;
  earningsPools: Norama;
  storageEscrow: Norama;
  otherProtocol: Norama;
  circulating: Norama;
}

/**
 * The epoch in progress, read from x/emission (the current epoch and its parameters) and the
 * head at the same moment. An epoch closes when BOTH the duration and the block count have passed.
 */
export interface EpochClock {
  epoch: number;
  /** ISO time the epoch in progress started (BFT time). */
  startedAt: string;
  durationSeconds: number;
  minBlocks: number;
  /** Blocks counted in the epoch when it was read. */
  blocksInEpoch: number;
  /** The chain height at that read, to advance the count with the head. */
  readAtHeight: number;
}
