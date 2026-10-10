import type { Validator, ValidatorSet } from "../../model/types";
import { chainPath } from "./client";
import type { ChainClient } from "./client";
import type { Directory } from "./directory";
import { digits, rec, share } from "./wire";

const LAMBDA = "query/orama.power.v1.Query/Lambda";
const POOL = chainPath("staking", "pool");
/** A coalition above this share of the voting power can halt the chain: one third. */
const HALT_NUM = 1n;
const HALT_DEN = 3n;
const SHARE_SCALE = 1_000_000n;

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

function powerShare(power: bigint, total: bigint): number {
  return total === 0n ? 0 : Number((power * SHARE_SCALE) / total) / Number(SHARE_SCALE);
}

export function validatorsOf(directory: Directory): Validator[] {
  return directory.validators.map((v) => ({
    ref: v.ref,
    type: v.committee ? "committee" : "community",
    power: powerShare(v.power, directory.totalPower),
    jailed: v.jailed,
  }));
}

export async function loadValidatorSet(client: ChainClient, directory: Directory): Promise<ValidatorSet> {
  const [lambdaRaw, poolRaw] = await Promise.all([client.get(LAMBDA), client.get(POOL)]);
  const pool = rec(rec(poolRaw, "staking pool").pool, "staking pool");
  return {
    lambda: share(rec(lambdaRaw, "lambda").lambda, "lambda"),
    nakamoto: nakamotoCoefficient(directory.validators.map((v) => v.power)),
    totalStaked: digits(pool.bonded_tokens, "bonded tokens"),
    jailed: directory.validators.filter((v) => v.jailed).length,
    validators: validatorsOf(directory),
  };
}
