import type { MessageField, Norama } from "./types";

export type ProposalStatus = "voting" | "veto_window" | "timelock" | "executed" | "rejected" | "failed" | "unspecified";

export interface ProposalContent {
  /** "Parameter change", "Software upgrade" and the other kinds of x/houses. */
  kind: string;
  /** What the proposal would do, one labelled fact each. */
  facts: MessageField[];
}

export interface Proposal {
  id: number;
  proposer: string;
  status: ProposalStatus;
  submittedAt: string;
  votingEndsAt: string;
  /** Set for a proposal the operator house may veto; null otherwise. */
  vetoEndsAt: string | null;
  /** When the proposal may run, after the vote and any veto window; null until set. */
  timelockEndsAt: string | null;
  content: ProposalContent;
  /** The token house's weighted votes, in norama of bonded stake. */
  tokens: { yes: Norama; no: Norama; abstain: Norama };
  /** The operator house's votes, one per operator identity. */
  operators: { yes: number; no: number; abstain: number; eligible: number };
  /** Why the proposal failed; empty for any other status. */
  failReason: string;
}

export type VoteOption = "yes" | "no" | "abstain" | "unspecified";

export interface Vote {
  proposalId: number;
  voter: string;
  option: VoteOption;
}

/** x/houses' parameters. Only some can be changed by a proposal; the rest are fixed at genesis. */
export interface GovernanceParams {
  bootstrapExitStake: Norama;
  /** Share of bonded stake that must take part in the token house, 0 to 1. */
  tokenQuorum: number;
  /** Share of the participating weight that must vote yes, 0 to 1. */
  tokenPassThreshold: number;
  votingPeriodSeconds: number;
  houseBond: Norama;
  maxEligiblePerPrefix16: number;
  maxEligiblePerAsn: number;
  minHouseSize: number;
  vetoWindowSeconds: number;
  parameterTimelockSeconds: number;
  upgradeTimelockSeconds: number;
  spendTimelockSeconds: number;
}

/** What enacted proposals have set, which other modules read. */
export interface EnactedState {
  scheduledUpgrade: { name: string; height: number } | null;
  emissionSplit: { validator: number; storage: number; relay: number; development: number } | null;
  powerBoundsActive: boolean;
  relayReporters: string[];
  codeUploadAllow: string[];
  adapterAllow: string[];
}

export interface Governance {
  params: GovernanceParams;
  enacted: EnactedState;
}

const STATUS_TEXT: Record<ProposalStatus, string> = {
  voting: "Voting",
  veto_window: "Veto window",
  timelock: "Timelock",
  executed: "Executed",
  rejected: "Rejected",
  failed: "Failed",
  unspecified: "Unknown",
};

export function statusText(status: ProposalStatus): string {
  return STATUS_TEXT[status];
}

const MINUTE = 60;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** "14 days", "1 h 30 m", "45 s": a span of seconds the way a proposal's windows are read. */
export function durationText(seconds: number): string {
  if (seconds >= DAY && seconds % DAY === 0) return `${seconds / DAY} ${seconds === DAY ? "day" : "days"}`;
  if (seconds >= DAY) return `${Math.floor(seconds / DAY)} d ${durationText(seconds % DAY)}`;
  if (seconds >= HOUR) {
    const minutes = Math.floor((seconds % HOUR) / MINUTE);
    return minutes === 0 ? `${Math.floor(seconds / HOUR)} h` : `${Math.floor(seconds / HOUR)} h ${minutes} m`;
  }
  if (seconds >= MINUTE) return seconds % MINUTE === 0 ? `${seconds / MINUTE} m` : `${Math.floor(seconds / MINUTE)} m ${seconds % MINUTE} s`;
  return `${seconds} s`;
}

/** "in 4 d 3 h", "in 2 h 5 m", "in 12 m": how long until a future time; "in under a minute" for the last minute, and "now" for a time that has come. */
export function untilText(iso: string, nowMs: number): string {
  const seconds = Math.floor((Date.parse(iso) - nowMs) / 1000);
  if (Number.isNaN(seconds) || seconds <= 0) return "now";
  if (seconds < MINUTE) return "in under a minute";
  const days = Math.floor(seconds / DAY);
  const hours = Math.floor((seconds % DAY) / HOUR);
  const minutes = Math.floor((seconds % HOUR) / MINUTE);
  if (days > 0) return hours === 0 ? `in ${days} d` : `in ${days} d ${hours} h`;
  if (hours > 0) return minutes === 0 ? `in ${hours} h` : `in ${hours} h ${minutes} m`;
  return `in ${minutes} m`;
}
