import type { EpochClock, EpochListQuery, EpochRow, EpochValidator, IndexPage, SupplyPoint } from "../model/epochs";
import type { AccountRewards, Earnings, EpochRewards, RewardListQuery, ValidatorRewardHistory } from "../model/rewards";
import type { NodeDetail, NodeSet } from "../model/nodes";
import type { KeyQuery } from "../model/paging";
import type { ArchiveHealth, DealList, DealReplica, SettlementQueue } from "../model/storage";
import type { TorRelays } from "../model/tor";
import type { AssetsSource } from "./assets-source";
import type {
  ActivityFilter,
  ActivityItem,
  ActivityQuery,
  Block,
  BlockSummary,
  Counterparty,
  ExampleTargets,
  Head,
  IndexStatus,
  NetworkSnapshot,
  OperatorGroups,
  Page,
  TxDetail,
  TxSummary,
  ValidatorDetail,
  ValidatorHistory,
  ValidatorSet,
  ValidatorSigning,
  WalletProfile,
  WalletRef,
} from "../model/types";

/**
 * The largest list any read returns. Implementations clamp a larger `limit`
 * to this rather than rejecting, and treat a negative or NaN limit as 0.
 */
export const MAX_LIST_LIMIT = 500;

/** The limit an implementation actually applies: an integer in [0, MAX_LIST_LIMIT]. */
export function clampLimit(limit: number): number {
  if (Number.isNaN(limit) || limit <= 0) return 0;
  return Math.min(Math.floor(limit), MAX_LIST_LIMIT);
}

/**
 * The single boundary between the explorer UI and the chain. The UI imports
 * this interface and nothing else about where data comes from.
 *
 * Contract every implementation must keep:
 *  - A lookup for something that does not exist resolves to null (getTx,
 *    getBlock, getWallet). It never rejects for "not found" and never returns
 *    a made-up empty record.
 *  - A real failure (network, indexer down, a cursor this source never
 *    issued) rejects with an Error whose message the UI can show to a reader.
 *  - Ordering is stated per method below; there is no global default.
 *  - Every `limit` is clamped by the implementation to [0, MAX_LIST_LIMIT]:
 *    a negative or NaN limit yields an empty list, a larger one yields
 *    MAX_LIST_LIMIT items. A caller never needs its own upper bound.
 *  - Pagination is cursor-based. A cursor is opaque to the caller and only
 *    valid as returned by the same source.
 *  - The head is a finished block. Nothing above it is returned by any read:
 *    the block in progress, and its transactions, do not exist yet.
 */
export interface ExplorerDataSource {
  /** Where the data comes from, shown in the footer: the chain id of the network read. */
  readonly origin: { label: string };
  /** Tokens, NFTs, contracts, the market, governance and the shielded pool. */
  readonly assets: AssetsSource;

  /** Chain-wide numbers as of the head. Rejects with an Error if they cannot be read. */
  getNetwork(): Promise<NetworkSnapshot>;
  /** Where the chain indexer stands against the chain's tip. Rejects when the node runs no indexer. */
  getIndexStatus(): Promise<IndexStatus>;
  /**
   * Suggested pages to open from the home page. A field is null when nothing
   * suitable exists (no transfer yet, no wallet yet); it is never invented.
   */
  getExamples(): Promise<ExampleTargets>;

  /** Transactions, newest first. */
  getLatestActivity(filter: ActivityFilter, limit: number): Promise<TxSummary[]>;
  /** Blocks, newest first, ending at the head. */
  getRecentBlocks(limit: number): Promise<BlockSummary[]>;
  /** null for a height that is not an integer, below 1, or above the head. */
  getBlock(height: number): Promise<Block | null>;
  /** `hash` is 64 hex characters, any case. null when no such transaction exists. */
  getTx(hash: string): Promise<TxDetail | null>;

  /** null for a wallet with no transactions yet: a wallet page exists after its first one. */
  getWallet(address: string): Promise<WalletProfile | null>;
  /**
   * One row per transaction the wallet took part in, newest first. A row
   * describes the transaction's FIRST message from this wallet's side; a
   * transaction that carries several messages still appears once, and the
   * rest of its messages are on the transaction page.
   * `counterparty` is the other wallet. It is null for staking rows unless
   * that wallet is one whose page contains the transaction, because a link to
   * a page that does not show the transaction would mislead. Rejects with an
   * Error for a cursor this source did not issue.
   */
  getWalletActivity(address: string, query: ActivityQuery): Promise<Page<ActivityItem>>;
  /** Wallets this one dealt with, largest total volume first. A source may read them from the wallet's most recent transactions only. */
  getCounterparties(address: string, limit: number): Promise<Counterparty[]>;

