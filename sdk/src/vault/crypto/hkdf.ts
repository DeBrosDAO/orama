/**
 * HKDF Key Derivation
 *
 * Derives deterministic sub-keys from a master secret using HKDF-SHA256 (RFC 5869).
 */

import { hkdf } from '@noble/hashes/hkdf';
import { sha256 } from '@noble/hashes/sha256';

/** Default output length in bytes (256 bits) */
const DEFAULT_KEY_LENGTH = 32;

/** Maximum allowed output length (255 * SHA-256 output = 8160 bytes) */
const MAX_KEY_LENGTH = 255 * 32;

/**
 * Derives a sub-key from input key material using HKDF-SHA256.
 *
 * @param ikm - Input key material (e.g., wallet private key). MUST be high-entropy.
 * @param salt - Domain separation salt. Can be a string or bytes.
 * @param info - Context-specific info. Can be a string or bytes.
 * @param length - Output key length in bytes (default: 32).
 * @returns Derived key as Uint8Array. Caller MUST zero this after use.
 */
export function deriveKeyHKDF(
  ikm: Uint8Array,
  salt: string | Uint8Array,
  info: string | Uint8Array,
  length: number = DEFAULT_KEY_LENGTH,
): Uint8Array {
  if (!ikm || ikm.length === 0) {
    throw new Error('HKDF: input key material must not be empty');
  }
  if (length <= 0 || length > MAX_KEY_LENGTH) {
    throw new Error(`HKDF: output length must be between 1 and ${MAX_KEY_LENGTH}`);
  }

  const saltBytes = typeof salt === 'string' ? new TextEncoder().encode(salt) : salt;
  const infoBytes = typeof info === 'string' ? new TextEncoder().encode(info) : info;

  return hkdf(sha256, ikm, saltBytes, infoBytes, length);
}
