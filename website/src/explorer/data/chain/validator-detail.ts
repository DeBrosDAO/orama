import { bech32Rehrp } from "../../model/bech32";
import type {
  JailPeriod,
  OperatorGroups,
  PowerOperator,
  SlashEvent,
  Validator,
  ValidatorDetail,
  ValidatorEpoch,
  ValidatorHistory,
  ValidatorSigning,
  ValidatorStatus,
} from "../../model/types";
import { ChainNotFound, ChainReadError, chainPath, withQuery } from "./client";
import type { ChainClient } from "./client";
import type { Directory, DirectoryValidator } from "./directory";
import { clean } from "./text";
import { validatorsOf } from "./validators";
import { account, arr, bool, digits, iso, optArr, optDigits, optStr, rec, str, uint } from "./wire";
import type { Rec } from "./wire";

const VALIDATOR_POWER = "query/orama.power.v1.Query/ValidatorPower";
const STAKING_DELEGATION = "query/cosmos.staking.v1beta1.Query/Delegation";
const NORAMA = "norama";
const UNLINKED = "unlinked";
const ACCOUNT_HRP = "orama";
const BPS_PER_UNIT = 10_000;
/** The newest closed epochs, slashes and jail periods a validator page reads: one indexer page each. */
const EPOCH_WINDOW = 60;
const SLASH_WINDOW = 50;
const JAIL_WINDOW = 20;
/** The indexer's largest page, and the pages of the validator list one read follows (a set of 300 is far above the chain's plan). */
const INDEX_PAGE = 100;
const MAX_SIGNING_PAGES = 3;
/**
 * The operator of at most this many validators is looked up (one x/power query each); the largest first.
 * The gateway gives a client network a burst of 30 module queries and 120 a minute (core/pkg/gateway/rate_limit_key.go),
 * and a validators page makes four more (the staking pool is REST; Lambda, x/power's Params, the current epoch and the
 * founding committee are queries), so a load stays inside the burst only with a few less than 26 lookups. The validators
 * past this are counted, not grouped, and the page says how many.
 */
export const MAX_OPERATOR_LOOKUPS = 24;
/** How many of those queries run at once: the gateway answers at most 16 module queries at a time. */
const LOOKUP_CONCURRENCY = 6;
const MAX_REASON = 64;

const STATUSES: ReadonlySet<string> = new Set<ValidatorStatus>(["bonded", "unbonding", "unbonded"]);

/** x/power's view of one validator: its CometBFT power and the operator its stake counts toward. Null when x/power knows no such validator. */
export async function loadPowerOf(client: ChainClient, valoper: string): Promise<{ cometPower: string; operator: PowerOperator } | null> {
  const body = rec(await client.get(withQuery(VALIDATOR_POWER, { json: JSON.stringify({ operator_address: valoper }) })), "validator power");
  const operator = optStr(body.operator, "validator operator", 120);
  if (operator === "") return null;
  return {
    cometPower: optDigits(body.comet_power, "comet power"),
    operator: operator === UNLINKED ? { kind: "unlinked" } : { kind: "registered", address: account(operator, "validator operator") },
  };
}

/** What the validator's own wallet has bonded to it, in norama; "0" when it has not bonded to its own validator. */
async function loadSelfBond(client: ChainClient, dv: DirectoryValidator): Promise<string> {
  const json = JSON.stringify({ delegator_addr: dv.ref.operator, validator_addr: dv.valoper });
  try {
    const body = rec(await client.get(withQuery(STAKING_DELEGATION, { json })), "delegation");
    const balance = rec(rec(body.delegation_response, "delegation").balance, "delegation balance");
    return balance.denom === NORAMA ? digits(balance.amount, "delegation balance") : "0";
  } catch (err) {
    if (err instanceof ChainNotFound) return "0";
    throw err;
  }
}

/** A validator's page: the staking record plus x/power's view and the operator's own bond. Null when no validator has that operator wallet. */
export async function loadValidator(client: ChainClient, directory: Directory, address: string): Promise<ValidatorDetail | null> {
  const dv = directory.byOperator.get(address);
  if (dv === undefined) return null;
  const [selfBond, power] = await Promise.all([loadSelfBond(client, dv), loadPowerOf(client, dv.valoper)]);
  const validator = validatorsOf(directory).find((v) => v.ref.operator === address);
  if (validator === undefined) return null;
  const others = BigInt(dv.tokens) - BigInt(selfBond);
  return {
    validator,
    website: dv.website,
    details: dv.details,
    commission: dv.commission,
    minSelfDelegation: dv.minSelfDelegation,
    selfBond,
    delegatedByOthers: (others < 0n ? 0n : others).toString(),
    unbondsAt: dv.unbondsAt,
    power,
  };
}

function status(value: unknown): ValidatorStatus {
  const text = str(value, "validator status", 16);
  if (!STATUSES.has(text)) throw new ChainReadError("The chain sent a malformed validator status.");
  return text as ValidatorStatus;
}

function readEpoch(raw: unknown): ValidatorEpoch {
  const e = rec(raw, "validator epoch");
  const signed = uint(e.signed_blocks, "signed blocks");
  const missed = uint(e.missed_blocks, "missed blocks");
  return {
    epoch: uint(e.epoch, "epoch"),
    cometPower: uint(e.comet_power, "comet power"),
    signedBlocks: signed,
    missedBlocks: missed,
    uptime: signed + missed === 0 ? null : Math.min(1, uint(e.uptime_bps, "uptime") / BPS_PER_UNIT),
    slashes: uint(e.slashes, "slash count"),
    slashedBurned: digits(e.slashed_burned, "slashed amount"),
    jailed: bool(e.jailed, "jailed flag"),
    status: status(e.status),
  };
}

