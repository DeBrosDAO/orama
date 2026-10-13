import { bech32Hrp } from "../../model/bech32";
import { ChainReadError } from "./client";

/**
 * Readers for the JSON the proxy returns. Chain data is written by anyone, so
 * every field is checked before it reaches a page; a malformed record rejects
 * the whole read with a message that names the field, never the value.
 */

export type Rec = Record<string, unknown>;

const MAX_STRING = 4096;
const MAX_DIGITS = 60;
const HEX_64 = /^[0-9a-fA-F]{64}$/;
const UINT = /^(0|[1-9][0-9]*)$/;
const ACCOUNT_HRP = "orama";

function malformed(what: string): ChainReadError {
  return new ChainReadError(`The chain sent a malformed ${what}.`);
}

export function rec(value: unknown, what: string): Rec {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw malformed(what);
  return value as Rec;
}

export function arr(value: unknown, what: string): unknown[] {
  if (!Array.isArray(value)) throw malformed(what);
  return value;
}

/** An array that may be absent: protojson leaves out an empty repeated field. */
export function optArr(value: unknown, what: string): unknown[] {
  return value === undefined || value === null ? [] : arr(value, what);
}

export function str(value: unknown, what: string, max: number = MAX_STRING): string {
  if (typeof value !== "string" || value.length > max) throw malformed(what);
  return value;
}

/** A string that may be absent, as protojson leaves out an empty one. */
export function optStr(value: unknown, what: string, max: number = MAX_STRING): string {
  return value === undefined || value === null ? "" : str(value, what, max);
}

export function bool(value: unknown, what: string): boolean {
  if (typeof value !== "boolean") throw malformed(what);
  return value;
}

/** A non-negative integer as a decimal string, from a JSON string or a safe integer. */
export function digits(value: unknown, what: string): string {
  if (typeof value === "number" && Number.isSafeInteger(value) && value >= 0) return String(value);
  if (typeof value === "string" && value.length <= MAX_DIGITS && UINT.test(value)) return value;
  throw malformed(what);
}

/** A protojson uint64 or an absent one, which is zero. */
export function optDigits(value: unknown, what: string): string {
  return value === undefined || value === null ? "0" : digits(value, what);
}

/** A non-negative safe integer. */
export function uint(value: unknown, what: string): number {
  const n = Number(digits(value, what));
  if (!Number.isSafeInteger(n)) throw malformed(what);
  return n;
}

/** An enum protojson writes by its proto name ("ROLE_STORAGE"), as the lower-case word after `prefix`, which must be one of `allowed`. */
export function enumName<T extends string>(value: unknown, prefix: string, allowed: readonly T[], what: string): T {
  const text = str(value, what, 64);
  const name = text.startsWith(prefix) ? text.slice(prefix.length).toLowerCase() : "";
  const found = allowed.find((a) => a === name);
  if (found === undefined) throw malformed(what);
  return found;
}

const INT = /^-?(0|[1-9][0-9]*)$/;

/** A protojson int64: a safe integer, possibly negative, from a JSON string or number. */
export function int(value: unknown, what: string): number {
  const text = typeof value === "number" ? String(value) : value;
  if (typeof text !== "string" || text.length > MAX_DIGITS || !INT.test(text)) throw malformed(what);
  const n = Number(text);
  if (!Number.isSafeInteger(n)) throw malformed(what);
  return n;
}

export function optUint(value: unknown, what: string): number {
  return value === undefined || value === null ? 0 : uint(value, what);
}

/** An RFC 3339 time as a UTC ISO string. */
export function iso(value: unknown, what: string): string {
  const ms = Date.parse(str(value, what, 64));
  if (Number.isNaN(ms)) throw malformed(what);
  return new Date(ms).toISOString();
}

/** A transaction or block hash: 64 hex characters, returned in upper case. */
export function hash64(value: unknown, what: string): string {
  const text = str(value, what, 64);
  if (!HEX_64.test(text)) throw malformed(what);
  return text.toUpperCase();
}

/** An orama account address with a valid checksum, in lower case. */
export function account(value: unknown, what: string): string {
  const text = str(value, what, 120).toLowerCase();
  if (bech32Hrp(text) !== ACCOUNT_HRP) throw malformed(what);
  return text;
}

const DEC_PLACES = 18;
const DEC_TEXT = /^[0-9]+(\.[0-9]+)?$/;

/**
 * A cosmos-sdk LegacyDec as a number. The node's REST API writes it as a
 * decimal, "0.050000000000000000"; the gateway's module-query route
 * (query/<Service>/<Method>) writes the same value as the unscaled integer,
 * "50000000000000000" (18 places), which is how x/power's lambda and caps
 * arrive. A text with a point is a decimal and one without is unscaled.
 */
export function legacyDec(value: unknown, what: string): number {
  const text = str(value, what, 96);
  if (!DEC_TEXT.test(text)) throw malformed(what);
  let n: number;
  if (text.includes(".")) {
    n = Number(text);
  } else {
    const padded = text.padStart(DEC_PLACES + 1, "0");
    n = Number(`${padded.slice(0, -DEC_PLACES)}.${padded.slice(-DEC_PLACES)}`);
  }
  if (!Number.isFinite(n)) throw malformed(what);
  return n;
}

/** A LegacyDec that is a fraction, 0 to 1 (a share, a rate, a cap). */
export function decShare(value: unknown, what: string): number {
  const n = legacyDec(value, what);
  if (n > 1) throw malformed(what);
  return n;
}

/** The result of a CometBFT JSON-RPC answer. */
export function rpcResult(body: unknown, what: string): Rec {
  return rec(rec(body, what).result, what);
}
