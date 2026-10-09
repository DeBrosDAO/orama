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

/** A decimal fraction such as "0.250000000000000000", as a number from 0 to 1. */
export function share(value: unknown, what: string): number {
  const text = str(value, what, 64);
  if (!/^[0-9]+(\.[0-9]+)?$/.test(text)) throw malformed(what);
  const n = Number(text);
  if (!Number.isFinite(n) || n < 0 || n > 1) throw malformed(what);
  return n;
}

/** The result of a CometBFT JSON-RPC answer. */
export function rpcResult(body: unknown, what: string): Rec {
  return rec(rec(body, what).result, what);
}
