import type { ValidatorStatus } from "../../model/types";
import { ChainReadError } from "./client";
import { clean } from "./text";
import { decShare, digits, iso, optStr, rec } from "./wire";
import type { Rec } from "./wire";

const MAX_WEBSITE = 120;
const MAX_DETAILS = 280;
/** The staking module writes the zero time for "never": a rate never changed, a validator that is not unbonding. */
const ZERO_TIME_MS = 0;

const STATUS: Record<string, ValidatorStatus> = {
  BOND_STATUS_BONDED: "bonded",
  BOND_STATUS_UNBONDING: "unbonding",
  BOND_STATUS_UNBONDED: "unbonded",
};

export interface Commission {
  rate: number;
  maxRate: number;
  maxChangePerDay: number;
  updatedAt: string | null;
}

/** What the staking module's validator record says beyond who the validator is. */
export interface StakingFields {
  status: ValidatorStatus;
  /** Everything bonded to the validator, delegations included. */
  tokens: string;
  commission: Commission;
  minSelfDelegation: string;
  /** Written by the validator, cleaned and cut. */
  website: string;
  details: string;
  unbondsAt: string | null;
}

/** A time that is the zero time is no time. */
function realTime(value: unknown, what: string): string | null {
  const t = iso(value, what);
  return Date.parse(t) <= ZERO_TIME_MS ? null : t;
}

/** Reads the fields of one validator of the node's REST staking list (decimals are written with a point there). */
export function readStakingFields(v: Rec): StakingFields {
  const status = STATUS[String(v.status)];
  if (status === undefined) throw new ChainReadError("The chain sent a malformed validator status.");
  const c = rec(v.commission, "validator commission");
  const rates = rec(c.commission_rates, "validator commission");
  const description = v.description === undefined ? {} : rec(v.description, "validator description");
  return {
    status,
    tokens: digits(v.tokens, "validator tokens"),
    commission: {
      rate: decShare(rates.rate, "commission rate"),
      maxRate: decShare(rates.max_rate, "maximum commission rate"),
      maxChangePerDay: decShare(rates.max_change_rate, "maximum commission change"),
      updatedAt: realTime(c.update_time, "commission update time"),
    },
    minSelfDelegation: digits(v.min_self_delegation, "minimum self-delegation"),
    website: clean(optStr(description.website, "validator website", 512), MAX_WEBSITE),
    details: clean(optStr(description.details, "validator details", 2048), MAX_DETAILS),
    unbondsAt: status === "bonded" ? null : realTime(v.unbonding_time, "unbonding time"),
  };
}
