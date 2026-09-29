import { secp256k1 } from "@noble/curves/secp256k1";
import { sha256 } from "@noble/hashes/sha256";
import { addressFromPublicKey } from "./address";
import { fromHex } from "./format";

/**
 * Whatever holds the account key: RootWallet, a hardware wallet, or a local
 * key. The builder hands it the SignDoc, never a key, so the signer can decode
 * the document and show the user what they are approving before it signs.
 */
export interface OramaSigner {
  /** The orama bech32 account address the signature is for. */
  readonly address: string;
  /** The 33-byte compressed secp256k1 public key. */
  readonly publicKey: Uint8Array;
  /**
   * Signs a protobuf-encoded cosmos.tx.v1beta1.SignDoc with SIGN_MODE_DIRECT:
   * ECDSA over SHA-256 of signDoc, returned as the 64-byte r||s with a low s.
   */
  signDirect(signDoc: Uint8Array): Promise<Uint8Array>;
}

const PRIVATE_KEY_BYTES = 32;

/**
 * A signer over a private key held in memory. For tests, scripts and servers
 * that own their key; a user's own funds belong in RootWallet, which shows
 * the decoded transaction and asks per signature.
 */
export class LocalSigner implements OramaSigner {
  readonly address: string;
  readonly publicKey: Uint8Array;
  readonly #privateKey: Uint8Array;

  constructor(privateKey: Uint8Array | string) {
    const key = typeof privateKey === "string" ? fromHex(privateKey) : Uint8Array.from(privateKey);
    if (key.length !== PRIVATE_KEY_BYTES || !secp256k1.utils.isValidPrivateKey(key)) {
      throw new Error("not a valid secp256k1 private key: it must be 32 bytes in [1, n-1]");
    }
    this.#privateKey = key;
    this.publicKey = secp256k1.getPublicKey(key, true);
    this.address = addressFromPublicKey(this.publicKey);
  }

  async signDirect(signDoc: Uint8Array): Promise<Uint8Array> {
    const digest = sha256(signDoc);
    return secp256k1.sign(digest, this.#privateKey, { lowS: true }).toCompactRawBytes();
  }
}

/** Verifies a SIGN_MODE_DIRECT signature over signDoc for a compressed public key. */
export function verifyDirectSignature(publicKey: Uint8Array, signDoc: Uint8Array, signature: Uint8Array): boolean {
  if (signature.length !== 64) return false;
  try {
    return secp256k1.verify(signature, sha256(signDoc), publicKey, { lowS: true });
  } catch {
    return false;
  }
}
