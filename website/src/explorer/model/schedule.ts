import type { EpochClock } from "./epochs";
import { NORAMA_PER_ORAMA } from "./units";
import { TIME } from "./time";

/**
 * The emission schedule, mirrored from chain/x/emission/types/schedule.go and split.go. The chain
 * fixes it in code (only a hard fork changes it): five brackets of 730 epochs whose per-epoch
 * ceiling halves each time, then a flat tail forever. The 274 ORAMA tail is a step down from
 * bracket five's 928, not another halving. These numbers are the chain's; the tests pin them.
 */
export const EPOCHS_PER_BRACKET = 730;
/** The per-epoch ceiling of each bracket, in whole ORAMA. */
export const BRACKET_CEILINGS_ORAMA: readonly bigint[] = [14_848n, 7_424n, 3_712n, 1_856n, 928n];
export const TAIL_CEILING_ORAMA = 274n;
/** The last epoch the halving table covers; the tail applies from the next one. */
export const FINAL_BRACKET_EPOCH = BRACKET_CEILINGS_ORAMA.length * EPOCHS_PER_BRACKET;

/** The genesis split of an epoch's ceiling. A structural vote of the houses can move each share by up to 10 points. */
export const SPLIT_PERCENT = { validators: 60n, storage: 25n, relay: 10n, development: 5n } as const;
const PERCENT = 100n;

/** The schedule's ceiling for a completed epoch, in norama; epoch 0 and below mint nothing. */
export function maxMintableAt(epoch: number): bigint {
  if (!Number.isSafeInteger(epoch) || epoch < 1) return 0n;
  const bracket = Math.floor((epoch - 1) / EPOCHS_PER_BRACKET);
  const orama = BRACKET_CEILINGS_ORAMA[bracket] ?? TAIL_CEILING_ORAMA;
  return orama * NORAMA_PER_ORAMA;
}

export interface EpochSplit {
  validators: bigint;
  storage: bigint;
  relay: bigint;
  development: bigint;
}

/**
 * An epoch's ceiling split at the genesis 60/25/10/5, as x/emission does it: storage, relay and
 * development by integer division, and the validator share takes the remainder.
 */
export function splitCeiling(total: bigint): EpochSplit {
  const storage = (total * SPLIT_PERCENT.storage) / PERCENT;
  const relay = (total * SPLIT_PERCENT.relay) / PERCENT;
  const development = (total * SPLIT_PERCENT.development) / PERCENT;
  return { validators: total - storage - relay - development, storage, relay, development };
}

/** The most the schedule allows to have been minted over epochs 1 to `epoch`, in norama. */
export function cumulativeCeiling(epoch: number): bigint {
  if (!Number.isSafeInteger(epoch) || epoch < 1) return 0n;
  let total = 0n;
  let left = epoch;
  for (const orama of BRACKET_CEILINGS_ORAMA) {
    const inBracket = Math.min(left, EPOCHS_PER_BRACKET);
    total += orama * NORAMA_PER_ORAMA * BigInt(inBracket);
    left -= inBracket;
    if (left === 0) return total;
  }
  return total + TAIL_CEILING_ORAMA * NORAMA_PER_ORAMA * BigInt(left);
}

export interface Bracket {
  /** 1-based. */
  number: number;
  firstEpoch: number;
  /** null for the tail, which never ends. */
  lastEpoch: number | null;
  ceiling: bigint;
}

/** The six rows of the schedule: five brackets and the tail. */
export function brackets(): Bracket[] {
  const rows: Bracket[] = BRACKET_CEILINGS_ORAMA.map((orama, i) => ({
    number: i + 1,
    firstEpoch: i * EPOCHS_PER_BRACKET + 1,
    lastEpoch: (i + 1) * EPOCHS_PER_BRACKET,
    ceiling: orama * NORAMA_PER_ORAMA,
  }));
  rows.push({ number: rows.length + 1, firstEpoch: FINAL_BRACKET_EPOCH + 1, lastEpoch: null, ceiling: TAIL_CEILING_ORAMA * NORAMA_PER_ORAMA });
  return rows;
}

/** The row of the schedule an epoch falls in. */
export function bracketOf(epoch: number): Bracket {
  const rows = brackets();
  const found = rows.find((b) => epoch >= b.firstEpoch && (b.lastEpoch === null || epoch <= b.lastEpoch));
  return found ?? rows[0]!;
}

