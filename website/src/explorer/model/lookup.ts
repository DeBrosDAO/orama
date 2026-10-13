import type { AssetPaths } from "./assets-routes";
import { bech32Hrp } from "./bech32";
import { splitFactoryDenom } from "./tokens";

const ACCOUNT_HRP = "orama";
const POSITIVE_INT = /^[1-9][0-9]{0,14}$/;
const ASSET_ID = /^[0-9a-fA-F]{64}$/;

/** A whole number from 1, the shape of a collection, listing, proposal or code id; null for any other text. */
export function parseId(text: string): number | null {
  if (!POSITIVE_INT.test(text)) return null;
  const n = Number(text);
  return Number.isSafeInteger(n) ? n : null;
}

/** An asset id in the one spelling the explorer uses, 64 lower-case hexadecimal characters; null for any other text. */
export function parseAssetId(text: string): string | null {
  return ASSET_ID.test(text) ? text.toLowerCase() : null;
}

/** An account address with a valid checksum, in lower case; null for any other text. */
export function parseAccount(text: string): string | null {
  const lower = text.toLowerCase();
  return bech32Hrp(lower) === ACCOUNT_HRP ? lower : null;
}

/** A token's denom from text: `factory/<creator>/<subdenom>`, or `<creator>/<subdenom>`; the creator must be a valid address. */
export function parseDenom(text: string): string | null {
  const denom = text.startsWith("factory/") ? text : `factory/${text}`;
  const parts = splitFactoryDenom(denom);
  if (parts === null) return null;
  const creator = parseAccount(parts.creator);
  return creator === null ? null : `factory/${creator}/${parts.subdenom}`;
}

/** The token page a denom names. */
export function tokenTarget(text: string, paths: AssetPaths): string | null {
  const denom = parseDenom(text);
  return denom === null ? null : paths.token(denom);
}

/** An asset id opens the NFT, an address its holder's NFTs, a number a collection. */
export function nftTarget(text: string, paths: AssetPaths): string | null {
  const asset = parseAssetId(text);
  if (asset !== null) return paths.nft(asset);
  const owner = parseAccount(text);
  if (owner !== null) return paths.nftsOf(owner);
  const id = parseId(text);
  return id === null ? null : paths.collection(id);
}

/** An address opens the contract at it, a number the code with that id. */
export function contractTarget(text: string, paths: AssetPaths): string | null {
  const address = parseAccount(text);
  if (address !== null) return paths.contract(address);
  const id = parseId(text);
  return id === null ? null : paths.code(id);
}

export function listingTarget(text: string, paths: AssetPaths): string | null {
  const id = parseId(text);
  return id === null ? null : paths.listing(id);
}

export function proposalTarget(text: string, paths: AssetPaths): string | null {
  const id = parseId(text);
  return id === null ? null : paths.proposal(id);
}
