/**
 * AES-256-GCM Encryption
 *
 * Implements authenticated encryption using AES-256 in Galois/Counter Mode.
 * Uses @noble/ciphers for platform-agnostic, audited cryptographic operations.
 *
 * Features:
 * - Authenticated encryption (confidentiality + integrity)
 * - 256-bit keys for strong security
 * - 96-bit nonces (randomly generated)
 * - 128-bit authentication tags
 *
 * Security considerations:
 * - Never reuse a nonce with the same key
 * - Nonces are randomly generated and prepended to ciphertext
 * - Authentication tags are verified before decryption
 */

import { gcm } from '@noble/ciphers/aes';
import { randomBytes } from '@noble/ciphers/webcrypto';
import { bytesToHex, hexToBytes, concatBytes } from '@noble/hashes/utils';

/**
 * Size constants
 */
export const KEY_SIZE = 32; // 256 bits
export const NONCE_SIZE = 12; // 96 bits (recommended for GCM)
export const TAG_SIZE = 16; // 128 bits

/**
 * Encrypted data structure
 */
export interface EncryptedData {
  /** Ciphertext including authentication tag */
  ciphertext: Uint8Array;
  /** Nonce used for encryption */
  nonce: Uint8Array;
  /** Additional authenticated data (optional) */
  aad?: Uint8Array;
}

/**
 * Serialized encrypted data (nonce prepended to ciphertext)
 */
export interface SerializedEncryptedData {
  /** Combined nonce + ciphertext + tag */
  data: Uint8Array;
  /** Additional authenticated data (optional) */
  aad?: Uint8Array;
}

/**
 * Encrypts data using AES-256-GCM
 */
export function encrypt(
  plaintext: Uint8Array,
  key: Uint8Array,
  aad?: Uint8Array
): EncryptedData {
  validateKey(key);

  const nonce = randomBytes(NONCE_SIZE);
  const cipher = gcm(key, nonce, aad);
  const ciphertext = cipher.encrypt(plaintext);

  return {
    ciphertext,
    nonce,
    aad,
  };
}

/**
 * Decrypts data using AES-256-GCM
 */
export function decrypt(encryptedData: EncryptedData, key: Uint8Array): Uint8Array {
  validateKey(key);
  validateNonce(encryptedData.nonce);

  const cipher = gcm(key, encryptedData.nonce, encryptedData.aad);

  try {
    return cipher.decrypt(encryptedData.ciphertext);
  } catch (error) {
    throw new Error('Decryption failed: invalid ciphertext or authentication tag');
  }
}

/**
 * Encrypts a string message
 */
export function encryptString(
  message: string,
  key: Uint8Array,
  aad?: Uint8Array
): EncryptedData {
  const plaintext = new TextEncoder().encode(message);
  try {
    return encrypt(plaintext, key, aad);
  } finally {
    plaintext.fill(0);
  }
}

/**
 * Decrypts to a string message
 */
export function decryptString(encryptedData: EncryptedData, key: Uint8Array): string {
  const plaintext = decrypt(encryptedData, key);
  try {
    return new TextDecoder().decode(plaintext);
  } finally {
    plaintext.fill(0);
  }
}

/**
 * Serializes encrypted data (prepends nonce to ciphertext)
 */
export function serialize(encryptedData: EncryptedData): SerializedEncryptedData {
  const data = concatBytes(encryptedData.nonce, encryptedData.ciphertext);

  return {
    data,
    aad: encryptedData.aad,
  };
}

/**
 * Deserializes encrypted data
 */
export function deserialize(serialized: SerializedEncryptedData): EncryptedData {
  if (serialized.data.length < NONCE_SIZE + TAG_SIZE) {
    throw new Error('Invalid serialized data: too short');
  }

  const nonce = serialized.data.slice(0, NONCE_SIZE);
  const ciphertext = serialized.data.slice(NONCE_SIZE);

  return {
    ciphertext,
    nonce,
    aad: serialized.aad,
  };
}

/**
 * Encrypts and serializes data in one step
 */
export function encryptAndSerialize(
  plaintext: Uint8Array,
  key: Uint8Array,
  aad?: Uint8Array
): SerializedEncryptedData {
  const encrypted = encrypt(plaintext, key, aad);
  return serialize(encrypted);
}

/**
 * Deserializes and decrypts data in one step
 */
export function deserializeAndDecrypt(
  serialized: SerializedEncryptedData,
  key: Uint8Array
): Uint8Array {
  const encrypted = deserialize(serialized);
  return decrypt(encrypted, key);
}

/**
 * Converts encrypted data to hex string
 */
export function toHex(encryptedData: EncryptedData): string {
  const serialized = serialize(encryptedData);
  return bytesToHex(serialized.data);
}

/**
 * Parses encrypted data from hex string
 */
export function fromHex(hex: string, aad?: Uint8Array): EncryptedData {
  const normalized = hex.startsWith('0x') ? hex.slice(2) : hex;
  const data = hexToBytes(normalized);

  return deserialize({ data, aad });
}

/**
 * Converts encrypted data to base64 string
 */
export function toBase64(encryptedData: EncryptedData): string {
  const serialized = serialize(encryptedData);

  if (typeof btoa === 'function') {
    return btoa(String.fromCharCode(...serialized.data));
  } else {
    return Buffer.from(serialized.data).toString('base64');
  }
}

/**
 * Parses encrypted data from base64 string
 */
export function fromBase64(base64: string, aad?: Uint8Array): EncryptedData {
  let data: Uint8Array;

  if (typeof atob === 'function') {
    const binary = atob(base64);
    data = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      data[i] = binary.charCodeAt(i);
    }
  } else {
    data = new Uint8Array(Buffer.from(base64, 'base64'));
  }

  return deserialize({ data, aad });
}

function validateKey(key: Uint8Array): void {
  if (!(key instanceof Uint8Array)) {
    throw new Error('Key must be a Uint8Array');
  }

  if (key.length !== KEY_SIZE) {
    throw new Error(`Invalid key length: expected ${KEY_SIZE}, got ${key.length}`);
  }
}

function validateNonce(nonce: Uint8Array): void {
  if (!(nonce instanceof Uint8Array)) {
    throw new Error('Nonce must be a Uint8Array');
  }

  if (nonce.length !== NONCE_SIZE) {
    throw new Error(`Invalid nonce length: expected ${NONCE_SIZE}, got ${nonce.length}`);
  }
}

/**
 * Generates a random encryption key
 */
export function generateKey(): Uint8Array {
  return randomBytes(KEY_SIZE);
}

/**
 * Generates a random nonce
 */
export function generateNonce(): Uint8Array {
  return randomBytes(NONCE_SIZE);
}

/**
 * Securely clears a key from memory
 */
export function clearKey(key: Uint8Array): void {
  key.fill(0);
}

/**
 * Checks if encrypted data appears valid (basic structure check)
 */
export function isValidEncryptedData(data: EncryptedData): boolean {
  return (
    data.nonce instanceof Uint8Array &&
    data.nonce.length === NONCE_SIZE &&
    data.ciphertext instanceof Uint8Array &&
    data.ciphertext.length >= TAG_SIZE
  );
}
