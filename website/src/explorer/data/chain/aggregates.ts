import { bech32Rehrp } from "../../model/bech32";
import type {
  EpochClock,
  EpochListQuery,
  EpochRow,
  EpochSpan,
  EpochValidator,
  IndexPage,
  RecordedEpochRow,
  SupplyPoint,
  UnavailableEpochRow,
} from "../../model/epochs";
import { shortAddress } from "../../model/units";
import { ChainNotFound, ChainReadError, chainPath, withQuery } from "./client";
import type { ChainClient } from "./client";
import type { Directory } from "./directory";
import { clean } from "./text";
import { bool, digits, iso, optArr, optDigits, optUint, rec, str, uint } from "./wire";
import { readHead } from "./network";

/** The indexer's own bounds for a paged list (chain/indexer/api.go; the gateway checks them again). */
export const INDEX_MAX_PAGE = 1000;
export const INDEX_MAX_LIMIT = 100;

const VALIDATOR_HRP = "orama";
const MAX_ADDRESS = 120;
const MAX_STATUS = 32;
const BASIS_POINTS = 10_000;
const NANOS_PER_MILLI = 1_000_000n;

const CURRENT_EPOCH = "query/orama.emission.v1.Query/CurrentEpoch";
const EMISSION_PARAMS = "query/orama.emission.v1.Query/Params";
const BASE_FEE = "query/orama.fees.v1.Query/BaseFee";

/** A page number the indexer accepts: a whole number from 1 to INDEX_MAX_PAGE. */
export function pageNumber(page: number): number {
  return Number.isFinite(page) ? Math.min(INDEX_MAX_PAGE, Math.max(1, Math.floor(page))) : 1;
}

/** A page size the indexer accepts: a whole number from 1 to INDEX_MAX_LIMIT. */
export function pageLimit(limit: number): number {
  return Number.isFinite(limit) ? Math.min(INDEX_MAX_LIMIT, Math.max(1, Math.floor(limit))) : 1;
}

function pagedPath(path: string, query: EpochListQuery): { path: string; page: number; limit: number } {
  const page = pageNumber(query.page);
  const limit = pageLimit(query.limit);
  return { path: withQuery(path, { page, limit }), page, limit };
}

/** The split of an epoch's payout; all three are present when the epoch's payout is recorded and absent when it is not. */
function readRewardTotals(r: Record<string, unknown>): Pick<EpochSpan, "rewardsRecorded" | "rewardsCommission" | "rewardsOperatorShare" | "rewardsDelegators"> {
  const recorded = r.rewards_recorded === undefined ? false : bool(r.rewards_recorded, "rewards recorded flag");
  if (!recorded) return { rewardsRecorded: false, rewardsCommission: null, rewardsOperatorShare: null, rewardsDelegators: null };
  const operatorShare = digits(r.rewards_operator_share, "operators' reward share");
  const delegators = digits(r.rewards_delegators, "delegators' reward share");
  const commission = digits(r.rewards_commission, "reward commission");
  if (BigInt(commission) > BigInt(operatorShare)) throw new ChainReadError("The chain sent a commission above the operators' share.");
  return { rewardsRecorded: true, rewardsCommission: commission, rewardsOperatorShare: operatorShare, rewardsDelegators: delegators };
}

/** The fields the indexer reads from state: an epoch it could not read has none of them, and one it could read has all. */
const STATE_FIGURES = [
  "max_mintable",
  "minted_validators",
  "minted_service",
  "minted_development",
  "minted_faucet",
  "minted_total",
  "burned",
  "fees_collected",
  "fees_burned",
  "fees_distributed",
  "rewards_credited",
  "rewards_force_bonded",
] as const;

const MAX_REASON = 600;

/** An epoch the indexer could not read has a reason and no state figure; one that carries a figure is not believed. */
function readUnavailable(r: Record<string, unknown>, span: EpochSpan): UnavailableEpochRow {
  if (STATE_FIGURES.some((key) => r[key] !== undefined)) throw new ChainReadError("The chain sent an unavailable epoch that carries figures.");
  const reason = clean(str(r.unavailable_reason, "epoch unavailable reason", MAX_REASON), MAX_REASON);
  if (reason === "") throw new ChainReadError("The chain sent an unavailable epoch without a reason.");
  return { ...span, unavailable: true, reason };
}

