import { bech32Hrp } from "./bech32";

export type Hit =
  | { kind: "block"; height: number }
  | { kind: "tx"; hash: string }
  | { kind: "wallet"; address: string }
  | { kind: "shielded-address" };

/** Only account addresses are wallets; a validator operator address is not a wallet page. */
const WALLET_HRP = "orama";
const SHIELDED = /^u1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{6,}$/i;

/**
 * Decide what a pasted string is, without asking any server. Returns null for
 * anything that is not an exact identifier; the search box then falls back to
 * name matching. A wallet address must pass its bech32 checksum, so a typo is
 * "not found" instead of a page for an address nobody owns.
 */
export function classify(raw: string): Hit | null {
  const q = raw.trim();
  if (!q) return null;
  if (/^[1-9][0-9]{0,15}$/.test(q)) {
    const height = Number(q);
    if (Number.isSafeInteger(height)) return { kind: "block", height };
  }
  const bare = q.replace(/^0x/i, "");
  if (/^[0-9a-fA-F]{64}$/.test(bare) && (bare === q || /^0x/i.test(q))) {
    return { kind: "tx", hash: bare.toUpperCase() };
  }
  const hrp = bech32Hrp(q);
  if (hrp === WALLET_HRP) return { kind: "wallet", address: q.toLowerCase() };
  if (SHIELDED.test(q)) return { kind: "shielded-address" };
  return null;
}
