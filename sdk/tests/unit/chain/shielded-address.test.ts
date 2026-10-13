import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  SHIELDED_ADDRESS_BYTES,
  SHIELDED_ADDRESS_HRP,
  SHIELDED_ADDRESS_LENGTH,
  ShieldedAddressError,
  decodeShieldedAddress,
  encodeShieldedAddress,
  fromHex,
  isShieldedAddress,
  toHex,
} from "../../../src/chain";

// The one vector file the Rust, Go and TypeScript codecs all read.
const VECTORS = resolve(__dirname, "../../../../chain/x/shielded/wallet/testdata/addresses/addresses.json");

interface Pair { raw: string; address: string }
interface Vectors {
  hrp: string;
  raw_length: number;
  encoded_length: number;
  valid: Pair[];
  accept: Pair[];
  invalid: Array<{ why: string; address: string; error: string }>;
}

const vectors: Vectors = JSON.parse(readFileSync(VECTORS, "utf8"));

describe("shielded address (Bech32m, oramas)", () => {
  it("has the constants of the vector file", () => {
    expect(vectors.hrp).toBe(SHIELDED_ADDRESS_HRP);
    expect(vectors.raw_length).toBe(SHIELDED_ADDRESS_BYTES);
    expect(vectors.encoded_length).toBe(SHIELDED_ADDRESS_LENGTH);
  });

  it("encodes and decodes every valid vector", () => {
    expect(vectors.valid.length).toBeGreaterThanOrEqual(8);
    for (const v of vectors.valid) {
      expect(encodeShieldedAddress(fromHex(v.raw))).toBe(v.address);
      expect(toHex(decodeShieldedAddress(v.address))).toBe(v.raw);
      expect(isShieldedAddress(v.address)).toBe(true);
      expect(v.address).toHaveLength(SHIELDED_ADDRESS_LENGTH);
    }
  });

  it("accepts all capitals", () => {
    for (const v of vectors.accept) {
      expect(v.address).not.toBe(v.address.toLowerCase());
      expect(toHex(decodeShieldedAddress(v.address))).toBe(v.raw);
    }
  });

  it("refuses every invalid vector with its code", () => {
    expect(vectors.invalid.length).toBeGreaterThanOrEqual(15);
    for (const v of vectors.invalid) {
      let caught: unknown;
      try {
        decodeShieldedAddress(v.address);
      } catch (err) {
        caught = err;
      }
      expect(caught, v.why).toBeInstanceOf(ShieldedAddressError);
      expect((caught as ShieldedAddressError).code, `${v.why}: ${v.address}`).toBe(v.error);
      expect(isShieldedAddress(v.address)).toBe(false);
    }
  });

  it("refuses a raw address of the wrong length", () => {
    expect(() => encodeShieldedAddress(new Uint8Array(42))).toThrow(RangeError);
    expect(() => encodeShieldedAddress(new Uint8Array(44))).toThrow(RangeError);
  });

  it("never throws anything but a ShieldedAddressError on odd input", () => {
    for (const s of ["", "1", "oramas1", "ORAMAS1", "é1é", "oramas1\u{1F600}", "1".repeat(200), "q".repeat(500)]) {
      expect(() => decodeShieldedAddress(s)).toThrow(ShieldedAddressError);
    }
  });
});
