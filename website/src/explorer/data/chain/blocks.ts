import type { Block, BlockSummary, TxSummary, ValidatorRef } from "../../model/types";
import { chainPath } from "./client";
import type { ChainClient } from "./client";
import type { Directory } from "./directory";
import { commitTally } from "./network";
import { readIndexTx, summaryOf } from "./tx";
import type { Context } from "./tx";
import { arr, digits, hash64, iso, rec, rpcResult, str, uint } from "./wire";

/** CometBFT's /blockchain returns at most this many blocks a call. */
export const BLOCKS_PER_CALL = 20;
/** The transactions of one block that the block page lists. */
export const BLOCK_TX_LIMIT = 100;
const TX_FETCH_BATCH = 10;

function proposerOf(directory: Directory, address: unknown): ValidatorRef | null {
  return directory.byConsensus.get(str(address, "proposer address", 64).toUpperCase())?.ref ?? null;
}

function summaryFromMeta(meta: unknown, directory: Directory): BlockSummary {
  const m = rec(meta, "block");
  const header = rec(m.header, "block header");
  return {
    height: uint(header.height, "block height"),
    hash: hash64(rec(m.block_id, "block id").hash, "block hash"),
    time: iso(header.time, "block time"),
    proposer: proposerOf(directory, header.proposer_address),
    txCount: uint(m.num_txs, "block transaction count"),
  };
}

/** The newest `count` blocks ending at `head`, newest first. */
export async function loadRecentBlocks(client: ChainClient, directory: Directory, head: number, count: number): Promise<BlockSummary[]> {
  const out: BlockSummary[] = [];
  let max = head;
  while (out.length < count && max >= 1) {
    const min = Math.max(1, max - Math.min(BLOCKS_PER_CALL, count - out.length) + 1);
    const metas = arr(rpcResult(await client.get(`blocks?min_height=${min}&max_height=${max}`), "recent blocks").block_metas, "recent blocks");
    const page = metas.map((m) => summaryFromMeta(m, directory));
    page.sort((a, b) => b.height - a.height);
    out.push(...page);
    max = min - 1;
  }
  return out;
}

interface IndexBlock {
  hash: string;
  txCount: number;
  txHashes: string[];
  gasUsed: number;
  burned: string;
}

function readIndexBlock(raw: unknown): IndexBlock {
  const b = rec(raw, "indexed block");
  return {
    hash: hash64(b.hash, "block hash"),
    txCount: uint(b.tx_count, "block transaction count"),
    txHashes: arr(b.tx_hashes, "block transactions").map((h) => hash64(h, "transaction hash")),
    gasUsed: uint(b.gas_used, "block gas"),
    burned: digits(b.burned, "block burn"),
  };
}

async function loadTxs(client: ChainClient, ctx: Context, hashes: readonly string[]): Promise<TxSummary[]> {
  const out: TxSummary[] = [];
  for (let i = 0; i < hashes.length; i += TX_FETCH_BATCH) {
    const batch = await Promise.all(
      hashes.slice(i, i + TX_FETCH_BATCH).map(async (h) => summaryOf(readIndexTx(await client.get(chainPath("index", "txs", h.toLowerCase()))), ctx)),
    );
    out.push(...batch);
  }
  return out;
}

/**
 * One block: its header from CometBFT, its gas, burn and transactions from the
 * indexer, and its signatures from the next block's last commit (null for the head).
 */
export async function loadBlock(client: ChainClient, directory: Directory, ctx: Context, height: number, head: number): Promise<Block> {
  const [headerRaw, indexRaw, nextRaw] = await Promise.all([
    client.get(`block?height=${height}`),
    client.get(chainPath("index", "blocks", String(height))),
    height < head ? client.get(`block?height=${height + 1}`) : Promise.resolve(null),
  ]);
  const block = rec(rpcResult(headerRaw, "block").block, "block");
  const header = rec(block.header, "block header");
  const indexed = readIndexBlock(indexRaw);
  const signatures = nextRaw === null ? null : commitTally(rec(rpcResult(nextRaw, "next block").block, "next block"));
  return {
    height: uint(header.height, "block height"),
    hash: indexed.hash,
    time: iso(header.time, "block time"),
    proposer: proposerOf(directory, header.proposer_address),
    txCount: indexed.txCount,
    gasUsed: indexed.gasUsed,
    burned: indexed.burned,
    signatures,
    txs: await loadTxs(client, ctx, indexed.txHashes.slice(0, BLOCK_TX_LIMIT)),
  };
}
