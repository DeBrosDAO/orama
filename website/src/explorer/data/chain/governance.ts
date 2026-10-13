import type { EnactedState, Governance, GovernanceParams, Proposal, ProposalStatus, Vote, VoteOption } from "../../model/governance";
import { ChainReadError } from "./client";
import type { ChainClient } from "./client";
import { readContent } from "./governance-content";
import { lookup, queryPath } from "./module-query";
import { clean } from "./text";
import { account, decShare, digits, optArr, optStr, optUint, rec, str, uint } from "./wire";
import type { Rec } from "./wire";

const PARAMS = "orama.houses.v1.Query/Params";
const ENACTED = "orama.houses.v1.Query/Enacted";
const PROPOSAL = "orama.houses.v1.Query/Proposal";
const VOTE = "orama.houses.v1.Query/Vote";

const MAX_REASON = 300;
const NANOS_PER_MILLI = 1_000_000n;

const STATUSES: Record<string, ProposalStatus> = {
  VOTING: "voting",
  VETO_WINDOW: "veto_window",
  TIMELOCK: "timelock",
  EXECUTED: "executed",
  REJECTED: "rejected",
  FAILED: "failed",
  PROPOSAL_STATUS_UNSPECIFIED: "unspecified",
};
const OPTIONS: Record<string, VoteOption> = { YES: "yes", NO: "no", ABSTAIN: "abstain", VOTE_OPTION_UNSPECIFIED: "unspecified" };

function malformed(what: string): ChainReadError {
  return new ChainReadError(`The chain sent a malformed ${what}.`);
}

/** A time the chain keeps as Unix nanoseconds, as an ISO string; null when it is 0 (not set). */
function nanoTime(value: unknown, what: string): string | null {
  const nanos = BigInt(digits(value ?? "0", what));
  if (nanos === 0n) return null;
  const ms = Number(nanos / NANOS_PER_MILLI);
  if (!Number.isSafeInteger(ms)) throw malformed(what);
  return new Date(ms).toISOString();
}

function required(value: string | null, what: string): string {
  if (value === null) throw malformed(what);
  return value;
}

/** An enum as protojson writes it (its name, also for the zero value). */
function enumOf<T>(table: Record<string, T>, value: unknown, what: string): T {
  const found = table[optStr(value, what, 64)];
  if (found === undefined) throw malformed(what);
  return found;
}

export function readProposal(raw: unknown): Proposal {
  const p = rec(raw, "proposal");
  return {
    id: uint(p.id, "proposal id"),
    proposer: account(p.proposer, "proposer"),
    status: enumOf(STATUSES, p.status, "proposal status"),
    submittedAt: required(nanoTime(p.submit_unix_nano, "proposal submission time"), "proposal submission time"),
    votingEndsAt: required(nanoTime(p.voting_end_unix_nano, "proposal voting end"), "proposal voting end"),
    vetoEndsAt: nanoTime(p.veto_end_unix_nano, "proposal veto end"),
    timelockEndsAt: nanoTime(p.timelock_end_unix_nano, "proposal timelock end"),
    content: readContent(rec(p.content, "proposal content")),
    tokens: { yes: digits(p.token_yes, "token yes"), no: digits(p.token_no, "token no"), abstain: digits(p.token_abstain, "token abstain") },
    operators: {
      yes: optUint(p.operator_yes, "operator yes"),
      no: optUint(p.operator_no, "operator no"),
      abstain: optUint(p.operator_abstain, "operator abstain"),
      eligible: optUint(p.eligible_operators, "eligible operators"),
    },
    failReason: clean(optStr(p.fail_reason, "proposal failure reason", MAX_REASON * 4), MAX_REASON),
  };
}

/** One proposal by id; null when x/houses has none. */
export async function loadProposal(client: ChainClient, id: number): Promise<Proposal | null> {
  if (!Number.isSafeInteger(id) || id < 1) return null;
  const answer = await lookup(client, PROPOSAL, { proposal_id: id });
  if (answer === null) return null;
  const proposal = readProposal(rec(answer, "proposal").proposal);
  if (proposal.id !== id) throw malformed("proposal (another id than asked for)");
  return proposal;
}

/** One address's vote on a proposal; null when it has not voted. */
export async function loadVote(client: ChainClient, proposalId: number, voter: string): Promise<Vote | null> {
  const answer = await lookup(client, VOTE, { proposal_id: proposalId, voter });
  if (answer === null) return null;
  const v = rec(rec(answer, "vote").vote, "vote");
  return { proposalId: uint(v.proposal_id, "vote proposal"), voter: account(v.voter, "voter"), option: enumOf(OPTIONS, v.option, "vote option") };
}

function readParams(p: Rec): GovernanceParams {
  return {
    bootstrapExitStake: digits(p.bootstrap_exit_stake, "bootstrap exit stake"),
    tokenQuorum: decShare(p.token_quorum, "token quorum"),
    tokenPassThreshold: decShare(p.token_pass_threshold, "token pass threshold"),
    votingPeriodSeconds: uint(p.voting_period_seconds, "voting period"),
    houseBond: digits(p.house_bond, "house bond"),
    maxEligiblePerPrefix16: optUint(p.max_eligible_per_prefix16, "operator cap per /16"),
    maxEligiblePerAsn: optUint(p.max_eligible_per_asn, "operator cap per ASN"),
    minHouseSize: optUint(p.min_house_size, "minimum house size"),
    vetoWindowSeconds: uint(p.veto_window_seconds, "veto window"),
    parameterTimelockSeconds: uint(p.parameter_timelock_seconds, "parameter timelock"),
    upgradeTimelockSeconds: uint(p.upgrade_timelock_seconds, "upgrade timelock"),
    spendTimelockSeconds: uint(p.spend_timelock_seconds, "spend timelock"),
  };
}

function strings(value: unknown, what: string): string[] {
  return optArr(value, what).map((item) => account(item, what));
}

function readEnacted(e: Rec): EnactedState {
  const upgrade = e.scheduled_upgrade === null || e.scheduled_upgrade === undefined ? null : rec(e.scheduled_upgrade, "scheduled upgrade");
  const split = e.emission_split === null || e.emission_split === undefined ? null : rec(e.emission_split, "emission split");
  return {
    scheduledUpgrade: upgrade === null || upgrade.name === "" ? null : { name: clean(str(upgrade.name, "upgrade name", 256), 64), height: uint(upgrade.height, "upgrade height") },
    emissionSplit:
      split === null || optUint(split.validator_percent, "validator share") === 0
        ? null
        : {
            validator: uint(split.validator_percent, "validator share"),
            storage: optUint(split.storage_percent, "storage share"),
            relay: optUint(split.relay_percent, "relay share"),
            development: optUint(split.development_percent, "development share"),
          },
    powerBoundsActive: e.m_activated === true,
    relayReporters: strings(e.relay_reporters, "relay reporter"),
    codeUploadAllow: strings(e.code_upload_allow, "code upload address"),
    adapterAllow: strings(e.adapter_allow, "adapter address"),
  };
}

/** x/houses' parameters and what its enacted proposals have set. */
export async function loadGovernance(client: ChainClient): Promise<Governance> {
  const [params, enacted] = await Promise.all([client.get(queryPath(PARAMS)), client.get(queryPath(ENACTED))]);
  return {
    params: readParams(rec(rec(params, "governance parameters").params, "governance parameters")),
    enacted: readEnacted(rec(rec(enacted, "enacted state").enacted, "enacted state")),
  };
}
