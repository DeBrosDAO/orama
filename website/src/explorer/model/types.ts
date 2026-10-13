/**
 * The explorer's domain model. Everything the UI knows about the chain is one
 * of these types, and every data source produces exactly these. A figure the
 * chain cannot give is null (or absent), never a made-up value. Nothing here mentions CometBFT,
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
  /** The operator's wallet address (orama1…); the validator's page is /validator/<this>. */
  operator: string;
}

export type TxStatus = { ok: true } | { ok: false; reason: string };

export type StorageVisibility = "private" | "public";

/** One piece of a sentence, in reading order, so the UI can link the names. */
export type SentencePart =
  | { kind: "text"; text: string }
  | { kind: "wallet"; ref: WalletRef }
  | { kind: "validator"; ref: ValidatorRef }
  | { kind: "amount"; norama: string };

/** The activity filter a message counts under. */
export type MessageCategory = "transfers" | "staking" | "storage" | "other";

/** One structured fact a message states. The value is chain-written text, already cleaned and bounded. */
export interface MessageField {
  label: string;
  value: string;
  /** "norama" values are integers shown as ORAMA; "address" values are shown in monospace. */
  kind: "text" | "norama" | "address";
}

export type TxMessage =
  | { type: "send"; from: WalletRef; to: WalletRef; amount: Norama }
  | { type: "delegate"; delegator: WalletRef; validator: ValidatorRef; amount: Norama }
  | { type: "undelegate"; delegator: WalletRef; validator: ValidatorRef; amount: Norama }
  | { type: "claim_rewards"; delegator: WalletRef; validator: ValidatorRef; amount: Norama }
  | {
      type: "storage_deal";
      owner: WalletRef;
      /** Null: a deal's providers are assigned after the transaction. */
      provider: WalletRef | null;
      /** The escrow: price per epoch times replicas times epochs. */
      amount: Norama;
      replicas: number;
      visibility: StorageVisibility;
    }
  /**
   * Any other first-party message: its sentence and facts come from the
   * message alone. `done` reads when the transaction succeeded, `tried` when
   * the chain refused it; neither says more than the message does.
   */
  | {
      type: "action";
      typeUrl: string;
      /** The module that owns the message: "Nodes", "Storage". */
      module: string;
      /** The short name of the action: "Register node". */
      tag: string;
      category: MessageCategory;
      actor: WalletRef | null;
      done: SentencePart[];
      tried: SentencePart[];
      /** The ORAMA the message states, when it states one. */
      amount: Norama | null;
      fields: MessageField[];
    }
  /** Any message the explorer has no decoder for. Never a blank row. */
  | { type: "unknown"; typeUrl: string; signer: WalletRef | null };

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
  /** Null for a transaction with no signature (a shielded one). */
  signer: WalletRef | null;
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
  /** Share (0-100) of this week's successful transfers this one is larger than. Null when the source cannot tell. */
  amountPercentile: number | null;
  /** How many times the signer paid this receiver before. Null when the source cannot tell. */
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
  /** Null when the proposer is not in the validator list (it left the set). */
  proposer: ValidatorRef | null;
  txCount: number;
}

export interface Block extends BlockSummary {
  gasUsed: number;
  burned: Norama;
  /** Who signed this block. Null for the head: its signatures arrive in the next block. */
  signatures: { signed: number; total: number } | null;
  /** The transactions, in block order. At most `txs.length` of `txCount` when a source shows a prefix. */
  txs: TxSummary[];
}

/** Why the chain indexer cannot move forward, while it cannot (chain/indexer/stuck.go). */
export interface IndexStuck {
  /** The block it cannot index. */
  height: number;
  reason: string;
  /** The node no longer keeps the state of a height the block needs, which no retry changes. */
  statePruned: boolean;
  failures: number;
}

/**
 * Where the chain indexer stands against the chain: the last block it indexed (cursor) and the
 * chain's tip. `lag` is the tip less the cursor, 0 when it has caught up. The history on the pages
 * that read the index (transaction lists, epochs, rewards) is as of the cursor.
 */
export interface IndexStatus {
  startHeight: number;
  cursor: number;
  tip: number;
  lag: number;
  /** The first block the epoch, supply and validator aggregates cover; 0 until they have begun. */
  aggregatesFrom: number;
  stuck: IndexStuck | null;
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
  /** Where the chain indexer stands, as of the same read: the history on the page is as of its cursor. */
  index: IndexStatus;
  transactions24h: number;
  /** Percent change against the 24 hours before, null when there is no baseline. */
  transactionsChangePct: number | null;
  /** Hourly counts, oldest first. */
  transactionsSeries: number[];
  /** Transactions the chain refused (after their fee was settled) in the last 24 hours. */
  failed24h: number;
  burned24h: Norama;
}

export type ActivityFilter = "all" | "transfers" | "staking" | "storage" | "failed";

export type WalletFilter = "all" | "in" | "out" | "staking" | "storage" | "failed";

export interface WalletBalance {
  available: Norama;
  staked: Norama;
  unbonding: Norama;
  /** available + staked + unbonding. */
  total: Norama;
}

