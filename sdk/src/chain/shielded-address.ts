import { bech32m } from "@scure/base";

/**
 * The text form of an Orama shielded address: the 43 raw bytes of an Orchard address (an 11-byte
 * diversifier and the 32-byte transmission key) in Bech32m (BIP-350) with the human-readable part
 * `oramas`. It is 82 characters, written in lowercase; a decoder also accepts all capitals and
 * refuses a mix of cases.
 *
 * This is the TypeScript twin of the Rust codec in chain/x/shielded/wallet/src/address.rs and of
 * the Go one in core/pkg/shieldedaddr. The three check a string in the same order and report the
 * same codes, and all read the one vector file,
 * chain/x/shielded/wallet/testdata/addresses/addresses.json.
 *
 * The codec is structural: it accepts any 43 bytes. Whether they are an address Orchard can pay (a
 * curve point) is for the wallet that builds the bundle.
 */

/** The human-readable part of a shielded address. */
export const SHIELDED_ADDRESS_HRP = "oramas";
/** The bytes in a raw Orchard address. */
export const SHIELDED_ADDRESS_BYTES = 43;
/** The characters in an encoded address. */
export const SHIELDED_ADDRESS_LENGTH = 82;

/** The characters after the separator: 69 for the address and 6 of checksum. */
const DATA_LENGTH = 75;
const CHECKSUM_LENGTH = 6;
const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";

/** Why a string is not a shielded address; the values are the names the vector file uses. */
export type ShieldedAddressErrorCode =
  | "mixed_case"
  | "no_separator"
  | "wrong_prefix"
  | "bad_character"
  | "wrong_length"
  | "bad_checksum"
  | "bad_padding";

const MESSAGES: Record<ShieldedAddressErrorCode, string> = {
  mixed_case: "mixes upper and lower case",
  no_separator: "has no '1' separator",
  wrong_prefix: "does not start with oramas1",
  bad_character: "has a character outside the Bech32 alphabet",
  wrong_length: "is not 82 characters long",
  bad_checksum: "has a wrong Bech32m checksum",
  bad_padding: "has non-zero padding bits",
};

/** Thrown by {@link decodeShieldedAddress}; `code` says which check failed. */
export class ShieldedAddressError extends Error {
  readonly code: ShieldedAddressErrorCode;
  constructor(code: ShieldedAddressErrorCode) {
    super(`not a shielded address: it ${MESSAGES[code]}`);
    this.name = "ShieldedAddressError";
    this.code = code;
  }
}

/** The shielded address text of a raw 43-byte Orchard address, in lowercase. */
export function encodeShieldedAddress(raw: Uint8Array): string {
  if (raw.length !== SHIELDED_ADDRESS_BYTES) {
    throw new RangeError(`a raw shielded address is ${SHIELDED_ADDRESS_BYTES} bytes, got ${raw.length}`);
  }
  return bech32m.encode(SHIELDED_ADDRESS_HRP, bech32m.toWords(raw));
}

/**
 * The raw 43 bytes of a shielded address, or a {@link ShieldedAddressError} for the first reason it
 * is not one: mixed case, no separator, a prefix other than oramas, a character outside the
 * alphabet, a data part other than 75 characters, the Bech32m checksum (an address with the
 * original Bech32 checksum fails here) and a non-zero padding bit, in that order.
 */
export function decodeShieldedAddress(address: string): Uint8Array {
  const hasUpper = /[A-Z]/.test(address);
  const hasLower = /[a-z]/.test(address);
  if (hasUpper && hasLower) throw new ShieldedAddressError("mixed_case");
  const lower = address.replace(/[A-Z]/g, (c) => c.toLowerCase());
  const sep = lower.lastIndexOf("1");
  if (sep < 0) throw new ShieldedAddressError("no_separator");
  if (lower.slice(0, sep) !== SHIELDED_ADDRESS_HRP) throw new ShieldedAddressError("wrong_prefix");
  const data = lower.slice(sep + 1);
  for (const c of data) {
    if (!CHARSET.includes(c)) throw new ShieldedAddressError("bad_character");
  }
  if (data.length !== DATA_LENGTH) throw new ShieldedAddressError("wrong_length");
  let words: number[];
  try {
    const decoded = bech32m.decodeUnsafe(lower, SHIELDED_ADDRESS_LENGTH);
    if (!decoded) throw new ShieldedAddressError("bad_checksum");
    words = decoded.words;
  } catch (err) {
    if (err instanceof ShieldedAddressError) throw err;
    throw new ShieldedAddressError("bad_checksum");
  }
  if (words.length !== DATA_LENGTH - CHECKSUM_LENGTH) throw new ShieldedAddressError("wrong_length");
  try {
    return bech32m.fromWords(words);
  } catch {
    throw new ShieldedAddressError("bad_padding");
  }
}

/** True when `address` is a well-formed shielded address. */
export function isShieldedAddress(address: string): boolean {
  try {
    decodeShieldedAddress(address);
    return true;
  } catch {
    return false;
  }
}
