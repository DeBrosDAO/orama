const NORAMA_PER_ORAMA = 1_000_000_000n;

function groupDigits(n: bigint): string {
  return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** Whole ORAMA plus a trimmed fractional part. The input is a base-unit integer. */
export function formatNorama(amount: string): string {
  if (!/^(0|[1-9][0-9]*)$/.test(amount)) throw new Error("amount is not a norama integer");
  const v = BigInt(amount);
  const whole = v / NORAMA_PER_ORAMA;
  const frac = v % NORAMA_PER_ORAMA;
  const head = groupDigits(whole);
  if (frac === 0n) return head;
  const tail = frac.toString().padStart(9, "0").replace(/0+$/, "");
  return `${head}.${tail}`;
}

export function formatBig(n: bigint): string {
  const neg = n < 0n;
  return (neg ? "-" : "") + groupDigits(neg ? -n : n);
}

export function short(addr: string): string {
  if (addr.length <= 18) return addr;
  return `${addr.slice(0, 10)}…${addr.slice(-4)}`;
}

export function sharePct(part: bigint, whole: bigint): string {
  if (whole <= 0n || part <= 0n) return "0.0%";
  let scaled = (part * 1000n) / whole;
  if (scaled > 1000n) scaled = 1000n;
  return `${scaled / 10n}.${scaled % 10n}%`;
}

export function shareWidth(part: bigint, whole: bigint): string {
  if (whole <= 0n || part <= 0n) return "0%";
  let bps = (part * 10000n) / whole;
  if (bps > 10000n) bps = 10000n;
  return `${Number(bps) / 100}%`;
}

/**
 * CometBFT 0.39 JSON-encodes event attribute strings as themselves. Some
 * responses still base64-encode those bytes. Decode only when the result is
 * printable text; a hex address or a plain word stays as it arrived.
 */
export function showAttr(value: string): string {
  if (value.length < 4 || value.length % 4 !== 0) return value;
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(value)) return value;
  let bin: string;
  try {
    bin = atob(value);
  } catch {
    return value;
  }
  if (bin.length === 0) return value;
  for (let i = 0; i < bin.length; i++) {
    const c = bin.charCodeAt(i);
    if (c < 32 || c > 126) return value;
  }
  return bin;
}

export function decodeBase64(value: string): Uint8Array {
  if (value.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(value)) {
    throw new Error("transaction bytes are not base64");
  }
  const bin = atob(value);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** CometBFT's tx hash is the uppercase hex SHA-256 of the raw transaction bytes. */
export async function txHashFromBase64(value: string): Promise<string> {
  const bytes = decodeBase64(value);
  const copy = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(copy).set(bytes);
  const digest = await crypto.subtle.digest("SHA-256", copy);
  const view = new Uint8Array(digest);
  let hex = "";
  for (const b of view) hex += b.toString(16).padStart(2, "0");
  return hex.toUpperCase();
}
