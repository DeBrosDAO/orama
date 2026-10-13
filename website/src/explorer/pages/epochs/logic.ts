import { hasFigures } from "../../model/epochs";
import type { EpochClock, EpochRow } from "../../model/epochs";
import { splitCeiling } from "../../model/schedule";

const TENTHS_PER_WHOLE = 1000n;
const PERCENT_DIVISOR = 10;
const POSITIVE_INT = /^[1-9][0-9]{0,14}$/;
const BASIS_POINTS = 100;

/** The epoch number in a URL: digits only, from 1; anything else is no epoch. */
export function parseEpochParam(raw: string | undefined): number | null {
  if (raw === undefined || !POSITIVE_INT.test(raw)) return null;
  const n = Number(raw);
  return Number.isSafeInteger(n) ? n : null;
}

/** The ?page= of a list URL: a whole number from 1; a missing or odd value is page 1. */
export function parsePageParam(raw: string | null): number {
  if (raw === null || !POSITIVE_INT.test(raw)) return 1;
  return Math.min(Number(raw), 1000);
}

/** The query string that shows `page` of the list `key` names, keeping the rest of the URL's query; page 1 is the bare URL. */
export function pageSearch(current: URLSearchParams, key: string, page: number): string {
  const next = new URLSearchParams(current);
  if (page <= 1) next.delete(key);
  else next.set(key, String(page));
  const text = next.toString();
  return text === "" ? "" : `?${text}`;
}

/** "62.5%": `part` of `whole`, truncated to a tenth like every amount in the explorer; null when there is no whole. */
export function sharePercent(part: bigint, whole: bigint): string | null {
  if (whole <= 0n) return null;
  const tenths = Number((part * TENTHS_PER_WHOLE) / whole);
  return `${(tenths / PERCENT_DIVISOR).toFixed(1)}%`;
}

/** "99.5%" from an uptime in basis points. */
export function uptimePercent(bps: number): string {
  return `${(bps / BASIS_POINTS).toFixed(2)}%`;
}

/** How long an epoch ran, in ms, from its first block's epoch start to its closing block. */
export function epochSpanMs(row: Pick<EpochRow, "startTime" | "endTime">): number {
  return Math.max(0, Date.parse(row.endTime) - Date.parse(row.startTime));
}

export type MissingEpoch = "open" | "future" | "unindexed";

/**
 * Why the index has no row for an epoch: it is the one in progress, it has not been reached, or
 * it closed before the index began (an index holds only the epochs it saw close).
 */
export function whyMissing(epoch: number, clock: Pick<EpochClock, "epoch">): MissingEpoch {
  if (epoch === clock.epoch) return "open";
  return epoch > clock.epoch ? "future" : "unindexed";
}

export interface Tally {
  /** What the schedule allowed for this category over the epochs counted. */
  ceiling: bigint;
  /** What was minted in those epochs' blocks. */
  minted: bigint;
}

export interface CeilingWindow {
  /** Closed epochs counted. */
  epochs: number;
  /** Epochs left out because the index saw only part of them. */
  partialSkipped: number;
  /** Epochs left out because the index could not read their figures: not counted as zero, so they change no sum. */
  unavailableSkipped: number;
  validators: Tally;
  /** Storage and relay together: the chain keeps one running total for the two. */
  service: Tally;
  development: Tally;
  total: Tally;
  /** Test-network faucet mints, which no ceiling covers. */
  faucet: bigint;
}

/**
 * What was minted against what the schedule allowed, over a run of closed epochs. Each epoch's
 * ceiling is the chain's own (the row's max_mintable) split at the genesis 60/25/10/5. A partial
 * epoch is left out: its minted figures cover only the blocks the index saw, so comparing them
 * with a whole epoch's ceiling would understate them. An unavailable epoch has no figure to add, and
 * its ceiling is left out with it, so the minted and the ceiling still cover the same epochs.
 */
export function ceilingWindow(rows: readonly EpochRow[]): CeilingWindow {
  const win: CeilingWindow = {
    epochs: 0,
    partialSkipped: 0,
    unavailableSkipped: 0,
    validators: { ceiling: 0n, minted: 0n },
    service: { ceiling: 0n, minted: 0n },
    development: { ceiling: 0n, minted: 0n },
    total: { ceiling: 0n, minted: 0n },
    faucet: 0n,
  };
  for (const row of rows) {
    if (!hasFigures(row)) {
      win.unavailableSkipped += 1;
      continue;
    }
    if (row.partial) {
      win.partialSkipped += 1;
      continue;
    }
    const ceiling = BigInt(row.maxMintable);
    const split = splitCeiling(ceiling);
    win.epochs += 1;
    win.validators.ceiling += split.validators;
    win.validators.minted += BigInt(row.mintedValidators);
    win.service.ceiling += split.storage + split.relay;
    win.service.minted += BigInt(row.mintedService);
    win.development.ceiling += split.development;
    win.development.minted += BigInt(row.mintedDevelopment);
    win.total.ceiling += ceiling;
    win.total.minted += BigInt(row.mintedTotal);
    win.faucet += BigInt(row.mintedFaucet);
  }
  return win;
}