function readRecordedFigures(r: Record<string, unknown>): Omit<RecordedEpochRow, keyof EpochSpan | "unavailable"> {
  return {
    maxMintable: digits(r.max_mintable, "epoch ceiling"),
    mintedValidators: digits(r.minted_validators, "validator mint"),
    mintedService: digits(r.minted_service, "service mint"),
    mintedDevelopment: digits(r.minted_development, "development mint"),
    mintedFaucet: digits(r.minted_faucet, "faucet mint"),
    mintedTotal: digits(r.minted_total, "total mint"),
    burned: digits(r.burned, "epoch burn"),
    feesCollected: digits(r.fees_collected, "fees collected"),
    feesBurned: digits(r.fees_burned, "fees burned"),
    feesDistributed: digits(r.fees_distributed, "fees distributed"),
    rewardsCredited: digits(r.rewards_credited, "rewards credited"),
    rewardsForceBonded: digits(r.rewards_force_bonded, "rewards force-bonded"),
  };
}

/** One epoch row: with its figures, or unavailable. Either every figure is present or none is. */
export function readEpochRow(raw: unknown): EpochRow {
  const r = rec(raw, "epoch");
  const startHeight = uint(r.start_height, "epoch start height");
  const endHeight = uint(r.end_height, "epoch end height");
  if (endHeight < startHeight) throw new ChainReadError("The chain sent a malformed epoch.");
  const span: EpochSpan = {
    epoch: uint(r.epoch, "epoch number"),
    startHeight,
    endHeight,
    startTime: iso(r.start_time, "epoch start time"),
    endTime: iso(r.end_time, "epoch end time"),
    blocks: uint(r.blocks, "epoch block count"),
    partial: r.partial === undefined ? false : bool(r.partial, "epoch partial flag"),
    ...readRewardTotals(r),
  };
  if (r.unavailable !== undefined && bool(r.unavailable, "epoch unavailable flag")) return readUnavailable(r, span);
  if (r.unavailable_reason !== undefined) throw new ChainReadError("The chain sent an epoch with figures that also says it is unavailable.");
  return { ...span, unavailable: false, ...readRecordedFigures(r) };
}

export function readSupplyPoint(raw: unknown): SupplyPoint {
  const r = rec(raw, "supply point");
  return {
    epoch: uint(r.epoch, "supply epoch"),
    height: uint(r.height, "supply height"),
    time: iso(r.time, "supply time"),
    totalSupply: digits(r.total_supply, "total supply"),
    mintedToDate: digits(r.minted_to_date, "minted to date"),
    burnedToDate: digits(r.burned_to_date, "burned to date"),
    genesisSupply: digits(r.genesis_supply, "genesis supply"),
    bonded: digits(r.bonded, "bonded"),
    unbonding: digits(r.unbonding, "unbonding"),
    earningsPools: digits(r.earnings_pools, "earnings pools"),
    storageEscrow: digits(r.storage_escrow, "storage escrow"),
    otherProtocol: digits(r.other_protocol, "other protocol holdings"),
    circulating: digits(r.circulating, "circulating supply"),
  };
}

export function readEpochValidator(raw: unknown, directory: Directory): EpochValidator {
  const r = rec(raw, "epoch validator");
  const valoper = str(r.operator, "validator operator", MAX_ADDRESS);
  const operator = bech32Rehrp(valoper, VALIDATOR_HRP);
  if (operator === null) throw new ChainReadError("The chain sent a malformed validator operator address.");
  const uptimeBps = uint(r.uptime_bps, "validator uptime");
  if (uptimeBps > BASIS_POINTS) throw new ChainReadError("The chain sent a malformed validator uptime.");
  return {
    ref: directory.byOperator.get(operator)?.ref ?? { moniker: shortAddress(operator), operator },
    power: uint(r.comet_power, "validator power"),
    signedBlocks: uint(r.signed_blocks, "validator signed blocks"),
    missedBlocks: uint(r.missed_blocks, "validator missed blocks"),
    uptimeBps,
    slashes: optUint(r.slashes, "validator slashes"),
    slashedBurned: optDigits(r.slashed_burned, "validator slash burn"),
    jailed: r.jailed === undefined ? false : bool(r.jailed, "validator jailed flag"),
    status: clean(str(r.status, "validator status", MAX_STATUS), MAX_STATUS),
  };
}