  /** Every validator, in no particular order; the UI ranks them. */
  getValidators(): Promise<ValidatorSet>;
  /** One validator by its operator wallet (orama1…); null when no validator has that operator. */
  getValidator(address: string): Promise<ValidatorDetail | null>;
  /**
   * A validator's closed epochs, slashes and jail periods, newest first, from
   * the chain indexer: it rejects when the node has none. Null when the index
   * has not seen the validator yet.
   */
  getValidatorHistory(address: string): Promise<ValidatorHistory | null>;
  /** Each validator's signing in the newest closed epoch, from the chain indexer; it rejects when the node has none. */
  getValidatorSigning(): Promise<ValidatorSigning[]>;
  /** The active validators grouped by the operator x/power counts their stake under. */
  getOperatorGroups(): Promise<OperatorGroups>;

  /** The epoch in progress and what closes it. Needs the chain only, not the index. */
  getEpochClock(): Promise<EpochClock>;
  /**
   * Closed emission epochs, newest first, a page at a time (page 1 to 1000, limit 1 to 100; both
   * are clamped). Rejects if the index cannot be read.
   */
  getEpochs(query: EpochListQuery): Promise<IndexPage<EpochRow>>;
  /** null for an epoch the index does not hold: one still open, one not reached, or one before the index began. */
  getEpoch(epoch: number): Promise<EpochRow | null>;
  /** The validators of a closed epoch, highest voting power first; null when the epoch is not closed in the index. */
  getEpochValidators(epoch: number, query: EpochListQuery): Promise<IndexPage<EpochValidator> | null>;
  /**
   * What a closed epoch paid each validator, as the chain reported it; `recorded` is false for an
   * epoch that closed before it did. Null when the epoch is not closed in the index.
   */
  getEpochRewards(epoch: number, query: RewardListQuery): Promise<EpochRewards | null>;
  /** A validator's closed epochs, newest first, each with what it was paid or "not recorded"; null when the index has not seen the validator. */
  getValidatorRewards(address: string, query: RewardListQuery): Promise<ValidatorRewardHistory | null>;
  /** What an account was credited by x/power as a delegator or as an operator, newest epoch first. */
  getAccountRewards(address: string, query: KeyQuery): Promise<AccountRewards>;
  /** The balance of an account's earnings, read from the chain (x/fees), not from the index. */
  getEarnings(address: string): Promise<Earnings>;
  /** The supply breakdown after each of the newest `limit` closed epochs (at most 100), newest first. */
  getSupplyHistory(limit: number): Promise<SupplyPoint[]>;
  /** The base fee now, in norama per gas. The chain keeps no history of it, and neither does the index. */
  getBaseFee(): Promise<number>;

  /** Every node x/nodes holds, whatever its status, with the module's parameters. */
  getNetworkNodes(): Promise<NodeSet>;
  /** One node by the id it was registered under; null when the chain holds no such node. */
  getNode(id: string): Promise<NodeDetail | null>;

  /** The newest storage deals, newest first, with x/storage's parameters. */
  getDeals(): Promise<DealList>;
  /** A deal's replicas (at most a dozen), one per slot. */
  getDealReplicas(dealId: number, replicas: number): Promise<DealReplica[]>;
  /** How many settlements (payments and penalties) wait for a block. */
  getSettlementQueue(): Promise<SettlementQueue>;
  /** What x/archive holds: the archived prefix, the retain height and the oldest ranges not archived yet. */
  getArchiveHealth(): Promise<ArchiveHealth>;

  /** The relays x/relay holds with its parameters, reporters and the newest closed epoch's settlement. */
  getTorRelays(): Promise<TorRelays>;

  /** Wallets and validators whose label contains the query, best match first. */
  searchLabels(query: string, limit: number): Promise<WalletRef[]>;

  /**
   * Call `listener` whenever the chain head advances. Returns the function
   * that stops it. The listener is not called for the current head. `onError`
   * is called each time the head cannot be read; the subscription goes on.
   */
  subscribeHead(listener: (head: Head) => void, onError: (error: Error) => void): () => void;
  /** The current head, without subscribing. */
  getHead(): Promise<Head>;
}
