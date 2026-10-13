import type { Head, IndexStatus, NetworkSnapshot, BlockSummary } from "../../model/types";
import { ChainReadError, chainPath, withQuery } from "./client";
import type { ChainClient } from "./client";
import { blockTimeSeconds, readHours, transactionTotals } from "./network";
import type { Hourly } from "./network";
import { nextCursor } from "./paging";
import { clean } from "./text";
import { readIndexTx } from "./tx";
import type { IndexTx } from "./tx";
import { arr, bool, digits, hash64, iso, optStr, rec, uint } from "./wire";

/**
 * The home page's data in one read: GET /v1/chain/index/home (chain/indexer/home.go). The indexer reuses a snapshot for two seconds while
 * the index stays at one block, and readers who arrive during a build share it. It holds what the page shows: the index's position
 * and lag, the newest blocks and transactions, 48 hours of transaction counts, and the epoch in
 * progress, the supply and the base fee at the chain's tip.
 */

const MAX_WALLET = 120;
const MAX_REASON = 512;

/** The most recent transactions and blocks the home answer holds (chain/indexer HomeTxs and HomeBlocks). */
export const HOME_TXS = 12;
export const HOME_BLOCKS = 24;

export interface HomeEpoch {
  number: number;
  startTime: string;
  durationSeconds: number;
  endsAt: string;
}

export interface Home {
  status: IndexStatus;
  blocks: BlockSummary[];
  /** Per-block commit tallies, newest first, as the blocks above. */
  commits: { signed: number; size: number }[];
  txs: IndexTx[];
  hours: Hourly[];
  epoch: HomeEpoch;
  supply: string;
  baseFee: number;
  examples: { latestTx: string | null; busyAccount: string | null };
}

export function readIndexStatus(raw: unknown): IndexStatus {
  const s = rec(raw, "index status");
  const stuck = s.stuck === undefined || s.stuck === null ? null : rec(s.stuck, "index stuck record");
  const cursor = uint(s.cursor, "index cursor");
  const tip = uint(s.tip, "chain tip");
  return {
    startHeight: uint(s.start_height, "index start height"),
    cursor,
    tip,
    lag: s.lag === undefined ? Math.max(0, tip - cursor) : uint(s.lag, "index lag"),
    aggregatesFrom: uint(s.aggregates_from, "aggregates start"),
    stuck:
      stuck === null
        ? null
        : {
            height: uint(stuck.height, "stuck height"),
            reason: clean(optStr(stuck.reason, "stuck reason", MAX_REASON), MAX_REASON),
            statePruned: bool(stuck.state_pruned, "stuck pruned flag"),
            failures: uint(stuck.failures, "stuck failures"),
          },
  };
}

function readBlock(raw: unknown): { block: BlockSummary; commit: { signed: number; size: number } } {
  const b = rec(raw, "indexed block");
  return {
    block: {
      height: uint(b.height, "block height"),
      hash: hash64(b.hash, "block hash"),
      time: iso(b.time, "block time"),
      proposer: null,
      txCount: uint(b.tx_count, "block transaction count"),
    },
    commit: { signed: uint(b.commit_signed, "block commit votes"), size: uint(b.commit_size, "block commit size") },
  };
}

function readEpoch(raw: unknown): HomeEpoch {
  const e = rec(raw, "epoch");
  const durationSeconds = uint(e.duration_seconds, "epoch length");
  if (durationSeconds <= 0) throw new ChainReadError("The chain sent a malformed epoch.");
  return { number: uint(e.number, "epoch number"), startTime: iso(e.start_time, "epoch start"), durationSeconds, endsAt: iso(e.ends_at, "epoch end") };
}

function optHash(value: unknown, what: string): string | null {
  const text = optStr(value, what, MAX_WALLET);
  return text === "" ? null : text;
}

export function readHome(raw: unknown): Home {
  const h = rec(raw, "home");
  const blocks = arr(h.blocks, "home blocks").map(readBlock);
  const baseFee = Number(digits(h.base_fee, "base fee"));
  if (!Number.isSafeInteger(baseFee)) throw new ChainReadError("The chain sent a malformed base fee.");
  const examples = rec(h.examples, "home examples");
  return {
    status: readIndexStatus(h.status),
    blocks: blocks.map((b) => b.block),
    commits: blocks.map((b) => b.commit),
    txs: arr(h.txs, "home transactions").map(readIndexTx),
    hours: readHours(h),
    epoch: readEpoch(h.epoch),
    supply: digits(h.supply, "supply"),
    baseFee,
    examples: { latestTx: optHash(examples.latest_tx, "latest transaction"), busyAccount: optHash(examples.busy_account, "busiest account") },
  };
}

/** The one read. */
export async function loadHome(client: ChainClient): Promise<Home> {
  return readHome(await client.get(chainPath("index", "home")));
}

/** The numbers of the home page's stat tiles, from the home answer and the head the page is showing. */
export function networkOf(home: Home, head: Head): NetworkSnapshot {
  const newest = home.commits[0] ?? { signed: 0, size: 0 };
  const start = Date.parse(home.epoch.startTime);
  const progress = Math.min(1, Math.max(0, (Date.parse(head.time) - start) / (home.epoch.durationSeconds * 1000)));
  return {
    chainId: head.chainId,
    height: head.height,
    blockTimeSeconds: blockTimeSeconds(home.blocks.map((b) => b.time)),
    validatorsSigning: newest.signed,
    validatorsTotal: newest.size,
    epoch: { number: home.epoch.number, progress, endsAt: home.epoch.endsAt },
    supply: home.supply,
    baseFee: home.baseFee,
    index: home.status,
    ...transactionTotals(home.hours),
  };
}

/** The index's position alone, for pages that show the lag. */
export async function loadIndexStatus(client: ChainClient): Promise<IndexStatus> {
  return readIndexStatus(await client.get(chainPath("index", "status")));
}

/**
 * The newest blocks, a page at a time, as the block list route serves them: the first page by
 * number, each next one by the cursor the page before it named (`after`).
 */
export async function loadBlockPage(client: ChainClient, after: string | null, limit: number): Promise<{ blocks: BlockSummary[]; next: string | null }> {
  const where = after === null ? { page: 1, limit } : { cursor: after, limit };
  const raw = rec(await client.get(withQuery(chainPath("index", "blocks"), where)), "block list");
  const blocks = arr(raw.blocks, "block list").map((b) => readBlock(b).block);
  return { blocks, next: nextCursor(raw, blocks.length, limit) };
}