/** Facts the wallet page turns into its plain-words summary. */
export interface WalletFacts {
  /** Null for a wallet that holds funds and has no transaction yet. */
  firstSeen: string | null;
  lastActive: string | null;
  txCount: number;
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

export type ValidatorStatus = "bonded" | "unbonding" | "unbonded";

export interface Validator {
  ref: ValidatorRef;
  type: "committee" | "community";
  /** Share of voting power, 0 to 1. */
  power: number;
  jailed: boolean;
  status: ValidatorStatus;
  /** The commission rate the validator takes from its delegators' rewards, 0 to 1. */
  commission: number;
  /** Everything bonded to the validator: its own bond and its delegators'. */
  tokens: Norama;
}

/** x/power's per-operator cap on voting power (the Lambda and Params queries). */
export interface PowerCap {
  /** The cap in force now, as a share of total power, 0 to 1. */
  current: number;
  /** The cap while at most `stepDownCount` validators are active. */
  normal: number;
  /** The cap once more than `stepDownCount` validators are active. */
  reduced: number;
  stepDownCount: number;
}

/** What drives lambda, the share of voting power that comes from stake instead of the founding committee. */
export interface Handover {
  /** 0 to 1; only ever rises. */
  lambda: number;
  /** Bonded stake against the stake at which lambda reaches 1 (x/power bootstrap_exit_stake). */
  stake: { bonded: Norama; exit: Norama };
  /** The current epoch against the epoch by which lambda reaches 1 whatever is staked (bootstrap_deadline_epochs). */
  epochs: { current: number; deadline: number };
  /** Until the validator-count gate is met once, lambda is held at or below this, 0 to 1. */
  preGateCap: number;
  cap: PowerCap;
}

export interface ValidatorSet {
  /** How much of voting power comes from stake rather than the founding committee, 0 to 1. */
  lambda: number;
  /** The smallest number of validators that together hold a third of the power. */
  nakamoto: number;
  totalStaked: Norama;
  jailed: number;
  validators: Validator[];
  handover: Handover;
}

/** The operator a validator's stake counts toward under x/power's cap. */
export type PowerOperator = { kind: "registered"; address: string } | { kind: "unlinked" };

export interface ValidatorDetail {
  validator: Validator;
  website: string;
  details: string;
  commission: {
    rate: number;
    maxRate: number;
    /** The most the rate may change in one day. */
    maxChangePerDay: number;
    /** Null when the rate has never been changed since the validator was created. */
    updatedAt: string | null;
  };
  minSelfDelegation: Norama;
  /** What the operator's own wallet has bonded to the validator; "0" when it has none. */
  selfBond: Norama;
  /** Everything bonded to the validator less the self-bond. */
  delegatedByOthers: Norama;
  /** When the validator finishes unbonding, or null. */
  unbondsAt: string | null;
  /** x/power's view: the CometBFT power and the operator the cap counts the validator under. Null when x/power knows no such validator. */
  power: { cometPower: string; operator: PowerOperator } | null;
}

/** One closed epoch of one validator, from the chain indexer. */
export interface ValidatorEpoch {
  epoch: number;
  /** CometBFT voting power in that epoch, in the chain's integer units (not a share). */
  cometPower: number;
  signedBlocks: number;
  missedBlocks: number;
  /** Signed over signed plus missed, 0 to 1; null when the epoch carried no commit for it. */
  uptime: number | null;
  slashes: number;
  slashedBurned: Norama;
  jailed: boolean;
  status: ValidatorStatus;
}

export interface SlashEvent {
  height: number;
  time: string;
  epoch: number;
  /** x/slashing's own reason: double_sign, missing_signature or unspecified. */
  reason: string;
  power: number;
  burned: Norama;
}

export interface JailPeriod {
  /** Null when the validator was jailed before the index's first block. */
  jailedHeight: number | null;
  jailedTime: string | null;
  reason: string;
  /** Null while it is still jailed. */
  unjailedHeight: number | null;
  unjailedTime: string | null;
}

/** A validator's history from the chain indexer: newest first. */
export interface ValidatorHistory {
  epochs: ValidatorEpoch[];
  slashes: SlashEvent[];
  jails: JailPeriod[];
}

/** One validator's signing in the newest closed epoch, from the chain indexer. */
export interface ValidatorSigning {
  /** The operator wallet (orama1…), as Validator.ref.operator. */
  operator: string;
  epoch: number;
  uptime: number | null;
  missedBlocks: number;
  slashes: number;
}

export interface OperatorGroup {
  operator: PowerOperator;
  validators: Validator[];
  /** The group's combined share of voting power, 0 to 1. */
  power: number;
}

export interface OperatorGroups {
  /** Largest combined power first. */
  groups: OperatorGroup[];
  /** Active validators whose operator was not read, because only the largest are looked up. */
  unresolved: number;
}

export interface Head {
  height: number;
  time: string;
  /** The chain id the gateway reports. A reset gives the same network a new one, and a lower height. */
  chainId: string;
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
