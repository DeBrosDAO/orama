import type {
  ActivityFilter,
  ActivityItem,
  ActivityQuery,
  BalancePoint,
  BalanceRange,
  Block,
  BlockSummary,
  Counterparty,
  ExampleTargets,
  Head,
  NetworkSnapshot,
  Page,
  TxDetail,
  TxSummary,
  ValidatorSet,
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
  /** Where the data comes from, shown in the header so nobody mistakes a demo for a chain. */
  readonly origin: { kind: "demo" | "chain"; label: string };

  /** Chain-wide numbers as of the head. Rejects with an Error if they cannot be read. */
  getNetwork(): Promise<NetworkSnapshot>;
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
  /** Wallets this one dealt with, largest total volume first. */
  getCounterparties(address: string, limit: number): Promise<Counterparty[]>;
  /** Total balance over time inside the range, oldest first, ending now. */
  getBalanceHistory(address: string, range: BalanceRange): Promise<BalancePoint[]>;

  /** Every validator, in no particular order; the UI ranks them. */
  getValidators(): Promise<ValidatorSet>;

  /** Wallets and validators whose label contains the query, best match first. */
  searchLabels(query: string, limit: number): Promise<WalletRef[]>;

  /**
   * Call `listener` whenever the chain head advances. Returns the function
   * that stops it. The listener is not called for the current head.
   */
  subscribeHead(listener: (head: Head) => void): () => void;
  /** The current head, without subscribing. */
  getHead(): Promise<Head>;
}
