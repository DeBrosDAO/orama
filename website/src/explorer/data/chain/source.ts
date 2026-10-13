import type { EpochListQuery } from "../../model/epochs";
import type { KeyQuery } from "../../model/paging";
import type { AccountRewards, Earnings, EpochRewards, RewardListQuery, ValidatorRewardHistory } from "../../model/rewards";
import { SHIELDED_POOL_ADDRESS, SHIELDED_POOL_LABEL } from "../../model/shielded";
import type { NodeDetail, NodeSet } from "../../model/nodes";
import type { ArchiveHealth, DealList, DealReplica, SettlementQueue } from "../../model/storage";
import type { TorRelays } from "../../model/tor";
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
  ValidatorRef,
  ValidatorSet,
  ValidatorSigning,
  WalletProfile,
  WalletRef,
} from "../../model/types";
import { clampLimit } from "../source";
import type { ExplorerDataSource } from "../source";
import { INDEX_MAX_LIMIT, loadBaseFee, loadEpoch, loadEpochClock, loadEpochs, loadEpochValidators, loadSupply } from "./aggregates";
import { loadBlock } from "./blocks";
import { CHAIN_BASE, ChainNotFound, chainPath, createClient, withQuery } from "./client";
import type { ChainClient, ChainFetch } from "./client";
import { loadDirectory } from "./directory";
import type { Directory } from "./directory";
import { browserEventSource, createHeadFeed } from "./head-feed";
import type { EventSourceFactory } from "./head-feed";
import { HOME_BLOCKS, HOME_TXS, loadBlockPage, loadHome, loadIndexStatus, networkOf } from "./home";
import type { Home } from "./home";
import { loadAccountRewards, loadEarnings, loadEpochRewards, loadValidatorRewards } from "./rewards";
import { loadArchiveHealth } from "./archive";
import { createAssetsSource } from "./assets-source";
import { loadNodeDetail, loadNodeSet } from "./nodes";
import { loadDeals, loadReplicas, loadSettlementQueue } from "./storage";
import { loadTorRelays } from "./tor";
import { detailOf, readIndexTx, summaryOf, walletRef } from "./tx";
import type { Context, IndexTx } from "./tx";
import { loadHistory, loadOperatorGroups, loadSigning, loadValidator } from "./validator-detail";
import { loadValidatorSet } from "./validators";
import { loadActivity, loadCounterparties, loadWallet } from "./wallet";
import { arr, rec, uint } from "./wire";

/** How long a read validator directory is reused. Validators change in epochs, not in blocks. */
const DIRECTORY_TTL_MS = 30_000;
/**
 * How long one read of the home snapshot is shared. The page's tiles, block squares and activity feed
 * each ask for a part of it when the head moves; one read answers all of them. The indexer reuses a
 * snapshot for two seconds, so this is the time the readers of one page share a request.
 */
const HOME_REUSE_MS = 1_000;
/** How long a read node list is reused: the nodes, storage and Tor pages each read it, and nodes change in registrations, not in blocks. */
const NODE_SET_TTL_MS = 30_000;
/** The previous transaction from a signer is looked for among their newest transactions. */
const PREVIOUS_WINDOW = 100;
/** Where the data comes from, shown in the footer, when the caller names no network. */
const DEFAULT_ORIGIN_LABEL = "the Orama chain";
const MAX_LABEL_QUERY = 64;
const MIN_LABEL_QUERY = 1;
const LABEL_RANK_PREFIX = 0;
const LABEL_RANK_INFIX = 1;

export interface ChainSourceOptions {
  fetcher?: ChainFetch;
  base?: string;
  /** Names the network read in the footer ("stagenet at stagenet.orama.network"). */
  label?: string;
  /** The clock, for the directory cache. */
  now?: () => number;
  /**
   * Opens the head stream (an EventSource). Defaults to the browser's; null stands for a browser that
   * has none, in which case the head cannot be followed and reading it fails with a message.
   */
  eventSource?: EventSourceFactory | null;
}