function readSlash(raw: unknown): SlashEvent {
  const s = rec(raw, "slash");
  return {
    height: uint(s.height, "slash height"),
    time: iso(s.time, "slash time"),
    epoch: uint(s.epoch, "slash epoch"),
    reason: clean(optStr(s.reason, "slash reason", 128), MAX_REASON),
    power: uint(s.power, "slashed power"),
    burned: digits(s.burned, "slashed amount"),
  };
}

function optTime(value: unknown, what: string): string | null {
  return value === undefined || value === null ? null : iso(value, what);
}

function readJail(raw: unknown): JailPeriod {
  const j = rec(raw, "jail period");
  const jailed = uint(j.jailed_height, "jailed height");
  const unjailed = uint(j.unjailed_height, "unjailed height");
  return {
    jailedHeight: jailed === 0 ? null : jailed,
    jailedTime: optTime(j.jailed_time, "jailed time"),
    reason: clean(optStr(j.reason, "jail reason", 128), MAX_REASON),
    unjailedHeight: unjailed === 0 ? null : unjailed,
    unjailedTime: optTime(j.unjailed_time, "unjailed time"),
  };
}

function indexList(client: ChainClient, valoper: string, list: string, limit: number): Promise<Rec> {
  return client.get(withQuery(chainPath("index", "validators", valoper, list), { limit })).then((raw) => rec(raw, `validator ${list}`));
}

/**
 * A validator's closed epochs (signing, power), slashes and jail periods from
 * the chain indexer, newest first. Null when the index has not seen the
 * validator (it appears when the epoch after it joined closes).
 */
export async function loadHistory(client: ChainClient, directory: Directory, address: string): Promise<ValidatorHistory | null> {
  const dv = directory.byOperator.get(address);
  if (dv === undefined) return null;
  try {
    const [epochs, slashes, jails] = await Promise.all([
      indexList(client, dv.valoper, "epochs", EPOCH_WINDOW),
      indexList(client, dv.valoper, "slashes", SLASH_WINDOW),
      indexList(client, dv.valoper, "jails", JAIL_WINDOW),
    ]);
    return {
      epochs: arr(epochs.epochs, "validator epochs").map(readEpoch),
      slashes: optArr(slashes.slashes, "validator slashes").map(readSlash),
      jails: optArr(jails.jails, "validator jail periods").map(readJail),
    };
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

/** Every validator's signing in the newest closed epoch the index holds, highest power first. */
export async function loadSigning(client: ChainClient): Promise<ValidatorSigning[]> {
  const out: ValidatorSigning[] = [];
  for (let page = 1; page <= MAX_SIGNING_PAGES; page++) {
    const raw = rec(await client.get(withQuery(chainPath("index", "validators"), { page, limit: INDEX_PAGE })), "validator index");
    const rows = optArr(raw.validators, "validator index");
    for (const item of rows) {
      const v = rec(item, "indexed validator");
      if (v.last_epoch === undefined || v.last_epoch === null) continue;
      const operator = bech32Rehrp(str(v.operator, "validator operator", 120), ACCOUNT_HRP);
      if (operator === null) throw new ChainReadError("The chain sent a malformed validator operator address.");
      const last = readEpoch(v.last_epoch);
      out.push({ operator, epoch: last.epoch, uptime: last.uptime, missedBlocks: last.missedBlocks, slashes: last.slashes });
    }
    if (rows.length < INDEX_PAGE) return out;
  }
  throw new ChainReadError("The validator index is larger than this explorer reads.");
}

/** `read` over `items`, at most `limit` at a time, in order. */
async function mapLimit<T, R>(items: readonly T[], limit: number, read: (item: T) => Promise<R>): Promise<R[]> {
  const out: R[] = new Array<R>(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const i = next++;
      out[i] = await read(items[i] as T);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return out;
}

function groupKey(op: PowerOperator): string {
  return op.kind === "unlinked" ? UNLINKED : op.address;
}

/**
 * The active validators grouped by the operator x/power counts their stake
 * under, which is how its per-operator cap applies. x/power names the operator
 * only through ValidatorPower (one query per validator), so the largest
 * MAX_OPERATOR_LOOKUPS active validators are looked up and the rest counted.
 * A validator x/power does not know is left out of its groups.
 */
export async function loadOperatorGroups(client: ChainClient, directory: Directory): Promise<OperatorGroups> {
  const all = validatorsOf(directory).filter((v) => v.power > 0);
  const byOperator = new Map(directory.validators.map((v) => [v.ref.operator, v]));
  const looked = [...all].sort((a, b) => b.power - a.power || a.ref.operator.localeCompare(b.ref.operator)).slice(0, MAX_OPERATOR_LOOKUPS);
  const powers = await mapLimit(looked, LOOKUP_CONCURRENCY, (v) => loadPowerOf(client, (byOperator.get(v.ref.operator) as DirectoryValidator).valoper));
  const groups = new Map<string, { operator: PowerOperator; validators: Validator[]; power: number }>();
  looked.forEach((v, i) => {
    const p = powers[i];
    if (p === null || p === undefined) return;
    const key = groupKey(p.operator);
    const g = groups.get(key) ?? { operator: p.operator, validators: [], power: 0 };
    g.validators.push(v);
    g.power += v.power;
    groups.set(key, g);
  });
  return {
    groups: [...groups.values()].sort((a, b) => b.power - a.power),
    unresolved: all.length - looked.length,
  };
}
