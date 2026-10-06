import type { Block, BlockSummary, NetworkSnapshot, Norama, Validator, ValidatorRef, ValidatorSet, UptimeDay } from "../../model/types";
import { hashString } from "../../model/hash";
import { TIME } from "../../model/time";
import { NORAMA_PER_ORAMA } from "../../model/units";
import { hashFor } from "./ids";
import { BASE_FEE } from "./plans";
import { finalizedHeight } from "./finality";
import { Rng } from "./rng";
import { BLOCK_MS, CHAIN_ID, GENESIS_SUPPLY } from "./world";
import type { TxRecord, World } from "./world";

const STAKE_FOR_FULL_HANDOVER = 390_000n * NORAMA_PER_ORAMA;
const MAX_LAMBDA = 0.9;
const NAKAMOTO_THRESHOLD = 1 / 3;
const UPTIME_DAYS = 30;

/** Index of the first transaction at or after `ms`. Transactions are time-ordered. */
export function firstIndexAtOrAfter(txs: readonly TxRecord[], ms: number): number {
  let lo = 0;
  let hi = txs.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if ((txs[mid] as TxRecord).timeMs < ms) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

function stakeBy(world: World, moniker: string): bigint {
  let sum = 0n;
  for (const a of world.accounts.values()) sum += a.stakes.get(moniker)?.amount ?? 0n;
  return sum;
}

function uptimeFor(moniker: string, jailed: boolean): UptimeDay[] {
  const rng = new Rng(hashString(`uptime:${moniker}`));
  return Array.from({ length: UPTIME_DAYS }, (_, i): UptimeDay => {
    const r = rng.next();
    if (jailed && i >= UPTIME_DAYS - 4) return "missed";
    if (r > 0.98) return "missed";
    return r > 0.9 ? "partial" : "ok";
  });
}

function uptimePct(days: UptimeDay[]): number {
  const lost = days.reduce((n, d) => n + (d === "missed" ? 1 : d === "partial" ? 0.3 : 0), 0);
  return Math.round((1 - lost / days.length) * 10000) / 100;
}

export function validatorSet(world: World): ValidatorSet {
  const stakes = new Map(world.validators.map((v) => [v.moniker, stakeBy(world, v.moniker)]));
  let totalStaked = 0n;
  for (const s of stakes.values()) totalStaked += s;
  const lambda = Math.min(MAX_LAMBDA, Number((totalStaked * 10_000n) / STAKE_FOR_FULL_HANDOVER) / 10_000);
  const committee = world.validators.filter((v) => v.type === "committee");
  const community = world.validators.filter((v) => v.type === "community" && !v.jailed);
  const communityStake = community.reduce((n, v) => n + Number(stakes.get(v.moniker)), 0) || 1;
  const validators: Validator[] = world.validators.map((v) => {
    const power =
      v.type === "committee"
        ? (1 - lambda) * (v.weight / committee.reduce((n, c) => n + c.weight, 0))
        : v.jailed
          ? 0
          : lambda * (Number(stakes.get(v.moniker)) / communityStake);
    const uptimeDays = uptimeFor(v.moniker, v.jailed);
    return { ref: v.ref, type: v.type, power, jailed: v.jailed, uptimeDays, uptimePct: uptimePct(uptimeDays) };
  });
  const delegators = [...world.accounts.values()].filter((a) => a.staked() > 0n).length;
  return {
    lambda,
    nakamoto: nakamoto(validators),
    totalStaked: totalStaked.toString(),
    delegators,
    jailedLast30d: validators.filter((v) => v.jailed).length,
    validators,
  };
}

function nakamoto(validators: Validator[]): number {
  let sum = 0;
  let n = 0;
  for (const v of [...validators].sort((a, b) => b.power - a.power)) {
    sum += v.power;
    n++;
    if (sum >= NAKAMOTO_THRESHOLD) break;
  }
  return n;
}

function proposerFor(set: Validator[], height: number): ValidatorRef {
  let r = (hashString(`proposer:${height}`) % 1_000_003) / 1_000_003;
  for (const v of set) {
    r -= v.power;
    if (r < 0 && v.power > 0) return v.ref;
  }
  return (set.find((v) => v.power > 0) ?? (set[0] as Validator)).ref;
}

/** Pass `set` when building many blocks so the validator set is computed once. */
export function blockSummary(world: World, height: number, set: Validator[] = validatorSet(world).validators): BlockSummary {
  return {
    height,
    hash: hashFor(`block:${height}`),
    time: new Date(world.timeOfHeight(height)).toISOString(),
    proposer: proposerFor(set, height),
    txCount: world.byHeight.get(height)?.length ?? 0,
  };
}

export function blockDetail(
  world: World,
  height: number,
  headHeight: number,
  summarize: (r: TxRecord) => Block["txs"][number],
): Block | null {
  if (!Number.isInteger(height) || height < 1 || height > headHeight) return null;
  const records = (world.byHeight.get(height) ?? []).map((i) => world.txs[i] as TxRecord);
  const set = validatorSet(world).validators;
  let burned = 0n;
  let gas = 0;
  for (const r of records) {
    burned += BigInt(r.summary.fee.burned);
    gas += r.summary.fee.gasUsed;
  }
  return {
    ...blockSummary(world, height, set),
    gasUsed: gas,
    burned: burned.toString(),
    signatures: { signed: set.filter((v) => !v.jailed).length, total: set.length },
    txs: records.map(summarize),
  };
}

export function networkSnapshot(world: World, nowMs: number): NetworkSnapshot {
  const from24 = firstIndexAtOrAfter(world.txs, nowMs - TIME.DAY);
  const from48 = firstIndexAtOrAfter(world.txs, nowMs - 2 * TIME.DAY);
  const day = world.txs.slice(from24);
  const prior = from24 - from48;
  const set = validatorSet(world).validators;
  const active = new Set<string>();
  let burned = 0n;
  const series = Array.from({ length: 24 }, () => 0);
  for (const r of day) {
    active.add(r.summary.signer.address);
    for (const m of r.summary.messages) if (m.type === "send") active.add(m.to.address);
    burned += BigInt(r.summary.fee.burned);
    const bucket = Math.min(23, Math.floor((r.timeMs - (nowMs - TIME.DAY)) / TIME.HOUR));
    series[bucket] = (series[bucket] ?? 0) + 1;
  }
  const epochNumber = Math.floor((nowMs - world.genesisMs) / TIME.DAY) + 1;
  const epochStart = world.genesisMs + (epochNumber - 1) * TIME.DAY;
  return {
    chainId: CHAIN_ID,
    height: finalizedHeight(world, nowMs),
    blockTimeSeconds: BLOCK_MS / 1000,
    validatorsSigning: set.filter((v) => !v.jailed).length,
    validatorsTotal: set.length,
    epoch: { number: epochNumber, progress: (nowMs - epochStart) / TIME.DAY, endsAt: new Date(epochStart + TIME.DAY).toISOString() },
    supply: supply(world),
    baseFee: Number(BASE_FEE),
    transactions24h: day.length,
    transactionsChangePct: prior > 0 ? Math.round(((day.length - prior) / prior) * 100) : null,
    transactionsSeries: series,
    activeWallets24h: active.size,
    newWallets24h: world.users.filter((u) => (u.firstSeenMs ?? 0) >= nowMs - TIME.DAY).length,
    burned24h: burned.toString(),
  };
}

function supply(world: World): Norama {
  return (GENESIS_SUPPLY + world.minted - world.burned).toString();
}
