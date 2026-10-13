import { ChainReadError } from "./client";
import { clean } from "./text";

const BASE64 = /^[A-Za-z0-9+/_-]*={0,2}$/;
const HEX_RADIX = 16;
const HEX_DIGITS = 2;
/** Printable ASCII, tab and newline: the only bytes a key or a message is shown as text for. */
const PRINTABLE = /^[ -~\n\t]*$/;

/** The bytes of a protojson `bytes` field (base64, either alphabet), at most `max` of them. */
export function base64Bytes(value: unknown, what: string, max: number): Uint8Array {
  if (value === undefined || value === null || value === "") return new Uint8Array();
  if (typeof value !== "string" || value.length > Math.ceil((max * 4) / 3) + 4 || !BASE64.test(value)) {
    throw new ChainReadError(`The chain sent a malformed ${what}.`);
  }
  try {
    const binary = atob(value.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(value.length / 4) * 4, "="));
    return Uint8Array.from(binary, (c) => c.charCodeAt(0));
  } catch {
    throw new ChainReadError(`The chain sent a malformed ${what}.`);
  }
}

const MAX_FIELD_CHARS = 4 << 20;
const BASE64_GROUP_CHARS = 4;
const BASE64_GROUP_BYTES = 3;

/** The size of a bytes field and its first `max` bytes, without decoding the rest of a large value. */
export function base64Head(value: unknown, what: string, max: number): { size: number; head: Uint8Array } {
  if (value === undefined || value === null || value === "") return { size: 0, head: new Uint8Array() };
  if (typeof value !== "string" || value.length > MAX_FIELD_CHARS || !BASE64.test(value)) {
    throw new ChainReadError(`The chain sent a malformed ${what}.`);
  }
  const padding = value.endsWith("==") ? 2 : value.endsWith("=") ? 1 : 0;
  const size = Math.floor((value.length * BASE64_GROUP_BYTES) / BASE64_GROUP_CHARS) - padding;
  const prefixChars = Math.ceil(max / BASE64_GROUP_BYTES) * BASE64_GROUP_CHARS;
  return { size, head: base64Bytes(value.slice(0, prefixChars), what, max).subarray(0, max) };
}

export function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(HEX_RADIX).padStart(HEX_DIGITS, "0")).join("");
}

/** The first `max` bytes as cleaned text when they are printable ASCII (a key, a JSON message), with an ellipsis if there are more; null otherwise. */
export function printableText(bytes: Uint8Array, max: number): string | null {
  const text = String.fromCharCode(...bytes.subarray(0, max));
  if (!PRINTABLE.test(text)) return null;
  return bytes.length > max ? `${clean(text, max)}…` : clean(text, max);
}
