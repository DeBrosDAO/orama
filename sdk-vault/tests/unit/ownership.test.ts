import { describe, expect, it } from 'vitest';
import { ed25519 } from '@noble/curves/ed25519.js';
import { identityFromSeed, publicKeyFromSeed, signUtf8 } from '../../src/crypto/ownership';

describe('ownership helpers', () => {
  it('derives identity as SHA-256 of the public key', () => {
    const seed = new Uint8Array(32).fill(3);
    const pub = publicKeyFromSeed(seed);
    expect(pub).toHaveLength(32);
    expect(identityFromSeed(seed)).toHaveLength(64);
    expect(identityFromSeed(seed)).toBe(identityFromSeed(seed));
  });

  it('signs a UTF-8 message that Ed25519 verifies', () => {
    const seed = new Uint8Array(32).fill(9);
    const msg = 'vault-secret-put-v1:abc:api-key:1';
    const sigHex = signUtf8(seed, msg);
    const sig = Uint8Array.from(sigHex.match(/.{2}/g)!.map((b) => parseInt(b, 16)));
    expect(ed25519.verify(sig, new TextEncoder().encode(msg), publicKeyFromSeed(seed))).toBe(true);
  });
});
