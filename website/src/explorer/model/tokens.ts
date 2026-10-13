import type { Norama } from "./types";

/** x/token's genesis parameters. */
export interface TokenParams {
  /** Burned when a token is created. */
  creationFee: Norama;
  /** Locked per byte of a token's name, symbol, description and subdenom. */
  depositPerByte: Norama;
}

/** One issued token as the bank lists it: its denom and everything in circulation. */
export interface TokenRow {
  /** factory/<creator>/<subdenom>. */
  denom: string;
  creator: string;
  subdenom: string;
  /** The bank's supply of the denom, in the token's own smallest unit (a token record has no decimals field). */
  supply: string;
}

/** The capabilities a token was created with and still holds; each can only be renounced. */
export interface TokenExtensions {
  mint: boolean;
  freeze: boolean;
  /** The account that may move the token out of any holder, or null. */
  permanentDelegate: string | null;
  /** Share of each transfer burned, in basis points; 0 means none. */
  transferFeeBps: number;
  nonTransferable: boolean;
  pause: boolean;
  /** The contract the chain calls on every transfer, or null. */
  transferHook: string | null;
}

/** x/token's record of one token. */
export interface TokenDetail {
  denom: string;
  creator: string;
  subdenom: string;
  name: string;
  symbol: string;
  description: string;
  extensions: TokenExtensions;
  paused: boolean;
  /** One-way flag: the token may enter the shielded pool. */
  shieldable: boolean;
  /** x/token's running total of the denom (the bank's supply by the module's invariant). */
  issued: string;
  /** Norama locked for the token's metadata. */
  depositAmount: Norama;
}

export interface TokenHolder {
  address: string;
  balance: string;
}

const FACTORY_PREFIX = "factory/";
/** The first string after every denom that starts with `factory/`: where their run ends in the bank's sorted store. */
export const FACTORY_RANGE_END = "factory0";
const SUBDENOM = /^[a-z][a-z0-9]{1,43}$/;
const FACTORY_SEGMENTS = 3;
const BASIS_POINTS_PER_PERCENT = 100;

/** The creator and subdenom of a factory denom, or null for any other denom (shape only: the creator's checksum is checked where it is read). */
export function splitFactoryDenom(denom: string): { creator: string; subdenom: string } | null {
  if (!denom.startsWith(FACTORY_PREFIX)) return null;
  const parts = denom.split("/");
  const creator = parts[1];
  const subdenom = parts[2];
  if (parts.length !== FACTORY_SEGMENTS || creator === undefined || subdenom === undefined) return null;
  return creator !== "" && SUBDENOM.test(subdenom) ? { creator, subdenom } : null;
}

/** The capabilities the token still holds, as short phrases; empty for a token that holds none. */
export function heldPowers(ext: TokenExtensions): string[] {
  const out: string[] = [];
  if (ext.mint) out.push("The creator can mint more");
  if (ext.freeze) out.push("The creator can freeze accounts");
  if (ext.permanentDelegate !== null) out.push("A permanent delegate can move it out of any holder");
  if (ext.transferFeeBps > 0) out.push(`${ext.transferFeeBps / BASIS_POINTS_PER_PERCENT}% of every transfer is burned`);
  if (ext.nonTransferable) out.push("It cannot be transferred");
  if (ext.pause) out.push("The creator can pause it");
  if (ext.transferHook !== null) out.push("A contract is called on every transfer");
  return out;
}
