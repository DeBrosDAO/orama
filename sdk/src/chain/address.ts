import { bech32 } from "@scure/base";
import { ripemd160 } from "@noble/hashes/ripemd160";
import { sha256 } from "@noble/hashes/sha256";
import { BECH32_PREFIX } from "./format";

const PUBKEY_BYTES = 33;
const ADDRESS_BYTES = 20;

/** The 20-byte account address bytes of a compressed secp256k1 public key: RIPEMD160(SHA256(pub)). */
export function addressBytes(publicKey: Uint8Array): Uint8Array {
  if (publicKey.length !== PUBKEY_BYTES) {
    throw new Error(`a compressed secp256k1 public key is ${PUBKEY_BYTES} bytes, got ${publicKey.length}`);
  }
  return ripemd160(sha256(publicKey));
}

/** The orama bech32 account address of a compressed secp256k1 public key. */
export function addressFromPublicKey(publicKey: Uint8Array, prefix: string = BECH32_PREFIX): string {
  return bech32.encode(prefix, bech32.toWords(addressBytes(publicKey)));
}

/** The address bytes of an orama account address. Throws on a bad checksum or prefix. */
export function addressToBytes(address: string, prefix: string = BECH32_PREFIX): Uint8Array {
  const decoded = bech32.decode(address as `${string}1${string}`);
  if (decoded.prefix !== prefix) {
    throw new Error(`address prefix is ${decoded.prefix}, want ${prefix}`);
  }
  const bytes = bech32.fromWords(decoded.words);
  if (bytes.length !== ADDRESS_BYTES) {
    throw new Error(`address holds ${bytes.length} bytes, want ${ADDRESS_BYTES}`);
  }
  return bytes;
}

/** True when address is a well-formed orama account address. */
export function isOramaAddress(address: string): boolean {
  try {
    addressToBytes(address);
    return true;
  } catch {
    return false;
  }
}
