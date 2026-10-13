import { bech32Rehrp } from "../../model/bech32";
import type {
  AccountReward,
  AccountRewards,
  Earnings,
  EpochRewards,
  RewardListQuery,
  ValidatorReward,
  ValidatorRewardHistory,
  ValidatorRewardEpoch,
} from "../../model/rewards";
import type { KeyQuery } from "../../model/paging";
import type { ValidatorRef } from "../../model/types";
import { shortAddress } from "../../model/units";
import { pageLimit, pageNumber } from "./aggregates";
import { ChainNotFound, ChainReadError, chainPath, withQuery } from "./client";
import type { ChainClient } from "./client";
import type { Directory } from "./directory";
import { REWARD_CURSOR, nextCursor } from "./paging";
import { addressQuery } from "./wallet";
import { bool, digits, optArr, optDigits, optUint, rec, str, uint } from "./wire";

const VALIDATOR_HRP = "orama";
const MAX_ADDRESS = 120;
const FEES_EARNINGS = "orama.fees.v1.Query/Earnings";

function refOf(valoper: string, directory: Directory): ValidatorRef {
  const operator = bech32Rehrp(valoper, VALIDATOR_HRP);
  if (operator === null) throw new ChainReadError("The chain sent a malformed validator operator address.");
  return directory.byOperator.get(operator)?.ref ?? { moniker: shortAddress(operator), operator };
}

function readReward(raw: unknown, directory: Directory): ValidatorReward {
  const r = rec(raw, "validator reward");
  const reward: ValidatorReward = {
    epoch: uint(r.epoch, "reward epoch"),
    ref: refOf(str(r.operator, "validator operator", MAX_ADDRESS), directory),
    amount: digits(r.amount, "reward amount"),
    commission: digits(r.commission, "reward commission"),
    operatorShare: digits(r.operator_share, "reward operator share"),
    delegators: digits(r.delegators, "reward delegator share"),
    forceBonded: digits(r.force_bonded, "reward force-bonded part"),
  };
  // The chain's events add up (chain/indexer/agg_rewards.go checks them); an answer that does not is not shown.
  if (BigInt(reward.operatorShare) + BigInt(reward.delegators) !== BigInt(reward.amount)) {
    throw new ChainReadError("The chain sent a reward whose parts do not add up.");
  }
  return reward;
}

function pagedPath(path: string, query: RewardListQuery): { path: string; page: number; limit: number } {
  const page = pageNumber(query.page);
  const limit = pageLimit(query.limit);
  return { path: withQuery(path, { page, limit }), page, limit };
}

/** What a closed epoch paid each validator. Null when the epoch is not closed in the index. */
export async function loadEpochRewards(
  client: ChainClient,
  directory: () => Promise<Directory>,
  epoch: number,
  query: RewardListQuery,
): Promise<EpochRewards | null> {
  if (!Number.isSafeInteger(epoch) || epoch < 1) return null;
  const paged = pagedPath(chainPath("index", "epochs", String(epoch), "rewards"), query);
  let body: unknown;
  try {
    body = await client.get(paged.path);
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
  const b = rec(body, "epoch rewards");
  const recorded = bool(b.recorded, "epoch rewards flag");
  const rows = optArr(b.rewards, "epoch rewards");
  if (!recorded && rows.length > 0) throw new ChainReadError("The chain sent rewards for an epoch it says it did not record.");
  const dir = await directory();
  const items = rows.map((row) => readReward(row, dir));
  return { epoch, recorded, items, page: paged.page, limit: paged.limit, hasMore: items.length === paged.limit };
}

function readRewardEpoch(raw: unknown, directory: Directory): ValidatorRewardEpoch {
  const r = rec(raw, "validator reward epoch");
  const recorded = bool(r.recorded, "reward recorded flag");
  const reward = r.reward === null || r.reward === undefined ? null : readReward(r.reward, directory);
  if (!recorded && reward !== null) throw new ChainReadError("The chain sent a reward for an epoch it says it did not record.");
  return { epoch: uint(r.epoch, "reward epoch"), recorded, reward };
}

/** A validator's closed epochs, newest first, each with what it was paid. Null when the index has not seen the validator. */
export async function loadValidatorRewards(
  client: ChainClient,
  directory: Directory,
  address: string,
  query: RewardListQuery,
): Promise<ValidatorRewardHistory | null> {
  const dv = directory.byOperator.get(address);
  if (dv === undefined) return null;
  const paged = pagedPath(chainPath("index", "validators", dv.valoper, "rewards"), query);
  let body: unknown;
  try {
    body = await client.get(paged.path);
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
  const items = optArr(rec(body, "validator rewards").rewards, "validator rewards").map((row) => readRewardEpoch(row, directory));
  return { items, page: paged.page, limit: paged.limit, hasMore: items.length === paged.limit };
}

function readAccountReward(raw: unknown, directory: Directory): AccountReward {
  const r = rec(raw, "account reward");
  const role = str(r.role, "reward role", 16);
  if (role !== "delegator" && role !== "operator") throw new ChainReadError("The chain sent a malformed reward role.");
  return {
    epoch: uint(r.epoch, "reward epoch"),
    validator: refOf(str(r.validator, "validator operator", MAX_ADDRESS), directory),
    role,
    amount: digits(r.amount, "reward amount"),
    forceBonded: digits(r.force_bonded, "reward force-bonded part"),
  };
}

/**
 * What an account was credited by x/power, newest epoch first. An account nothing named has an empty
 * list. The first page is read by number, each next one by the cursor the page before it named
 * (`query.key`), so a deep page costs what the first does.
 */
export async function loadAccountRewards(
  client: ChainClient,
  directory: Directory,
  address: string,
  query: KeyQuery,
): Promise<AccountRewards> {
  const limit = pageLimit(query.limit);
  const where = query.key === null ? { page: 1, limit } : { cursor: query.key, limit };
  const b = rec(await client.get(withQuery(chainPath("index", "accounts", address, "rewards"), where)), "account rewards");
  const items = optArr(b.rewards, "account rewards").map((row) => readAccountReward(row, directory));
  return {
    address,
    recordedFromEpoch: optUint(b.recorded_from_epoch, "first recorded epoch"),
    items,
    nextKey: nextCursor(b, items.length, limit, REWARD_CURSOR),
  };
}

/** The balance of an account's earnings, from x/fees: a live read of the chain, not of the index. */
export async function loadEarnings(client: ChainClient, address: string): Promise<Earnings> {
  const body = rec(await client.get(addressQuery(FEES_EARNINGS, "address", address)), "earnings");
  return { address, balance: optDigits(body.balance, "earnings balance") };
}