export interface CeilingChange {
  /** The first epoch that has the new ceiling. */
  firstEpoch: number;
  /** Epochs that must close, the one in progress included, before it starts. */
  epochsLeft: number;
  from: bigint;
  to: bigint;
  /** The ceiling halves; false for the last step, down to the flat tail. */
  halving: boolean;
}

/** The next time the per-epoch ceiling drops, seen from the epoch in progress; null once the tail applies. */
export function nextCeilingChange(currentEpoch: number): CeilingChange | null {
  if (!Number.isSafeInteger(currentEpoch) || currentEpoch < 1 || currentEpoch > FINAL_BRACKET_EPOCH) return null;
  const bracket = bracketOf(currentEpoch);
  const lastEpoch = bracket.lastEpoch;
  if (lastEpoch === null) return null;
  const firstEpoch = lastEpoch + 1;
  return {
    firstEpoch,
    epochsLeft: lastEpoch - currentEpoch + 1,
    from: bracket.ceiling,
    to: maxMintableAt(firstEpoch),
    halving: bracket.number < BRACKET_CEILINGS_ORAMA.length,
  };
}

/**
 * The earliest the epoch `epochsLeft` closings away can begin: the epoch in progress ends no
 * sooner than its start plus the duration, and each later epoch lasts at least the duration too.
 * Epochs also wait for their block count, so this is a lower bound, never a promise.
 */
export function earliestStartMs(clock: EpochClock, epochsLeft: number): number {
  return Date.parse(clock.startedAt) + epochsLeft * clock.durationSeconds * TIME.SECOND;
}

export interface EpochCountdown {
  /** Time left before the duration condition holds, ms; 0 when it holds. */
  timeLeftMs: number;
  /** Blocks left before the block condition holds; 0 when it holds. */
  blocksLeft: number;
  blocksSoFar: number;
  /** 0 to 1: how far the epoch is from closing, which is the slower of its two conditions. */
  progress: number;
  /** Both conditions hold: the epoch closes in the next block. */
  due: boolean;
}

/**
 * Where the epoch in progress stands. `liveHeight` is the head now; blocks made since the clock
 * was read are added to its block count. A clock with no duration or no block minimum counts that
 * condition as met, since the chain bounds both above zero.
 */
export function epochCountdown(clock: EpochClock, nowMs: number, liveHeight: number | null): EpochCountdown {
  const durationMs = clock.durationSeconds * TIME.SECOND;
  const elapsed = nowMs - Date.parse(clock.startedAt);
  const timeLeftMs = Math.max(0, durationMs - elapsed);
  const since = liveHeight === null ? 0 : Math.max(0, liveHeight - clock.readAtHeight);
  const blocksSoFar = clock.blocksInEpoch + since;
  const blocksLeft = Math.max(0, clock.minBlocks - blocksSoFar);
  const timeProgress = durationMs > 0 ? Math.min(1, Math.max(0, elapsed / durationMs)) : 1;
  const blockProgress = clock.minBlocks > 0 ? Math.min(1, blocksSoFar / clock.minBlocks) : 1;
  return { timeLeftMs, blocksLeft, blocksSoFar, progress: Math.min(timeProgress, blockProgress), due: timeLeftMs === 0 && blocksLeft === 0 };
}

const YEAR_DAYS = 365;

/** "2 y 41 d", "12 d 4 h", "9 h 22 m", "4 m 10 s", for the time left to an event; "now" at zero. */
export function formatSpan(ms: number): string {
  const total = Math.max(0, Math.floor(ms / TIME.SECOND));
  if (total === 0) return "now";
  const days = Math.floor(total / (TIME.DAY / TIME.SECOND));
  const hours = Math.floor((total % (TIME.DAY / TIME.SECOND)) / (TIME.HOUR / TIME.SECOND));
  const minutes = Math.floor((total % (TIME.HOUR / TIME.SECOND)) / (TIME.MINUTE / TIME.SECOND));
  const seconds = total % (TIME.MINUTE / TIME.SECOND);
  if (days >= YEAR_DAYS) return `${Math.floor(days / YEAR_DAYS)} y ${days % YEAR_DAYS} d`;
  if (days > 0) return `${days} d ${hours} h`;
  if (hours > 0) return `${hours} h ${minutes} m`;
  if (minutes > 0) return `${minutes} m ${seconds} s`;
  return `${seconds} s`;
}
