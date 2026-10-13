import { clean } from "../text";

/** Chain-written strings are cut to this before they are cleaned, so a megabyte cannot cost a page. */
export const MAX_RAW = 4096;
const MAX_DIGITS = 40;
const MAX_ENUM = 40;
const UINT = /^(0|[1-9][0-9]*)$/;
const INT = /^-?(0|[1-9][0-9]*)$/;
const BYTE_UNITS = ["KiB", "MiB", "GiB", "TiB", "PiB"] as const;
const BYTES_PER_UNIT = 1024n;
const HUNDRED = 100n;

/** A protojson integer (a string, or a safe integer) as digits, or null when it is neither. */
export function digitsOf(value: unknown, signed = false): string | null {
  const text = typeof value === "number" && Number.isSafeInteger(value) ? String(value) : value;
  if (typeof text !== "string" || text.length > MAX_DIGITS) return null;
  return (signed ? INT : UINT).test(text) ? text : null;
}

/** 1234567 as "1,234,567". */
export function grouped(digits: string): string {
  return BigInt(digits).toLocaleString("en-US");
}

/** "1.5 GiB (1,610,612,736 bytes)", with the fraction truncated like every amount here. */
export function bytesText(digits: string): string {
  const n = BigInt(digits);
  const exact = `${grouped(digits)} ${n === 1n ? "byte" : "bytes"}`;
  if (n < BYTES_PER_UNIT) return exact;
  let unit = 0;
  let scaled = n * HUNDRED;
  while (scaled >= BYTES_PER_UNIT * HUNDRED && unit < BYTE_UNITS.length) {
    scaled /= BYTES_PER_UNIT;
    unit++;
  }
  const frac = (scaled % HUNDRED).toString().padStart(2, "0").replace(/0+$/, "");
  return `${scaled / HUNDRED}${frac ? `.${frac}` : ""} ${BYTE_UNITS[unit - 1]} (${exact})`;
}

/** Basis points as a percentage: 250 is "2.5%". */
export function bpsText(digits: string): string {
  const n = BigInt(digits);
  const frac = (n % HUNDRED).toString().padStart(2, "0").replace(/0+$/, "");
  return `${n / HUNDRED}${frac ? `.${frac}` : ""}%`;
}

/** The bytes of a base64 string as lower-case hex, cut to `maxBytes`; "" when it is not base64. */
export function hexOf(value: unknown, maxBytes: number): string {
  if (typeof value !== "string" || value === "" || value.length > MAX_RAW) return "";
  let bin: string;
  try {
    bin = atob(value);
  } catch {
    return "";
  }
  const hex = Array.from(bin.slice(0, maxBytes), (c) => c.charCodeAt(0).toString(16).padStart(2, "0")).join("");
  return bin.length > maxBytes ? `${hex}…` : hex;
}

/** How many bytes a base64 string holds, from its length alone (a code upload can be megabytes). */
export function base64Size(value: unknown): string {
  if (typeof value !== "string") return "0";
  const pad = value.endsWith("==") ? 2 : value.endsWith("=") ? 1 : 0;
  return String(Math.max(0, Math.floor((value.length * 3) / 4) - pad));
}

/** The words an enum value stands for: a name by its table entry, a number by its position, anything else cleaned. */
export function enumText(value: unknown, prefix: string, names: readonly (readonly [string, string])[]): string {
  if (typeof value === "number") return names[value]?.[1] ?? "";
  if (typeof value !== "string") return "";
  const short = value.startsWith(prefix) ? value.slice(prefix.length) : value;
  const known = names.find(([name]) => name === short);
  return known ? known[1] : clean(short.toLowerCase().replace(/_/g, " "), MAX_ENUM);
}
