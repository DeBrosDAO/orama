import type { BlockSummary, ExampleTargets, Head } from "../../model/types";
import { TIME } from "../../model/time";
import { clampLimit } from "../source";
import type { ExplorerDataSource } from "../source";
import { finalizedHeight, settle } from "./finality";
import { blockDetail, blockSummary, networkSnapshot, validatorSet } from "./queries-chain";
import { latestActivity, txDetail } from "./queries-tx";
import { balanceHistory, counterparties, searchLabels, walletActivity, walletProfile } from "./queries-wallet";
import { BLOCK_MS, World } from "./world";
import type { TxRecord } from "./world";

/** The world settled to the last final block, and the clock reading it was settled at. */
export interface Snapshot {
  world: World;
  nowMs: number;
}

/** One consistent reading of the demo chain; every read calls it once and uses only that. */
export type View = () => Snapshot;

type Reads<K extends keyof ExplorerDataSource> = Pick<ExplorerDataSource, K>;

/** History is anchored here so it does not change on reload, only when the day does. */
const startOfUtcDay = (ms: number): number => Math.floor(ms / TIME.DAY) * TIME.DAY;

export function createView(seed: number, clock: () => number): View {
  let world: World | null = null;
  return () => {
    const nowMs = clock();
    world ??= new World({ seed, anchorMs: startOfUtcDay(nowMs) });
    settle(world, nowMs);
    return { world, nowMs };
  };
}

export function headOf({ world, nowMs }: Snapshot): Head {
  const height = finalizedHeight(world, nowMs);
  return { height, time: new Date(world.timeOfHeight(height)).toISOString() };
}

/** Run a synchronous read as the promise the interface promises, turning a throw into a rejection. */
export function ok<T>(read: () => T): Promise<T> {
  try {
    return Promise.resolve(read());
  } catch (err) {
    return Promise.reject(err instanceof Error ? err : new Error(String(err)));
  }
}

function recentBlocks({ world, nowMs }: Snapshot, limit: number): BlockSummary[] {
  const set = validatorSet(world).validators;
  const top = finalizedHeight(world, nowMs);
  return Array.from({ length: clampLimit(limit) }, (_, i) => top - i)
    .filter((h) => h >= 1)
    .map((h) => blockSummary(world, h, set));
}

/** The newest transfer and the busiest wallet; a field is null when the world has none. */
export function examples(world: Pick<World, "txs" | "users">): ExampleTargets {
  const latest = [...world.txs].reverse().find((r) => r.summary.status.ok && r.summary.messages[0]?.type === "send");
  const busiest = world.users.reduce<(typeof world.users)[number] | null>(
    (a, b) => (a === null || b.txIndices.length > a.txIndices.length ? b : a),
    null,
  );
  return { latestTxHash: latest?.summary.hash ?? null, busyWalletAddress: busiest?.ref.address ?? null };
}

const summaryOf = (r: TxRecord) => r.summary;

/** Take one snapshot and run the read against it. */
const at = <T>(view: View, read: (s: Snapshot) => T): Promise<T> => ok(() => read(view()));

export function chainReads(view: View): Reads<"getNetwork" | "getExamples" | "getLatestActivity" | "getRecentBlocks" | "getBlock" | "getTx" | "getValidators"> {
  return {
    getNetwork: () => at(view, (s) => networkSnapshot(s.world, s.nowMs)),
    getExamples: () => at(view, (s) => examples(s.world)),
    getLatestActivity: (filter, limit) => at(view, (s) => latestActivity(s.world, filter, clampLimit(limit))),
    getRecentBlocks: (limit) => at(view, (s) => recentBlocks(s, limit)),
    getBlock: (height) => at(view, (s) => blockDetail(s.world, height, finalizedHeight(s.world, s.nowMs), summaryOf)),
    getTx: (hash) => at(view, (s) => txDetail(s.world, hash.toUpperCase())),
    getValidators: () => at(view, (s) => validatorSet(s.world)),
  };
}

export function walletReads(view: View): Reads<"getWallet" | "getWalletActivity" | "getCounterparties" | "getBalanceHistory" | "searchLabels"> {
  return {
    getWallet: (address) => at(view, (s) => walletProfile(s.world, address, s.nowMs)),
    getWalletActivity: (address, q) => at(view, (s) => walletActivity(s.world, address, { ...q, limit: clampLimit(q.limit) })),
    getCounterparties: (address, limit) => at(view, (s) => counterparties(s.world, address, clampLimit(limit))),
    getBalanceHistory: (address, range) => at(view, (s) => balanceHistory(s.world, address, range, s.nowMs)),
    searchLabels: (query, limit) => at(view, (s) => searchLabels(s.world, query, clampLimit(limit))),
  };
}

/** Poll the demo clock once per block and announce each new head; without `live` there is nothing to announce. */
export function subscribeToHead(view: View, live: boolean, listener: (head: Head) => void): () => void {
  if (!live) return () => undefined;
  let last = headOf(view()).height;
  const timer = setInterval(() => {
    const head = headOf(view());
    if (head.height <= last) return;
    last = head.height;
    listener(head);
  }, BLOCK_MS);
  return () => clearInterval(timer);
}
