import type { Handover, Validator, ValidatorSet } from "../../model/types";
import { chainPath } from "./client";
import type { ChainClient } from "./client";
import type { Directory } from "./directory";
import { decShare, digits, rec, uint } from "./wire";

const LAMBDA = "query/orama.power.v1.Query/Lambda";
const POWER_PARAMS = "query/orama.power.v1.Query/Params";
const CURRENT_EPOCH = "query/orama.emission.v1.Query/CurrentEpoch";
const POOL = chainPath("staking", "pool");
/** A coalition above this share of the voting power can halt the chain: one third. */
const HALT_NUM = 1n;
const HALT_DEN = 3n;
const SHARE_SCALE = 1_000_000n;
const BPS_PER_UNIT = 10_000;

/** The fewest validators that together hold more than a third of the voting power. */
export function nakamotoCoefficient(powers: readonly bigint[]): number {
  const total = powers.reduce((n, p) => n + p, 0n);
  if (total === 0n) return 0;
  let held = 0n;
  let count = 0;
  for (const p of [...powers].sort((a, b) => (a === b ? 0 : a > b ? -1 : 1))) {
    held += p;
    count += 1;
    if (held * HALT_DEN > total * HALT_NUM) break;
  }
  return count;
}

export function powerShare(power: bigint, total: bigint): number {
  return total === 0n ? 0 : Number((power * SHARE_SCALE) / total) / Number(SHARE_SCALE);
}

export function validatorsOf(directory: Directory): Validator[] {
  return directory.validators.map((v) => ({
    ref: v.ref,
    type: v.committee ? "committee" : "community",
    power: powerShare(v.power, directory.totalPower),
    jailed: v.jailed,
    status: v.status,
    commission: v.commission.rate,
    tokens: v.tokens,
  }));
}

/**
 * Lambda and what moves it, from x/power's Lambda and Params queries, the
 * staking pool and the emission epoch. Lambda is the larger of two terms and
 * never falls (x/power types.ComputeLambda): bonded stake over the exit stake,
 * and epochs since genesis over the deadline. The query exposes neither term,
 * so the page shows the two figures they are made of, not a computed term.
 */
function handoverOf(lambdaRaw: unknown, paramsRaw: unknown, poolRaw: unknown, epochRaw: unknown): Handover {
  const lambda = rec(lambdaRaw, "lambda");
  const params = rec(rec(paramsRaw, "power parameters").params, "power parameters");
  const pool = rec(rec(poolRaw, "staking pool").pool, "staking pool");
  const epoch = rec(rec(epochRaw, "epoch").epoch_state, "epoch");
  return {
    lambda: decShare(lambda.lambda, "lambda"),
    stake: { bonded: digits(pool.bonded_tokens, "bonded tokens"), exit: digits(params.bootstrap_exit_stake, "bootstrap exit stake") },
    epochs: { current: uint(epoch.current_epoch, "epoch number"), deadline: uint(params.bootstrap_deadline_epochs, "bootstrap deadline") },
    preGateCap: decShare(params.pre_gate_lambda_cap, "pre-gate lambda cap"),
    cap: {
      current: uint(lambda.current_cap_bps, "current cap") / BPS_PER_UNIT,
      normal: decShare(params.cap_fraction_normal, "normal cap"),
      reduced: decShare(params.cap_fraction_reduced, "reduced cap"),
      stepDownCount: uint(params.cap_step_down_validator_count, "cap step-down count"),
    },
  };
}

export async function loadValidatorSet(client: ChainClient, directory: Directory): Promise<ValidatorSet> {
  const [lambdaRaw, paramsRaw, poolRaw, epochRaw] = await Promise.all([
    client.get(LAMBDA),
    client.get(POWER_PARAMS),
    client.get(POOL),
    client.get(CURRENT_EPOCH),
  ]);
  const handover = handoverOf(lambdaRaw, paramsRaw, poolRaw, epochRaw);
  return {
    lambda: handover.lambda,
    nakamoto: nakamotoCoefficient(directory.validators.map((v) => v.power)),
    totalStaked: handover.stake.bonded,
    jailed: directory.validators.filter((v) => v.jailed).length,
    validators: validatorsOf(directory),
    handover,
  };
}
