import { describe, it, expect } from 'vitest';
import {
  encrypt,
  decrypt,
  encryptString,
  decryptString,
  generateKey,
  clearKey,
  serialize,
  deserialize,
  toHex,
  fromHex,
  toBase64,
  fromBase64,
  isValidEncryptedData,
  KEY_SIZE,
  NONCE_SIZE,
} from '../../../src/vault/crypto/aes';

describe('AES-256-GCM', () => {
  it('encrypt/decrypt round-trip', () => {
    const key = generateKey();
    const plaintext = new TextEncoder().encode('hello vault');
    const encrypted = encrypt(plaintext, key);
    const decrypted = decrypt(encrypted, key);
    expect(decrypted).toEqual(plaintext);
    clearKey(key);
  });

  it('encryptString/decryptString round-trip', () => {
    const key = generateKey();
    const msg = 'sensitive data 123';
    const encrypted = encryptString(msg, key);
    const decrypted = decryptString(encrypted, key);
    expect(decrypted).toBe(msg);
    clearKey(key);
  });

  it('different keys cannot decrypt', () => {
    const key1 = generateKey();
    const key2 = generateKey();
    const encrypted = encrypt(new Uint8Array([1, 2, 3]), key1);
    expect(() => decrypt(encrypted, key2)).toThrow();
    clearKey(key1);
    clearKey(key2);
  });

  it('nonce is correct size', () => {
    const key = generateKey();
    const encrypted = encrypt(new Uint8Array([42]), key);
    expect(encrypted.nonce.length).toBe(NONCE_SIZE);
    clearKey(key);
  });

  it('rejects invalid key size', () => {
    expect(() => encrypt(new Uint8Array([1]), new Uint8Array(16))).toThrow('Invalid key length');
  });

  it('serialize/deserialize round-trip', () => {
    const key = generateKey();
    const encrypted = encrypt(new Uint8Array([1, 2, 3]), key);
    const serialized = serialize(encrypted);
    const deserialized = deserialize(serialized);
    expect(decrypt(deserialized, key)).toEqual(new Uint8Array([1, 2, 3]));
    clearKey(key);
  });

  it('toHex/fromHex round-trip', () => {
    const key = generateKey();
    const encrypted = encrypt(new Uint8Array([10, 20, 30]), key);
    const hex = toHex(encrypted);
    const restored = fromHex(hex);
    expect(decrypt(restored, key)).toEqual(new Uint8Array([10, 20, 30]));
    clearKey(key);
  });

  it('toBase64/fromBase64 round-trip', () => {
    const key = generateKey();
    const encrypted = encrypt(new Uint8Array([99]), key);
    const b64 = toBase64(encrypted);
    const restored = fromBase64(b64);
    expect(decrypt(restored, key)).toEqual(new Uint8Array([99]));
    clearKey(key);
  });

  it('isValidEncryptedData checks structure', () => {
    const key = generateKey();
    const encrypted = encrypt(new Uint8Array([1]), key);
    expect(isValidEncryptedData(encrypted)).toBe(true);
    expect(isValidEncryptedData({ ciphertext: new Uint8Array(0), nonce: new Uint8Array(0) })).toBe(false);
    clearKey(key);
  });

  it('generateKey produces KEY_SIZE bytes', () => {
    const key = generateKey();
    expect(key.length).toBe(KEY_SIZE);
    clearKey(key);
  });
});
