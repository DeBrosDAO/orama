/** 1 ORAMA is 10^9 norama. Amounts travel as integer strings; see types.ts. */
export const NORAMA_PER_ORAMA = 1_000_000_000n;
export const ORAMA_DECIMALS = 9;

const INT = /^-?(0|[1-9][0-9]*)$/;
/** The Unicode minus sign, the one negative marker every formatter here uses. */
export const MINUS = "\u2212";
/** No real supply needs more digits; a longer string is malformed input, not a number. */
export const MAX_NORAMA_DIGITS = 60;
const COMPACT_TENTHS = 10n;
const THOUSAND = 1_000n;
const MILLION = 1_000_000n;
const BILLION = 1_000_000_000n;
/** Below this many ORAMA the whole number is shown; from here on it is "10k" and up. */
const COMPACT_FROM_K = 10_000n;

function groupDigits(n: bigint): string {
  return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** Parse a base-unit integer string; throws on anything that is not one. */
export function parseNorama(amount: string): bigint {
  if (!INT.test(amount)) throw new Error(`amount is not a norama integer: ${JSON.stringify(amount)}`);
  if (amount.replace("-", "").length > MAX_NORAMA_DIGITS) {
    throw new Error(`amount has more than ${MAX_NORAMA_DIGITS} digits`);
  }
  return BigInt(amount);
}

/**
 * ORAMA with digit grouping and a fraction trimmed of trailing zeros. The
 * fraction is truncated, never rounded: an explorer must not show more than
 * the chain holds. Negative input gets the minus sign.
 */
export function formatNorama(amount: string, maxFraction: number = ORAMA_DECIMALS): string {
  const v = parseNorama(amount);
  const neg = v < 0n;
  const abs = neg ? -v : v;
  const whole = abs / NORAMA_PER_ORAMA;
  const frac = (abs % NORAMA_PER_ORAMA).toString().padStart(ORAMA_DECIMALS, "0");
  const tail = frac.slice(0, Math.max(0, maxFraction)).replace(/0+$/, "");
  const head = groupDigits(whole);
  return `${neg ? MINUS : ""}${head}${tail ? `.${tail}` : ""}`;
}

/** Fixed decimals (truncated), for table columns that must line up. */
export function formatNoramaFixed(amount: string, fraction: number): string {
  const v = parseNorama(amount);
  const neg = v < 0n;
  const abs = neg ? -v : v;
  const whole = groupDigits(abs / NORAMA_PER_ORAMA);
  if (fraction <= 0) return `${neg ? MINUS : ""}${whole}`;
  const frac = (abs % NORAMA_PER_ORAMA).toString().padStart(ORAMA_DECIMALS, "0").slice(0, fraction);
  return `${neg ? MINUS : ""}${whole}.${frac.padEnd(fraction, "0")}`;
}

export interface SignedAmount {
  sign: "+" | typeof MINUS | "";
  /** The magnitude, formatted like formatNorama. */
  text: string;
}

export function formatSigned(delta: string, maxFraction: number = ORAMA_DECIMALS): SignedAmount {
  const v = parseNorama(delta);
  if (v === 0n) return { sign: "", text: "0" };
  const text = formatNorama((v < 0n ? -v : v).toString(), maxFraction);
  return { sign: v < 0n ? MINUS : "+", text };
}

/**
 * 41.2M, 84k, 312: for headline tiles where precision is noise. Truncated to
 * one decimal like every amount here, so 999,960 ORAMA is "999.9k", never "1000k".
 */
export function formatCompact(amount: string): string {
  const orama = parseNorama(amount) / NORAMA_PER_ORAMA;
  const abs = orama < 0n ? -orama : orama;
  const sign = orama < 0n ? MINUS : "";
  if (abs >= BILLION) return `${sign}${tenths(abs, BILLION)}B`;
  if (abs >= MILLION) return `${sign}${tenths(abs, MILLION)}M`;
  if (abs >= COMPACT_FROM_K) return `${sign}${tenths(abs, THOUSAND)}k`;
  return `${sign}${groupDigits(abs)}`;
}

function tenths(abs: bigint, unit: bigint): string {
  const t = (abs * COMPACT_TENTHS) / unit;
  const fraction = t % COMPACT_TENTHS;
  return `${t / COMPACT_TENTHS}${fraction === 0n ? "" : `.${fraction}`}`;
}

export function formatInt(n: number): string {
  return groupDigits(BigInt(Math.trunc(n)));
}

/** "18.2%" from a 0-1 share, one decimal place. */
export function formatPct(share: number): string {
  return `${(share * 100).toFixed(1)}%`;
}

/** orama1q8w9k3…7x2 style: keeps the prefix and the last characters. */
export function shortAddress(address: string): string {
  if (address.length <= 18) return address;
  return `${address.slice(0, 10)}…${address.slice(-4)}`;
}

export function shortHash(hash: string): string {
  if (hash.length <= 14) return hash;
  return `${hash.slice(0, 6)}…${hash.slice(-4)}`;
}
