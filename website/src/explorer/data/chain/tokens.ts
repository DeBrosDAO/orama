import { FACTORY_RANGE_END, splitFactoryDenom } from "../../model/tokens";
import type { TokenDetail, TokenHolder, TokenParams, TokenRow } from "../../model/tokens";
import type { KeyPage, KeyQuery } from "../../model/paging";
import { ChainReadError } from "./client";
import type { ChainClient } from "./client";
import { lookup, nextKeyOf, pagination, queryPath, readKeyPage } from "./module-query";
import { clean } from "./text";
import { account, bool, digits, optArr, optStr, optUint, rec, str } from "./wire";
import type { Rec } from "./wire";

const TOTAL_SUPPLY = "cosmos.bank.v1beta1.Query/TotalSupply";
const DENOM_OWNERS = "cosmos.bank.v1beta1.Query/DenomOwners";
const TOKEN = "orama.token.v1.Query/Token";
const FROZEN = "orama.token.v1.Query/Frozen";
const TOKEN_PARAMS = "orama.token.v1.Query/Params";

const MAX_DENOM = 160;
const MAX_NAME = 64;
const MAX_SYMBOL = 32;
const MAX_DESCRIPTION = 512;

function malformed(what: string): ChainReadError {
  return new ChainReadError(`The chain sent a malformed ${what}.`);
}

/** The denom of a coin, checked to be a factory denom whose creator is a real address; null for any other denom. */
function factoryRow(coin: Rec): TokenRow | null {
  const denom = str(coin.denom, "token denom", MAX_DENOM);
  const parts = splitFactoryDenom(denom);
  if (parts === null) return null;
  return { denom, creator: account(parts.creator, "token creator"), subdenom: parts.subdenom, supply: digits(coin.amount, "token supply") };
}

/**
 * The tokens x/token has issued, as the bank lists the denoms, in denom order. Every issued denom is
 * `factory/<creator>/<subdenom>` and the bank keeps denoms sorted, so the run of them ends at the
 * first denom past that prefix and the page after it is not asked for.
 */
export async function loadTokens(client: ChainClient, query: KeyQuery): Promise<KeyPage<TokenRow>> {
  const body = rec(await client.get(queryPath(TOTAL_SUPPLY, { pagination: pagination(query) })), "token list");
  const coins = optArr(body.supply, "token list").map((coin) => rec(coin, "token supply"));
  const items = coins.map(factoryRow).filter((row): row is TokenRow => row !== null);
  const pastRun = coins.some((coin) => str(coin.denom, "token denom", MAX_DENOM) >= FACTORY_RANGE_END);
  return { items, nextKey: pastRun ? null : nextKeyOf(body) };
}

function address(value: unknown, what: string): string | null {
  const text = optStr(value, what, 120);
  return text === "" ? null : account(text, what);
}

/** One token record as x/token's `Token` query writes it (proto field names). */
export function readToken(raw: unknown): TokenDetail {
  const t = rec(raw, "token");
  const denom = str(t.denom, "token denom", MAX_DENOM);
  const parts = splitFactoryDenom(denom);
  if (parts === null) throw malformed("token denom");
  const ext = rec(t.extensions, "token capabilities");
  return {
    denom,
    creator: account(t.creator, "token creator"),
    subdenom: parts.subdenom,
    name: clean(str(t.name, "token name", MAX_NAME * 4), MAX_NAME),
    symbol: clean(str(t.symbol, "token symbol", MAX_SYMBOL * 4), MAX_SYMBOL),
    description: clean(optStr(t.description, "token description", MAX_DESCRIPTION * 4), MAX_DESCRIPTION),
    extensions: {
      mint: bool(ext.mint, "token mint flag"),
      freeze: bool(ext.freeze, "token freeze flag"),
      permanentDelegate: address(ext.permanent_delegate, "token permanent delegate"),
      transferFeeBps: optUint(ext.transfer_fee_bps, "token transfer fee"),
      nonTransferable: bool(ext.non_transferable, "token transferability"),
      pause: bool(ext.pause, "token pause flag"),
      transferHook: address(ext.transfer_hook, "token transfer hook"),
    },
    paused: bool(t.paused, "token paused flag"),
    shieldable: bool(t.shieldable, "token shieldable flag"),
    issued: digits(t.issued, "token issued supply"),
    depositAmount: digits(t.deposit_amount, "token deposit"),
  };
}

/** One token by denom; null when x/token has no such token. */
export async function loadToken(client: ChainClient, denom: string): Promise<TokenDetail | null> {
  if (splitFactoryDenom(denom) === null) return null;
  const answer = await lookup(client, TOKEN, { denom });
  if (answer === null) return null;
  const token = readToken(rec(answer, "token").token);
  if (token.denom !== denom) throw malformed("token (another denom than asked for)");
  return token;
}

function readHolder(raw: unknown): TokenHolder {
  const owner = rec(raw, "token holder");
  return { address: account(owner.address, "holder address"), balance: digits(rec(owner.balance, "holder balance").amount, "holder balance") };
}

/** The accounts that hold a token, in address order. */
export async function loadHolders(client: ChainClient, denom: string, query: KeyQuery): Promise<KeyPage<TokenHolder>> {
  const answer = await client.get(queryPath(DENOM_OWNERS, { denom, pagination: pagination(query) }));
  return readKeyPage(answer, "denom_owners", "token holders", readHolder);
}

/** Whether x/token has frozen the account for the denom. */
export async function loadFrozen(client: ChainClient, denom: string, holder: string): Promise<boolean> {
  const answer = await client.get(queryPath(FROZEN, { denom, account: holder }));
  return bool(rec(answer, "frozen answer").frozen, "frozen flag");
}

export async function loadTokenParams(client: ChainClient): Promise<TokenParams> {
  const params = rec(rec(await client.get(queryPath(TOKEN_PARAMS)), "token parameters").params, "token parameters");
  return { creationFee: digits(params.creation_fee, "token creation fee"), depositPerByte: digits(params.deposit_per_byte, "token deposit rate") };
}
