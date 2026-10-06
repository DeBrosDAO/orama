import { bech32Encode } from "../../model/bech32";
import { hashString } from "../../model/hash";
import { Rng } from "./rng";

const ADDRESS_BYTES = 20;
const HASH_BYTES = 32;

function bytesFor(label: string, length: number): Uint8Array {
  const rng = new Rng(hashString(label));
  const out = new Uint8Array(length);
  for (let i = 0; i < length; i++) out[i] = Math.floor(rng.next() * 256);
  return out;
}

/** A valid bech32 "orama1…" address, the same for the same label. */
export function addressFor(label: string): string {
  return bech32Encode("orama", bytesFor(`addr:${label}`, ADDRESS_BYTES));
}

/** 64 upper-case hex characters, the same for the same label. */
export function hashFor(label: string): string {
  return Array.from(bytesFor(`hash:${label}`, HASH_BYTES), (b) => b.toString(16).padStart(2, "0"))
    .join("")
    .toUpperCase();
}
