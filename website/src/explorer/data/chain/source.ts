import { categoryOf } from "../../model/describe";
import type {
  ActivityFilter,
  ActivityItem,
  ActivityQuery,
  Block,
  BlockSummary,
  Counterparty,
  ExampleTargets,
  Head,
  NetworkSnapshot,
  Page,
  TxDetail,
  TxSummary,
  ValidatorRef,
  ValidatorSet,
  WalletProfile,
  WalletRef,
} from "../../model/types";
import { toError } from "../errors";
import { clampLimit } from "../source";
import type { ExplorerDataSource } from "../source";
import { BLOCKS_PER_CALL, loadBlock, loadRecentBlocks } from "./blocks";
import { ChainNotFound, chainPath, createClient, withQuery } from "./client";
import type { ChainClient, ChainFetch } from "./client";
import { loadDirectory } from "./directory";
import type { Directory } from "./directory";
import { loadNetwork, readHead } from "./network";
import { detailOf, readIndexTx, summaryOf, walletRef } from "./tx";
import type { Context, IndexTx } from "./tx";
import { loadValidatorSet } from "./validators";
import { loadActivity, loadCounterparties, loadWallet } from "./wallet";
import { arr, rec, uint } from "./wire";

/** How long a read validator directory is reused. Validators change in epochs, not in blocks. */
const DIRECTORY_TTL_MS = 30_000;
/** How often a page open in a visible tab asks for the head. Blocks come every few seconds. */
const HEAD_POLL_MS = 3_000;
/** The newest transactions one list read asks the indexer for (its maximum). */
const LATEST_WINDOW = 100;
/** The previous transaction from a signer is looked for among their newest transactions. */
const PREVIOUS_WINDOW = 100;
/** Where the data comes from, shown in the footer. */
const ORIGIN_LABEL = "the Orama chain";
const MAX_LABEL_QUERY = 64;
const MIN_LABEL_QUERY = 1;
const LABEL_RANK_PREFIX = 0;
const LABEL_RANK_INFIX = 1;

export interface ChainSourceOptions {
  fetcher?: ChainFetch;
  base?: string;
  /** The clock, for the directory cache. */
  now?: () => number;
}

function filterTx(tx: TxSummary, filter: ActivityFilter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "failed":
      return !tx.status.ok;
    case "transfers":
      return tx.messages.some((m) => categoryOf(m) === "transfers");
    case "staking":
      return tx.messages.some((m) => categoryOf(m) === "staking");
    case "storage":
      return tx.messages.some((m) => categoryOf(m) === "storage");
  }
}

function isBefore(a: IndexTx, b: IndexTx): boolean {
  return a.height < b.height || (a.height === b.height && a.index < b.index);
}

/**
 * The explorer's data source over the gateway's /v1/chain/ proxy: CometBFT for
 * blocks and the validator set, the node's REST API for balances and staking,
 * the Orama modules' queries for fees, epochs and power, and the chain indexer
 * for transactions. Nothing here is invented: a figure the chain cannot give
 * is left out of the model (see model/types.ts).
 */
