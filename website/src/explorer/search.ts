const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];

export type Hit =
  | { kind: "block"; height: number }
  | { kind: "tx"; hash: string }
  | { kind: "account"; address: string }
  | { kind: "validator"; address: string }
  | { kind: "shielded-address" };

function polymod(values: number[]): number {
  let chk = 1;
  for (const v of values) {
    const b = chk >> 25;
    chk = ((chk & 0x1ffffff) << 5) ^ v;
    for (let i = 0; i < 5; i++) {
      if ((b >> i) & 1) chk ^= GEN[i] ?? 0;
    }
  }
  return chk >>> 0;
}

function hrpExpand(hrp: string): number[] {
  const out: number[] = [];
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) >> 5);
  out.push(0);
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) & 31);
  return out;
}

/** Bech32 human-readable part, or null when the checksum or charset fails. */
export function bech32Hrp(addr: string): string | null {
  if (addr.length < 8 || addr.length > 90) return null;
  const lower = addr.toLowerCase();
  if (addr !== lower && addr !== addr.toUpperCase()) return null;
  const pos = lower.lastIndexOf("1");
  if (pos < 1 || pos + 7 > lower.length) return null;
  const hrp = lower.slice(0, pos);
  const data: number[] = [];
  for (const c of lower.slice(pos + 1)) {
    const d = CHARSET.indexOf(c);
    if (d === -1) return null;
    data.push(d);
  }
  if (polymod(hrpExpand(hrp).concat(data)) !== 1) return null;
  return hrp;
}

export function classify(raw: string): Hit | null {
  const q = raw.trim();
  if (!q) return null;
  if (/^[1-9][0-9]{0,15}$/.test(q)) {
    const height = Number(q);
    if (Number.isSafeInteger(height) && height > 0) return { kind: "block", height };
  }
  const bare = q.replace(/^0x/i, "");
  if (/^[0-9a-fA-F]{64}$/.test(bare) && (bare === q || /^0x/i.test(q))) {
    return { kind: "tx", hash: bare.toUpperCase() };
  }
  if (/^[0-9a-fA-F]{40}$/.test(bare) && (bare === q || /^0x/i.test(q))) {
    return { kind: "validator", address: bare.toUpperCase() };
  }
  const hrp = bech32Hrp(q);
  if (hrp === "orama") return { kind: "account", address: q.toLowerCase() };
  if (hrp === "oramavaloper" || hrp === "oramavalcons") {
    return { kind: "validator", address: q.toLowerCase() };
  }
  if (/^u1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{6,}$/i.test(q)) return { kind: "shielded-address" };
  return null;
}
