import type { Head, NetworkSnapshot } from "../../model/types";
import { ChainReadError, chainPath } from "./client";
import type { ChainClient } from "./client";
import { arr, bool, digits, iso, optArr, optDigits, rec, rpcResult, str, uint } from "./wire";

const HOURS_PER_DAY = 24;
const BLOCK_TIME_SAMPLE = 20;
const BLOCK_ID_FLAG_COMMIT = 2;
const PERCENT = 100;
const NANOS_PER_MILLI = 1_000_000n;
const SECOND_MS = 1000;
const TENTH = 10;

const SUPPLY = chainPath("supply", "norama");
const BASE_FEE = "query/orama.fees.v1.Query/BaseFee";
const CURRENT_EPOCH = "query/orama.emission.v1.Query/CurrentEpoch";
const EMISSION_PARAMS = "query/orama.emission.v1.Query/Params";
const STATS = chainPath("index", "stats");

export function readHead(raw: unknown): Head & { chainId: string; catchingUp: boolean } {
  const result = rpcResult(raw, "chain status");
  const sync = rec(result.sync_info, "chain status");
  return {
    height: uint(sync.latest_block_height, "block height"),
    time: iso(sync.latest_block_time, "block time"),
    chainId: str(rec(result.node_info, "chain status").network, "chain id", 64),
    catchingUp: bool(sync.catching_up, "sync flag"),
  };
}

/**
 * How many of the validators in a CometBFT block's last commit voted for the
 * block, and how many were in the set: a commit has one entry per validator,
 * and an absent one is not counted as signed.
 */
export function commitTally(block: Record<string, unknown>): { signed: number; total: number } {
  const commit = rec(block.last_commit, "last commit");
  const entries = optArr(commit.signatures, "commit signatures");
  const signed = entries.filter((s) => {
    const flag = rec(s, "commit signature").block_id_flag;
    return flag === BLOCK_ID_FLAG_COMMIT || flag === "BLOCK_ID_FLAG_COMMIT";
  }).length;
  return { signed, total: entries.length };
}

/** Seconds per block over the recent blocks' header times, to a tenth. */
export function blockTimeSeconds(times: readonly string[]): number {
  if (times.length < 2) return 0;
  const sorted = [...times].map(Date.parse).sort((a, b) => a - b);
  const span = (sorted[sorted.length - 1] ?? 0) - (sorted[0] ?? 0);
  return Math.round((span / SECOND_MS / (sorted.length - 1)) * TENTH) / TENTH;
}

function recentBlockTimes(raw: unknown): string[] {
  const metas = arr(rpcResult(raw, "recent blocks").block_metas, "recent blocks");
  return metas.map((m) => iso(rec(rec(m, "block").header, "block header").time, "block time"));
}

interface Hourly {
  txs: number;
  failed: number;
  burned: bigint;
}

function readHours(raw: unknown): Hourly[] {
  return arr(rec(raw, "statistics").hours, "statistics").map((h) => {
    const hour = rec(h, "hourly statistic");
    return { txs: uint(hour.txs, "hourly count"), failed: uint(hour.failed, "hourly failures"), burned: BigInt(digits(hour.burned, "hourly burn")) };
  });
}

function sum(hours: readonly Hourly[], pick: (h: Hourly) => number): number {
  return hours.reduce((n, h) => n + pick(h), 0);
}

export function transactionTotals(hours: readonly Hourly[]) {
  const last = hours.slice(-HOURS_PER_DAY);
  const before = hours.slice(-2 * HOURS_PER_DAY, -HOURS_PER_DAY);
  const now = sum(last, (h) => h.txs);
  const prior = sum(before, (h) => h.txs);
  return {
    transactions24h: now,
    transactionsChangePct: prior > 0 ? ((now - prior) / prior) * PERCENT : null,
    transactionsSeries: last.map((h) => h.txs),
    failed24h: sum(last, (h) => h.failed),
    burned24h: last.reduce((n, h) => n + h.burned, 0n).toString(),
  };
}

function epochOf(stateRaw: unknown, paramsRaw: unknown, headTime: string): NetworkSnapshot["epoch"] {
  const state = rec(rec(stateRaw, "epoch").epoch_state, "epoch");
  const params = rec(rec(paramsRaw, "emission parameters").params, "emission parameters");
  const durationMs = Number(digits(params.epoch_duration_seconds, "epoch length")) * SECOND_MS;
  const startMs = Number(BigInt(optDigits(state.epoch_start_unix_nano, "epoch start")) / NANOS_PER_MILLI);
  if (!Number.isSafeInteger(startMs) || !Number.isSafeInteger(durationMs) || durationMs <= 0) {
    throw new ChainReadError("The chain sent a malformed epoch.");
  }
  const progress = Math.min(1, Math.max(0, (Date.parse(headTime) - startMs) / durationMs));
  return { number: uint(state.current_epoch, "epoch number"), progress, endsAt: new Date(startMs + durationMs).toISOString() };
}

/** Reads the network-wide numbers of the home page. */
export async function loadNetwork(client: ChainClient): Promise<NetworkSnapshot> {
  const head = readHead(await client.get("status"));
  const first = Math.max(1, head.height - BLOCK_TIME_SAMPLE + 1);
  const [supply, baseFee, stateRaw, paramsRaw, blocksRaw, headBlock, statsRaw] = await Promise.all([
    client.get(SUPPLY),
    client.get(BASE_FEE),
    client.get(CURRENT_EPOCH),
    client.get(EMISSION_PARAMS),
    client.get(`blocks?min_height=${first}&max_height=${head.height}`),
    client.get(`block?height=${head.height}`),
    client.get(STATS),
  ]);
  const base = Number(digits(rec(baseFee, "base fee").base_fee, "base fee"));
  if (!Number.isSafeInteger(base)) throw new ChainReadError("The chain sent a malformed base fee.");
  const tally = commitTally(rec(rpcResult(headBlock, "block").block, "block"));
  return {
    chainId: head.chainId,
    height: head.height,
    blockTimeSeconds: blockTimeSeconds(recentBlockTimes(blocksRaw)),
    validatorsSigning: tally.signed,
    validatorsTotal: tally.total,
    epoch: epochOf(stateRaw, paramsRaw, head.time),
    supply: digits(rec(rec(supply, "supply").amount, "supply").amount, "supply"),
    baseFee: base,
    ...transactionTotals(readHours(statsRaw)),
  };
}