function serverFilter(filter: ActivityFilter): string | undefined {
  return filter === "all" ? undefined : filter;
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
  const feed = createHeadFeed(
    `${options.base ?? CHAIN_BASE}/head/stream`,
    options.eventSource === undefined ? browserEventSource() : options.eventSource,
  );
  let cached: { at: number; directory: Promise<Directory> } | null = null;
  let homeRead: { at: number; home: Promise<Home> } | null = null;

  const home = (): Promise<Home> => {
    if (homeRead === null || now() - homeRead.at > HOME_REUSE_MS) {
      const pending = loadHome(client);
      homeRead = { at: now(), home: pending };
      pending.catch(() => {
        if (homeRead?.home === pending) homeRead = null;
      });
    }
    return homeRead.home;
  };

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

  let nodeSet: { at: number; nodes: Promise<NodeSet> } | null = null;
  let groupsRead: { at: number; groups: Promise<OperatorGroups> } | null = null;

  const nodes = (): Promise<NodeSet> => {
    if (nodeSet === null || now() - nodeSet.at > NODE_SET_TTL_MS) {
      const pending = loadNodeSet(client);
      nodeSet = { at: now(), nodes: pending };
      pending.catch(() => {
        if (nodeSet?.nodes === pending) nodeSet = null;
      });
    }
    return nodeSet.nodes;
  };

  const context = async (): Promise<Context> => {
    const dir = await directory();
    return {
      label: (address) => (address === SHIELDED_POOL_ADDRESS ? SHIELDED_POOL_LABEL : dir.byOperator.get(address)?.ref.moniker),
      validator: (operator): ValidatorRef => dir.byOperator.get(operator)?.ref ?? { moniker: operator, operator },
    };
  };

  const indexTx = async (hash: string): Promise<IndexTx | null> => {
    try {
      return readIndexTx(await client.get(chainPath("index", "txs", hash.toLowerCase())));
    } catch (err) {
      if (err instanceof ChainNotFound) return null;
      throw err;
    }
  };

  const txList = async (filter: ActivityFilter, limit: number): Promise<IndexTx[]> => {
    const raw = await client.get(withQuery(chainPath("index", "txs"), { filter: serverFilter(filter), limit }));
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
    origin: { label: options.label ?? DEFAULT_ORIGIN_LABEL },
    assets: createAssetsSource(client, now),

    async getNetwork(): Promise<NetworkSnapshot> {
      const [snapshot, head] = await Promise.all([home(), feed.current()]);
      return networkOf(snapshot, head);
    },

    getIndexStatus(): Promise<IndexStatus> {
      return loadIndexStatus(client);
    },

    async getExamples(): Promise<ExampleTargets> {
      const { examples } = await home();
      return { latestTxHash: examples.latestTx === null ? null : examples.latestTx.toUpperCase(), busyWalletAddress: examples.busyAccount };
    },

    async getLatestActivity(filter: ActivityFilter, limit: number): Promise<TxSummary[]> {
      const count = clampLimit(limit);
      if (count === 0) return [];
      const ctx = await context();
      // The newest few of every kind are in the home snapshot the page has already read; a filter, or a
      // longer list, is a read of the index's own list for it.
      const txs =
        filter === "all" && count <= HOME_TXS ? (await home()).txs.slice(0, count) : await txList(filter, Math.min(count, INDEX_MAX_LIMIT));
      return txs.map((t) => summaryOf(t, ctx));
    },

    async getRecentBlocks(limit: number): Promise<BlockSummary[]> {
      const count = clampLimit(limit);
      if (count === 0) return [];
      if (count <= HOME_BLOCKS) return (await home()).blocks.slice(0, count);
      const out: BlockSummary[] = [];
      let after: string | null = null;
      while (out.length < count) {
        const { blocks, next } = await loadBlockPage(client, after, INDEX_MAX_LIMIT);
        out.push(...blocks);
        if (next === null) break;
        after = next;
      }
      return out.slice(0, count);
    },

    async getBlock(height: number): Promise<Block | null> {
      if (!Number.isSafeInteger(height) || height < 1) return null;
      const [dir, ctx, h] = await Promise.all([directory(), context(), feed.current()]);
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

    getEpochClock: () => loadEpochClock(client),
    getEpochs: (query: EpochListQuery) => loadEpochs(client, query),
    getEpoch: (epoch: number) => loadEpoch(client, epoch),
    getEpochValidators: (epoch: number, query: EpochListQuery) => loadEpochValidators(client, directory, epoch, query),
    getEpochRewards: (epoch: number, query: RewardListQuery): Promise<EpochRewards | null> => loadEpochRewards(client, directory, epoch, query),
    async getValidatorRewards(address: string, query: RewardListQuery): Promise<ValidatorRewardHistory | null> {
      return loadValidatorRewards(client, await directory(), address.toLowerCase(), query);
    },
    async getAccountRewards(address: string, query: KeyQuery): Promise<AccountRewards> {
      return loadAccountRewards(client, await directory(), address.toLowerCase(), query);
    },
    getEarnings: (address: string): Promise<Earnings> => loadEarnings(client, address.toLowerCase()),
    getSupplyHistory: (limit: number) => loadSupply(client, limit),
    getBaseFee: () => loadBaseFee(client),

    async getValidator(address: string): Promise<ValidatorDetail | null> {
      return loadValidator(client, await directory(), address.toLowerCase());
    },

    async getValidatorHistory(address: string): Promise<ValidatorHistory | null> {
      return loadHistory(client, await directory(), address.toLowerCase());
    },

    getValidatorSigning(): Promise<ValidatorSigning[]> {
      return loadSigning(client);
    },

    // Shared for as long as the directory is: the validators page and every validator's page ask for the same
    // groups, and each read is up to MAX_OPERATOR_LOOKUPS of the gateway's module queries.
    getOperatorGroups(): Promise<OperatorGroups> {
      if (groupsRead === null || now() - groupsRead.at > DIRECTORY_TTL_MS) {
        const pending = directory().then((dir) => loadOperatorGroups(client, dir));
        groupsRead = { at: now(), groups: pending };
        pending.catch(() => {
          if (groupsRead?.groups === pending) groupsRead = null;
        });
      }
      return groupsRead.groups;
    },

    getNetworkNodes: nodes,
    getNode: (id: string): Promise<NodeDetail | null> => loadNodeDetail(client, id),
    getDeals: (): Promise<DealList> => loadDeals(client),
    getDealReplicas: (dealId: number, replicas: number): Promise<DealReplica[]> => loadReplicas(client, dealId, replicas),
    getSettlementQueue: (): Promise<SettlementQueue> => loadSettlementQueue(client),
    getArchiveHealth: (): Promise<ArchiveHealth> => loadArchiveHealth(client),
    getTorRelays: (): Promise<TorRelays> => loadTorRelays(client),

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

    getHead: () => feed.current(),

    subscribeHead: (listener: (head: Head) => void, onError: (error: Error) => void): (() => void) => feed.subscribe(listener, onError),
  };
}

function rank(at: number): number {
  return at === 0 ? LABEL_RANK_PREFIX : LABEL_RANK_INFIX;
}