export function createChainSource(options: ChainSourceOptions = {}): ExplorerDataSource {
  const client: ChainClient = createClient(options.fetcher, options.base);
  const now = options.now ?? Date.now;
  let cached: { at: number; directory: Promise<Directory> } | null = null;

  const directory = (): Promise<Directory> => {
    if (cached === null || now() - cached.at > DIRECTORY_TTL_MS) {
      const pending = loadDirectory(client);
      cached = { at: now(), directory: pending };
      pending.catch(() => {
        if (cached?.directory === pending) cached = null;
      });
    }
    return cached.directory;
  };

  const context = async (): Promise<Context> => {
    const dir = await directory();
    return {
      label: (address) => dir.byOperator.get(address)?.ref.moniker,
      validator: (operator): ValidatorRef => dir.byOperator.get(operator)?.ref ?? { moniker: operator, operator },
    };
  };

  const head = async (): Promise<Head> => {
    const h = readHead(await client.get("status"));
    return { height: h.height, time: h.time };
  };

  const indexTx = async (hash: string): Promise<IndexTx | null> => {
    try {
      return readIndexTx(await client.get(chainPath("index", "txs", hash.toLowerCase())));
    } catch (err) {
      if (err instanceof ChainNotFound) return null;
      throw err;
    }
  };

  const latestTxs = async (limit: number): Promise<IndexTx[]> => {
    const raw = await client.get(withQuery(chainPath("index", "txs"), { limit }));
    return arr(rec(raw, "transactions").txs, "transactions").map(readIndexTx);
  };

  const previousFromSigner = async (tx: IndexTx, ctx: Context): Promise<TxSummary | null> => {
    if (tx.signer === null) return null;
    const raw = await client.get(withQuery(chainPath("index", "accounts", tx.signer, "txs"), { limit: PREVIOUS_WINDOW }));
    const earlier = arr(rec(raw, "account transactions").txs, "account transactions")
      .map(readIndexTx)
      .find((t) => t.signer === tx.signer && isBefore(t, tx));
    return earlier ? summaryOf(earlier, ctx) : null;
  };

  return {
    origin: { label: ORIGIN_LABEL },

    getNetwork(): Promise<NetworkSnapshot> {
      return loadNetwork(client);
    },

    async getExamples(): Promise<ExampleTargets> {
      const recent = await latestTxs(LATEST_WINDOW);
      const signers = new Map<string, number>();
      for (const t of recent) if (t.signer !== null) signers.set(t.signer, (signers.get(t.signer) ?? 0) + 1);
      const busiest = [...signers.entries()].sort(([a, x], [b, y]) => (x === y ? a.localeCompare(b) : y - x))[0];
      return { latestTxHash: recent[0]?.hash ?? null, busyWalletAddress: busiest?.[0] ?? null };
    },

    async getLatestActivity(filter: ActivityFilter, limit: number): Promise<TxSummary[]> {
      const wanted = clampLimit(limit);
      if (wanted === 0) return [];
      const ctx = await context();
      const txs = await latestTxs(filter === "all" ? Math.min(wanted, LATEST_WINDOW) : LATEST_WINDOW);
      return txs.map((t) => summaryOf(t, ctx)).filter((t) => filterTx(t, filter)).slice(0, wanted);
    },

    async getRecentBlocks(limit: number): Promise<BlockSummary[]> {
      const wanted = clampLimit(limit);
      if (wanted === 0) return [];
      const [dir, h] = await Promise.all([directory(), head()]);
      return loadRecentBlocks(client, dir, h.height, Math.min(wanted, BLOCKS_PER_CALL * 5));
    },

    async getBlock(height: number): Promise<Block | null> {
      if (!Number.isSafeInteger(height) || height < 1) return null;
      const [dir, ctx, h] = await Promise.all([directory(), context(), head()]);
      if (height > h.height) return null;
      return loadBlock(client, dir, ctx, height, h.height);
    },

    async getTx(hash: string): Promise<TxDetail | null> {
      const tx = await indexTx(hash);
      if (tx === null) return null;
      const ctx = await context();
      const [block, previous] = await Promise.all([
        client.get(chainPath("index", "blocks", String(tx.height))),
        previousFromSigner(tx, ctx),
      ]);
      const others = uint(rec(block, "indexed block").tx_count, "block transaction count") - 1;
      return detailOf(tx, ctx, Math.max(0, others), previous);
    },

    async getWallet(address: string): Promise<WalletProfile | null> {
      const [dir, ctx] = await Promise.all([directory(), context()]);
      return loadWallet(client, dir, address.toLowerCase(), ctx);
    },

    async getWalletActivity(address: string, query: ActivityQuery): Promise<Page<ActivityItem>> {
      const limit = clampLimit(query.limit);
      if (limit === 0) return { items: [], nextCursor: null };
      return loadActivity(client, await context(), address.toLowerCase(), { ...query, limit });
    },

    async getCounterparties(address: string, limit: number): Promise<Counterparty[]> {
      const wanted = clampLimit(limit);
      if (wanted === 0) return [];
      return loadCounterparties(client, await context(), address.toLowerCase(), wanted);
    },

    async getValidators(): Promise<ValidatorSet> {
      return loadValidatorSet(client, await directory());
    },

    async searchLabels(query: string, limit: number): Promise<WalletRef[]> {
      const wanted = clampLimit(limit);
      const needle = query.trim().toLowerCase();
      if (wanted === 0 || needle.length < MIN_LABEL_QUERY || needle.length > MAX_LABEL_QUERY) return [];
      const ranked = (await directory()).validators
        .map((v) => ({ v, at: v.ref.moniker.toLowerCase().indexOf(needle) }))
        .filter((m) => m.at >= 0)
        .sort((a, b) => rank(a.at) - rank(b.at) || a.v.ref.moniker.localeCompare(b.v.ref.moniker));
      return ranked.slice(0, wanted).map((m) => walletRef(m.v.ref.operator, { label: () => m.v.ref.moniker, validator: () => m.v.ref }));
    },

    getHead: head,

    subscribeHead(listener: (head: Head) => void, onError: (error: Error) => void): () => void {
      let last: number | null = null;
      const poll = () =>
        head().then(
          (h) => {
            if (last !== null && h.height > last) listener(h);
            last = Math.max(last ?? 0, h.height);
          },
          (err: unknown) => onError(toError(err)),
        );
      const tick = () => {
        if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
        void poll();
      };
      const timer = setInterval(tick, HEAD_POLL_MS);
      void poll();
      return () => clearInterval(timer);
    },
  };
}

function rank(at: number): number {
  return at === 0 ? LABEL_RANK_PREFIX : LABEL_RANK_INFIX;
}
