import { PAID_BETA_MONTH } from "../funding";
import { WALLET_SCENARIOS, WALLET_USER_TARGET, walletRevenuePerUser } from "./model";
import type { Scenario } from "./model";

/**
 * Revenue illustrations, computed rather than typed in, so every figure on the
 * page can be reproduced from the assumptions printed next to it. In euros;
 * RootWallet's dollar benchmarks are converted at a fixed, cited rate.
 */

/** €1 = $1.137, 24 Sep 2026 (source "fx"). */
export const USD_PER_EUR = 1.137;

export const ORAMA_TAKE = 0.3;
/** The team plan, ~$10/month, charged in euros. */
export const TEAM_PLAN_EUR_MONTH = 9;
/** A support contract: ~10 nodes at a blended ~€2k per node per year. */
export const SUPPORT_CONTRACT_EUR_YEAR = 20_000;
export const HORIZON_MONTH = 36;

export interface OramaInputs {
  /** Paying teams three years after funding. */
  teamsAtHorizon: number;
  /** Average monthly usage spend per team, in euros. */
  spendEurMonth: number;
  /** Support contracts signed by month 18 and by month 36. */
  supportAt18: number;
  supportAt36: number;
}

export const ORAMA_INPUTS: Record<Scenario["id"], OramaInputs> = {
  conservative: { teamsAtHorizon: 400, spendEurMonth: 25, supportAt18: 1, supportAt36: 2 },
  base: { teamsAtHorizon: 1_500, spendEurMonth: 45, supportAt18: 1, supportAt36: 6 },
  upside: { teamsAtHorizon: 5_000, spendEurMonth: 60, supportAt18: 2, supportAt36: 15 },
};

/**
 * Paying teams at a given month: zero at the paid beta, growing along an
 * S-curve (exponent 1.6) to the three-year figure.
 */
export function payingTeams(inputs: OramaInputs, month: number): number {
  if (month <= PAID_BETA_MONTH) return 0;
  const progress = Math.min(1, (month - PAID_BETA_MONTH) / (HORIZON_MONTH - PAID_BETA_MONTH));
  return Math.round(inputs.teamsAtHorizon * progress ** 1.6);
}

/** Orama's own annual revenue run-rate at a month, in euros. */
export function oramaArrEur(inputs: OramaInputs, month: number): number {
  const teams = payingTeams(inputs, month);
  const usage = teams * inputs.spendEurMonth * 12 * ORAMA_TAKE;
  const plans = teams * TEAM_PLAN_EUR_MONTH * 12;
  const contracts = month >= HORIZON_MONTH ? inputs.supportAt36 : month >= 18 ? inputs.supportAt18 : 0;
  return usage + plans + contracts * SUPPORT_CONTRACT_EUR_YEAR;
}

/** RootWallet's annual fee run-rate at its user target, in euros. Fees only. */
export function walletArrEur(id: Scenario["id"]): number {
  return (walletRevenuePerUser(WALLET_SCENARIOS[id]) * WALLET_USER_TARGET) / USD_PER_EUR;
}

export const SCENARIO_IDS: Scenario["id"][] = ["conservative", "base", "upside"];

/** "~€119k", "~€1.9M": illustrations are rounded on purpose. */
export function formatApproxEur(amount: number): string {
  if (amount >= 1_000_000) return `~€${(amount / 1_000_000).toFixed(1)}M`;
  return `~€${Math.round(amount / 1_000)}k`;
}

/** Plain-language inputs, printed under the tables. */
export const PROJECTION_ASSUMPTIONS = [
  `Paying teams grow from zero at the month-${PAID_BETA_MONTH} paid beta along an S-curve`,
  "Teams spend €25 / €45 / €60 a month (Supabase earns about €51 per paying customer)",
  `Orama keeps ${ORAMA_TAKE * 100}% of usage, plus a ~€${TEAM_PLAN_EUR_MONTH}/month team plan`,
  `Support contracts at ~€${SUPPORT_CONTRACT_EUR_YEAR / 1000}k a year each`,
  `RootWallet: fees at ${WALLET_USER_TARGET.toLocaleString("en-US")} monthly users; Premium not included`,
] as const;
