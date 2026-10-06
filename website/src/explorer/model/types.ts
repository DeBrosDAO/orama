/**
 * The explorer's domain model. Everything the UI knows about the chain is one
 * of these types, and every data source (the demo world today, an indexer
 * adapter later) produces exactly these. Nothing here mentions CometBFT,
 * protobuf or the gateway.
 *
 * Amounts are base-unit integers in strings (1 ORAMA = 10^9 norama): they do
 * not fit a JS number, and a string survives JSON unchanged. Times are ISO
 * 8601 UTC strings.
 */

/** A non-negative base-unit integer, e.g. "12500000000" is 12.5 ORAMA. */
export type Norama = string;

/** A base-unit integer that may carry a leading "-". */
export type SignedNorama = string;

export interface WalletRef {
  address: string;
  /** A human name, when one is known: the foundation, a validator operator. */
  label?: string;
  /** Set only for labels the network itself vouches for. */
  verified?: boolean;
}

export interface ValidatorRef {
  moniker: string;
  /** The operator's wallet address; the validator's page is that wallet. */
  operator: string;
}

export type TxStatus = { ok: true } | { ok: false; reason: string };

export type StorageVisibility = "private" | "public";

export type TxMessage =
  | { type: "send"; from: WalletRef; to: WalletRef; amount: Norama }
  | { type: "delegate"; delegator: WalletRef; validator: ValidatorRef; amount: Norama }
  | { type: "undelegate"; delegator: WalletRef; validator: ValidatorRef; amount: Norama }
  | { type: "claim_rewards"; delegator: WalletRef; validator: ValidatorRef; amount: Norama }
  | {
      type: "storage_deal";
      owner: WalletRef;
      provider: WalletRef;
      amount: Norama;
      replicas: number;
      visibility: StorageVisibility;
    }
  /** Any message the explorer has no decoder for. Never a blank row. */
  | { type: "unknown"; typeUrl: string; signer: WalletRef };

export type TxMessageType = TxMessage["type"];

export interface Fee {
  /** Destroyed, not paid to anyone. */
  burned: Norama;
  /** Optional payment to the block proposer. */
  tip: Norama;
  gasUsed: number;
  gasWanted: number;
}

export interface TxSummary {
  /** 64 upper-case hex characters. */
  hash: string;
  height: number;
  time: string;
  status: TxStatus;
  signer: WalletRef;
  messages: TxMessage[];
  fee: Fee;
}

/** Someone whose balance a transaction changed, or a system pool. */
export type BalanceParty =
  | { kind: "wallet"; ref: WalletRef }
  | { kind: "system"; name: "burned" | "minted" | "staked" | "unbonding" | "storage_escrow" };

export interface BalanceChange {
  party: BalanceParty;
  /** Null for system pools, which have no balance worth showing. */
  before: Norama | null;
  after: Norama | null;
  delta: SignedNorama;
}

export interface TxEvent {
  type: string;
  attributes: Record<string, string>;
}

/** What a reader needs to judge whether a transaction is unusual. */
export interface TxContext {
  /** Share (0-100) of this week's successful transfers this one is larger than. Null for non-transfers. */
  amountPercentile: number | null;
  /** How many times the signer paid this receiver before. Null for non-transfers. */
  priorBetweenParties: number | null;
  previousFromSigner: TxSummary | null;
  otherInBlock: number;
}

export interface TxDetail extends TxSummary {
  memo: string;
  balanceChanges: BalanceChange[];
  events: TxEvent[];
  /** The canonical JSON form of the transaction body. */
  rawJson: string;
  context: TxContext;
}

export interface BlockSummary {
  height: number;
  hash: string;
  time: string;
  proposer: ValidatorRef;
  txCount: number;
}

export interface Block extends BlockSummary {
  gasUsed: number;
  burned: Norama;
  signatures: { signed: number; total: number };
  txs: TxSummary[];
}

export interface EpochInfo {
  number: number;
  /** 0 to 1. */
  progress: number;
  endsAt: string;
}

export interface NetworkSnapshot {
  chainId: string;
  height: number;
  blockTimeSeconds: number;
  validatorsSigning: number;
  validatorsTotal: number;
  epoch: EpochInfo;
  supply: Norama;
  /** Norama per gas unit. */
  baseFee: number;
  transactions24h: number;
  /** Percent change against the 24 hours before, null when there is no baseline. */
  transactionsChangePct: number | null;
  /** Hourly counts, oldest first. */
  transactionsSeries: number[];
  activeWallets24h: number;
  newWallets24h: number;
  burned24h: Norama;
}

export type ActivityFilter = "all" | "transfers" | "staking" | "storage" | "failed";

export type WalletFilter = "all" | "in" | "out" | "staking" | "storage" | "failed";

export interface WalletBalance {
  available: Norama;
  staked: Norama;
  unbonding: Norama;
  /** Rewards accrued and not yet claimed. Not part of the total. */
  claimableRewards: Norama;
  /** available + staked + unbonding. */
  total: Norama;
}

/** Facts the wallet page turns into its plain-words summary. */
export interface WalletFacts {
  firstSeen: string;
  lastActive: string;
  txCount: number;
  failedLast7d: number;
  topSender: WalletRef | null;
  topDelegate: ValidatorRef | null;
}

export interface WalletProfile {
  ref: WalletRef;
  /** "Validator operator", "Storage provider", "Foundation"; empty for a plain wallet. */
  roles: string[];
  balance: WalletBalance;
  facts: WalletFacts;
}

export type ActivityDirection = "in" | "out";

export interface ActivityItem {
  hash: string;
  time: string;
  status: TxStatus;
  /** The message this row is about, seen from the wallet's side. */
  message: TxMessage;
  direction: ActivityDirection;
  /** From this wallet's side: negative when it paid. */
  amount: SignedNorama;
  counterparty: WalletRef | null;
}

export interface ActivityQuery {
  filter: WalletFilter;
  /** Only transactions with this counterparty. */
  counterparty: string | null;
  cursor: string | null;
  limit: number;
}

export interface Page<T> {
  items: T[];
  /** Pass back as `cursor` for the next page; null when there is none. */
  nextCursor: string | null;
}

export interface Counterparty {
  ref: WalletRef;
  txCount: number;
  /** Received minus sent, from the wallet's side. */
  net: SignedNorama;
  /** Received plus sent. */
  volume: Norama;
}

export type BalanceRange = "7d" | "30d" | "all";

export interface BalancePoint {
  time: string;
  total: Norama;
}

export type UptimeDay = "ok" | "partial" | "missed";

export interface Validator {
  ref: ValidatorRef;
  type: "committee" | "community";
  /** Share of voting power, 0 to 1. */
  power: number;
  jailed: boolean;
  /** Last 30 days, oldest first. */
  uptimeDays: UptimeDay[];
  uptimePct: number;
}

export interface ValidatorSet {
  /** How much of voting power comes from stake rather than the founding committee, 0 to 1. */
  lambda: number;
  /** The smallest number of validators that together hold a third of the power. */
  nakamoto: number;
  totalStaked: Norama;
  delegators: number;
  jailedLast30d: number;
  validators: Validator[];
}

export interface Head {
  height: number;
  time: string;
}

/**
 * Real entities the landing page can offer as "try this". Each is null when
 * the chain has nothing suitable yet (no transfers, no wallets), so a fresh
 * chain shows no example instead of failing.
 */
export interface ExampleTargets {
  latestTxHash: string | null;
  busyWalletAddress: string | null;
}
