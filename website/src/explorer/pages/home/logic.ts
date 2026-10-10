import type { ActivityFilter, BlockSummary, NetworkSnapshot } from "../../model/types";
import { formatInt } from "../../model/units";

/** Above this share of validators signing, blocks keep finalising (BFT supermajority: more than 2/3). */
const SUPERMAJORITY_NUM = 2;
const SUPERMAJORITY_DEN = 3;

/** Block-square shade buckets by transaction count: 0, 1-2, 3-5, 6 and more. */
const SHADE_MAX_TXS = [0, 2, 5] as const;
const PERCENT = 100;

export type HealthTone = "healthy" | "degraded";

/** What a screen reader hears when the tone changes. Deliberately free of block height and counts, which change every block. */
export const TONE_ANNOUNCEMENT: Record<HealthTone, string> = {
  healthy: "Network healthy",
  degraded: "Network degraded: transactions may be delayed",
};

export interface Health {
  tone: HealthTone;
  sentence: string;
}

/** True when strictly more than two thirds of the validators are signing. */
export function hasSupermajority(signing: number, total: number): boolean {
  return total > 0 && signing * SUPERMAJORITY_DEN > total * SUPERMAJORITY_NUM;
}

/** "2", "1.5": whole seconds without a trailing ".0". */
export function formatSeconds(seconds: number): string {
  return Number.isInteger(seconds) ? String(seconds) : seconds.toFixed(1);
}

/**
 * The one-line verdict under the hero. "Network healthy" only when a
 * supermajority signs; otherwise it says "Degraded" and drops the finality
 * promise, because blocks cannot finalise without that supermajority.
 */
export function healthSentence(network: NetworkSnapshot): Health {
  const { height, validatorsSigning: signing, validatorsTotal: total, blockTimeSeconds } = network;
  const block = `block ${formatInt(height)}`;
  const signers = `${signing} of ${total} validators signing`;
  if (!hasSupermajority(signing, total)) {
    return { tone: "degraded", sentence: `Degraded · ${block} · ${signers} · transactions may be delayed` };
  }
  return {
    tone: "healthy",
    sentence: `Network healthy · ${block} · ${signers} · transactions final in ~${formatSeconds(blockTimeSeconds)} s`,
  };
}

export type DeltaTone = "up" | "down" | "flat";

export interface Delta {
  tone: DeltaTone;
  text: string;
}

/** "▲ 12%" against yesterday; null when the chain has no baseline to compare with. */
export function deltaText(changePct: number | null): Delta | null {
  if (changePct === null || !Number.isFinite(changePct)) return null;
  const rounded = Math.round(Math.abs(changePct));
  if (rounded === 0) return { tone: "flat", text: "No change" };
  return changePct > 0 ? { tone: "up", text: `▲ ${rounded}%` } : { tone: "down", text: `▼ ${rounded}%` };
}

export type BlockShade = 0 | 1 | 2 | 3;

export function blockShade(txCount: number): BlockShade {
  if (txCount <= SHADE_MAX_TXS[0]) return 0;
  if (txCount <= SHADE_MAX_TXS[1]) return 1;
  if (txCount <= SHADE_MAX_TXS[2]) return 2;
  return 3;
}

export function blockLabel(block: BlockSummary): string {
  const noun = block.txCount === 1 ? "transaction" : "transactions";
  return `Block ${formatInt(block.height)}, ${block.txCount} ${noun}`;
}

/** Mean transactions per block, one decimal; null for an empty list. */
export function averageTxs(blocks: readonly BlockSummary[]): string | null {
  if (blocks.length === 0) return null;
  const total = blocks.reduce((sum, b) => sum + b.txCount, 0);
  return (total / blocks.length).toFixed(1);
}

/** 0-100 for a progress bar; clamps a source that reports a value outside 0-1. */
export function epochPercent(progress: number): number {
  if (!Number.isFinite(progress)) return 0;
  return Math.round(Math.min(1, Math.max(0, progress)) * PERCENT);
}

export interface FilterOption {
  id: ActivityFilter;
  label: string;
}

export const ACTIVITY_FILTERS: readonly FilterOption[] = [
  { id: "all", label: "All" },
  { id: "transfers", label: "Transfers" },
  { id: "staking", label: "Staking" },
  { id: "storage", label: "Storage" },
  { id: "failed", label: "Failed" },
];
