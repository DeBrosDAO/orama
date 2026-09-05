import { describe, it, expect } from 'vitest';
import { deriveKeyHKDF } from '../../../src/crypto/hkdf';

describe('HKDF Derivation', () => {
  it('derives 32-byte key by default', () => {
    const ikm = new Uint8Array(32).fill(0xab);
    const key = deriveKeyHKDF(ikm, 'test-salt', 'test-info');
    expect(key.length).toBe(32);
  });

  it('same inputs produce same output', () => {
    const ikm = new Uint8Array(32).fill(0x42);
    const key1 = deriveKeyHKDF(ikm, 'salt', 'info');
    const key2 = deriveKeyHKDF(ikm, 'salt', 'info');
    expect(key1).toEqual(key2);
  });

  it('different salts produce different keys', () => {
    const ikm = new Uint8Array(32).fill(0x42);
    const key1 = deriveKeyHKDF(ikm, 'salt-a', 'info');
    const key2 = deriveKeyHKDF(ikm, 'salt-b', 'info');
    expect(key1).not.toEqual(key2);
  });

  it('different info produce different keys', () => {
    const ikm = new Uint8Array(32).fill(0x42);
    const key1 = deriveKeyHKDF(ikm, 'salt', 'info-a');
    const key2 = deriveKeyHKDF(ikm, 'salt', 'info-b');
    expect(key1).not.toEqual(key2);
  });

  it('custom length', () => {
    const ikm = new Uint8Array(32).fill(0x42);
    const key = deriveKeyHKDF(ikm, 'salt', 'info', 64);
    expect(key.length).toBe(64);
  });

  it('throws on empty ikm', () => {
    expect(() => deriveKeyHKDF(new Uint8Array(0), 'salt', 'info')).toThrow('must not be empty');
  });

  it('accepts Uint8Array salt and info', () => {
    const ikm = new Uint8Array(32).fill(0xab);
    const salt = new Uint8Array([1, 2, 3]);
    const info = new Uint8Array([4, 5, 6]);
    const key = deriveKeyHKDF(ikm, salt, info);
    expect(key.length).toBe(32);
  });
});
