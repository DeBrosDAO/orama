import { describe, expect, it } from "vitest";
import QRCode from "qrcode";
import { WALLETS } from "./wallets";

const BASE58 = /^[1-9A-HJ-NP-Za-km-z]+$/;
const BASE58_ALPHABET = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";

/** Decoded byte length of a base58 string (Bitcoin alphabet). */
function base58Bytes(s: string): number {
  let n = 0n;
  for (const c of s) n = n * 58n + BigInt(BASE58_ALPHABET.indexOf(c));
  let len = 0;
  while (n > 0n) {
    n >>= 8n;
    len++;
  }
  for (const c of s) {
    if (c !== "1") break;
    len++;
  }
  return len;
}

/** BIP-173 bech32 / BIP-350 bech32m checksum: catches any single-character typo. */
function bech32Valid(addr: string): boolean {
  const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
  const sep = addr.lastIndexOf("1");
  const hrp = addr.slice(0, sep);
  const data = [...addr.slice(sep + 1)].map((c) => CHARSET.indexOf(c));
  if (data.some((d) => d < 0)) return false;
  const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
  let chk = 1;
  const values = [...[...hrp].map((c) => c.charCodeAt(0) >> 5), 0, ...[...hrp].map((c) => c.charCodeAt(0) & 31), ...data];
  for (const v of values) {
    const top = chk >>> 25;
    chk = ((chk & 0x1ffffff) << 5) ^ v;
    for (let i = 0; i < 5; i++) if ((top >>> i) & 1) chk ^= GEN[i];
  }
  // bech32 (segwit v0) or bech32m (v1+).
  return chk === 1 || chk === 0x2bc830a3;
}

const FORMAT: Record<string, (a: string) => boolean> = {
  // Native SegWit (bech32): bc1 + lowercase data part, no 1/b/i/o.
  btc: (a) => /^bc1[ac-hj-np-z02-9]{39,59}$/.test(a) && bech32Valid(a),
  // Monero standard address: 95 base58 characters starting with 4.
  xmr: (a) => a.length === 95 && a.startsWith("4") && BASE58.test(a),
  // EVM: 0x + 40 hex characters.
  eth: (a) => /^0x[0-9a-fA-F]{40}$/.test(a),
  // Solana: base58 of a 32-byte ed25519 public key.
  sol: (a) => BASE58.test(a) && base58Bytes(a) === 32,
};

describe("donation wallets", () => {
  it("TestWallets_one_per_chain", () => {
    expect(WALLETS.map((w) => w.id).sort()).toEqual(["btc", "eth", "sol", "xmr"]);
  });

  it.each(WALLETS)("TestWallet_address_format_$id", (w) => {
    expect(FORMAT[w.id](w.address)).toBe(true);
  });

  it("TestWallets_no_whitespace_or_duplicates", () => {
    for (const w of WALLETS) expect(w.address).toBe(w.address.trim());
    expect(new Set(WALLETS.map((w) => w.address)).size).toBe(WALLETS.length);
  });

  it.each(WALLETS)("TestWallet_qr_encodes_$id", (w) => {
    // The longest (Monero, 95 chars) must still fit a QR code at level M.
    const qr = QRCode.create(w.address, { errorCorrectionLevel: "M" });
    expect(qr.modules.size).toBeGreaterThan(20);
  });

  it("TestWalletFormat_rejects_typos", () => {
    expect(FORMAT.eth("0x61482a75c5c0bE7667096264f637aB3272d6f4d")).toBe(false);
    expect(FORMAT.btc("bc1qufd0cewe54awrekqd6guryum9q6jsnyjckucxO")).toBe(false);
    // One valid-looking character changed: only the checksum catches it.
    expect(FORMAT.btc("bc1qufd0cewe54awrekqd6guryum9q6jsnyjckucxf")).toBe(false);
    expect(FORMAT.sol("9JEsuEeRpxrJyFVkx1RRXBTG7V6zTeApr33nGrBpcYt0")).toBe(false);
    expect(FORMAT.xmr("")).toBe(false);
  });
});
