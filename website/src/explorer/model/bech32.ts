const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
const CHECKSUM_LEN = 6;
const MAX_LEN = 90;

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
  if (addr.length < 8 || addr.length > MAX_LEN) return null;
  const lower = addr.toLowerCase();
  if (addr !== lower && addr !== addr.toUpperCase()) return null;
  const pos = lower.lastIndexOf("1");
  if (pos < 1 || pos + 1 + CHECKSUM_LEN > lower.length) return null;
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

function toFiveBitGroups(bytes: Uint8Array): number[] {
  const out: number[] = [];
  let acc = 0;
  let bits = 0;
  for (const b of bytes) {
    acc = (acc << 8) | b;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      out.push((acc >> bits) & 31);
    }
  }
  if (bits > 0) out.push((acc << (5 - bits)) & 31);
  return out;
}

/** Encode bytes as a bech32 string. Used by the demo world to mint valid addresses. */
export function bech32Encode(hrp: string, bytes: Uint8Array): string {
  const data = toFiveBitGroups(bytes);
  const mod = polymod([...hrpExpand(hrp), ...data, 0, 0, 0, 0, 0, 0]) ^ 1;
  const checksum: number[] = [];
  for (let i = 0; i < CHECKSUM_LEN; i++) checksum.push((mod >> (5 * (CHECKSUM_LEN - 1 - i))) & 31);
  return `${hrp}1${[...data, ...checksum].map((d) => CHARSET[d]).join("")}`;
}
