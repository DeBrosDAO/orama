import type { JailPeriod, SlashEvent, ValidatorEpoch, ValidatorStatus } from "../../model/types";

export interface WindowUptime {
  /** Signed over signed plus missed across the epochs, 0 to 1; null when none of them carried a commit. */
  uptime: number | null;
  missedBlocks: number;
  signedBlocks: number;
  /** How many closed epochs the figures cover. */
  epochs: number;
}

/** Uptime over every epoch given: blocks are added up, not the epochs' percentages averaged. */
export function windowUptime(epochs: readonly ValidatorEpoch[]): WindowUptime {
  const signed = epochs.reduce((n, e) => n + e.signedBlocks, 0);
  const missed = epochs.reduce((n, e) => n + e.missedBlocks, 0);
  return { uptime: signed + missed === 0 ? null : signed / (signed + missed), missedBlocks: missed, signedBlocks: signed, epochs: epochs.length };
}

/** A validator's CometBFT power per closed epoch, oldest first, for a trend line. */
export function powerSeries(epochs: readonly ValidatorEpoch[]): number[] {
  return [...epochs].sort((a, b) => a.epoch - b.epoch).map((e) => e.cometPower);
}

/** The number of slashes, and the norama they burned. */
export function slashTotals(slashes: readonly SlashEvent[]): { count: number; burned: string } {
  return { count: slashes.length, burned: slashes.reduce((n, s) => n + BigInt(s.burned), 0n).toString() };
}

const UNSPECIFIED = "Reason not stated";
const REASONS = new Map([
  ["double_sign", "Signed two blocks at one height"],
  ["missing_signature", "Missed too many blocks"],
  ["unspecified", UNSPECIFIED],
]);

/** x/slashing's reason in words. An unknown reason is shown as the chain wrote it (it was cleaned on the way in). */
export function slashReason(reason: string): string {
  return reason === "" ? UNSPECIFIED : (REASONS.get(reason) ?? reason);
}

/** A jail period in a sentence; "before the index began" when its start is not known. */
export function jailSentence(j: JailPeriod): string {
  const from = j.jailedHeight === null ? "before the index began" : `at block ${j.jailedHeight.toLocaleString("en-US")}`;
  const to = j.unjailedHeight === null ? "and is still jailed" : `until block ${j.unjailedHeight.toLocaleString("en-US")}`;
  return `Jailed ${from}, ${to}.`;
}

const STATUS_WORDS: Record<ValidatorStatus, string> = {
  bonded: "Active",
  unbonding: "Unbonding",
  unbonded: "Not active",
};

export function statusWord(status: ValidatorStatus): string {
  return STATUS_WORDS[status];
}

const PROGRESS_SCALE = 1_000_000n;

/** The two figures x/power's lambda is the larger of, each 0 to 1: bonded stake over the exit stake, and the epoch over the deadline. */
export function handoverTerms(bonded: string, exit: string, epoch: number, deadline: number): { stake: number; time: number } {
  const exitStake = BigInt(exit);
  const stake = exitStake === 0n ? 1 : Math.min(1, Number((BigInt(bonded) * PROGRESS_SCALE) / exitStake) / Number(PROGRESS_SCALE));
  const time = deadline === 0 ? 1 : Math.min(1, epoch / deadline);
  return { stake, time };
}

/** The validator count at which x/power's hand-over gate is met: twice the validators one cap allows. */
export function gateValidators(capShare: number): number {
  return capShare > 0 ? 2 * Math.ceil(1 / capShare) : 0;
}
