/** The chain's base denom, smallest unit. */
export const BASE_DENOM = "norama";
/** The display unit: 1 ORAMA is 10^9 norama. */
export const DISPLAY_DENOM = "ORAMA";
export const DENOM_DECIMALS = 9;
/** Bech32 human-readable prefix of an account address. */
export const BECH32_PREFIX = "orama";

const ORAMA_DIVISOR = 10n ** BigInt(DENOM_DECIMALS);

/**
 * Formats an amount of a denom. norama shows as ORAMA with the exact norama in
 * brackets, so an approval screen never rounds what is being signed.
 */
export function formatAmount(amount: string | bigint, denom: string = BASE_DENOM): string {
  const raw = typeof amount === "bigint" ? amount.toString() : amount;
  if (denom !== BASE_DENOM || !/^\d+$/.test(raw)) return `${raw} ${denom}`;
  const value = BigInt(raw);
  const whole = value / ORAMA_DIVISOR;
  const frac = (value % ORAMA_DIVISOR).toString().padStart(DENOM_DECIMALS, "0").replace(/0+$/, "");
  return `${whole}${frac ? "." + frac : ""} ${DISPLAY_DENOM} (${raw} ${BASE_DENOM})`;
}

/** Formats a list of coins, or "nothing". */
export function formatCoins(coins: ReadonlyArray<{ denom: string; amount: string }> | undefined): string {
  if (!coins || coins.length === 0) return "nothing";
  return coins.map((c) => formatAmount(c.amount, c.denom)).join(", ");
}

/** Basis points as a percentage: 250 is "2.5%". */
export function formatBps(bps: number): string {
  const whole = Math.floor(bps / 100);
  const frac = (bps % 100).toString().padStart(2, "0").replace(/0+$/, "");
  return `${whole}${frac ? "." + frac : ""}%`;
}

/** Lower-case hex. */
export function toHex(bytes: Uint8Array): string {
  let out = "";
  for (const b of bytes) out += b.toString(16).padStart(2, "0");
  return out;
}

/** Bytes from hex, with or without 0x. Throws on anything else. */
export function fromHex(hex: string): Uint8Array {
  const clean = hex.startsWith("0x") ? hex.slice(2) : hex;
  if (clean.length % 2 !== 0 || !/^[0-9a-fA-F]*$/.test(clean)) {
    throw new Error("not a hex string");
  }
  const out = new Uint8Array(clean.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(clean.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/** A short hex digest of bytes for a summary line: the first 8 bytes. */
export function shortHex(bytes: Uint8Array): string {
  return bytes.length <= 8 ? toHex(bytes) : `${toHex(bytes.slice(0, 8))}...`;
}

/**
 * Text a chain, node or gateway sent, without the control and invisible format characters that act
 * on a terminal or hide text in it. Run it on anything untrusted before it reaches an error message
 * or a prompt.
 */
export function printable(text: string): string {
  return text.replace(/[\p{Cc}\p{Cf}]/gu, "");
}