/** The closed epochs, newest first. */
export async function loadEpochs(client: ChainClient, query: EpochListQuery): Promise<IndexPage<EpochRow>> {
  const paged = pagedPath(chainPath("index", "epochs"), query);
  const items = optArr(rec(await client.get(paged.path), "epochs").epochs, "epochs").map(readEpochRow);
  return { items, page: paged.page, limit: paged.limit, hasMore: items.length === paged.limit };
}

/** One closed epoch; null when the index has none by that number (still open, not yet reached, or before the index began). */
export async function loadEpoch(client: ChainClient, epoch: number): Promise<EpochRow | null> {
  if (!Number.isSafeInteger(epoch) || epoch < 1) return null;
  try {
    const row = readEpochRow(await client.get(chainPath("index", "epochs", String(epoch))));
    if (row.epoch !== epoch) throw new ChainReadError("The chain answered a different epoch than the one asked for.");
    return row;
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

/** The validators of a closed epoch, highest power first; null when the epoch is not closed in the index. */
export async function loadEpochValidators(
  client: ChainClient,
  directory: () => Promise<Directory>,
  epoch: number,
  query: EpochListQuery,
): Promise<IndexPage<EpochValidator> | null> {
  if (!Number.isSafeInteger(epoch) || epoch < 1) return null;
  const paged = pagedPath(chainPath("index", "epochs", String(epoch), "validators"), query);
  let body: unknown;
  try {
    body = await client.get(paged.path);
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
  const rows = optArr(rec(body, "epoch validators").validators, "epoch validators");
  const dir = await directory();
  const items = rows.map((row) => readEpochValidator(row, dir));
  return { items, page: paged.page, limit: paged.limit, hasMore: items.length === paged.limit };
}

/** The supply breakdown after each closed epoch, newest first. */
export async function loadSupply(client: ChainClient, limit: number): Promise<SupplyPoint[]> {
  const paged = pagedPath(chainPath("index", "supply"), { page: 1, limit });
  return optArr(rec(await client.get(paged.path), "supply").points, "supply").map(readSupplyPoint);
}

/** The epoch in progress, from x/emission and the head, read together. */
export async function loadEpochClock(client: ChainClient): Promise<EpochClock> {
  const [statusRaw, stateRaw, paramsRaw] = await Promise.all([client.get("status"), client.get(CURRENT_EPOCH), client.get(EMISSION_PARAMS)]);
  const head = readHead(statusRaw);
  const state = rec(rec(stateRaw, "epoch").epoch_state, "epoch");
  const params = rec(rec(paramsRaw, "emission parameters").params, "emission parameters");
  const startMs = Number(BigInt(optDigits(state.epoch_start_unix_nano, "epoch start")) / NANOS_PER_MILLI);
  const durationSeconds = Number(digits(params.epoch_duration_seconds, "epoch length"));
  if (!Number.isSafeInteger(startMs) || startMs <= 0 || !Number.isSafeInteger(durationSeconds) || durationSeconds <= 0) {
    throw new ChainReadError("The chain sent a malformed epoch.");
  }
  return {
    epoch: uint(state.current_epoch, "epoch number"),
    startedAt: new Date(startMs).toISOString(),
    durationSeconds,
    minBlocks: uint(params.min_blocks_per_epoch, "epoch block minimum"),
    blocksInEpoch: optUint(state.blocks_in_epoch, "epoch block count"),
    readAtHeight: head.height,
  };
}

/** The base fee now, in norama per gas (x/fees). */
export async function loadBaseFee(client: ChainClient): Promise<number> {
  const base = Number(digits(rec(await client.get(BASE_FEE), "base fee").base_fee, "base fee"));
  if (!Number.isSafeInteger(base)) throw new ChainReadError("The chain sent a malformed base fee.");
  return base;
}
