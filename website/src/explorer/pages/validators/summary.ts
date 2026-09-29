import type { UptimeDay, Validator, ValidatorSet } from "../../model/types";

export interface ValidatorsSummary {
  /** Share of voting power held by the founding committee, 0 to 1. */
  committeeShare: number;
  /** Share held by stake-backed validators, 0 to 1. Equals lambda. */
  communityShare: number;
  signing: number;
  total: number;
}

export function summarize(set: ValidatorSet): ValidatorsSummary {
  const committeeShare = set.validators.filter((v) => v.type === "committee").reduce((n, v) => n + v.power, 0);
  return {
    committeeShare,
    communityShare: set.lambda,
    signing: set.validators.filter((v) => !v.jailed).length,
    total: set.validators.length,
  };
}

/** Largest power first; jailed validators last; equal power ordered by name. */
export function rankValidators(validators: readonly Validator[]): Validator[] {
  return [...validators].sort((a, b) => {
    if (a.jailed !== b.jailed) return a.jailed ? 1 : -1;
    if (b.power !== a.power) return b.power - a.power;
    return a.ref.moniker.localeCompare(b.ref.moniker);
  });
}

/** "27 of 30 days fully up, 2 partial, 1 missed": the strip's text alternative. */
export function uptimeLabel(days: readonly UptimeDay[]): string {
  const count = (kind: UptimeDay) => days.filter((d) => d === kind).length;
  return `${count("ok")} of ${days.length} days fully up, ${count("partial")} partial, ${count("missed")} missed`;
}

/** The plain sentence above the hand-over track. */
export function handoverSentence(summary: ValidatorsSummary): string {
  const pct = Math.round(summary.committeeShare * 100);
  if (pct >= 100) return "All voting power sits with the founding committee today.";
  if (pct <= 0) return "No voting power sits with the founding committee any more. Stakers hold all of it.";
  return `Today, ${pct}% of voting power sits with the founding committee.`;
}
