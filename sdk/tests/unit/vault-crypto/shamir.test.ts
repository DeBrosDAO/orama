import { describe, it, expect } from 'vitest';
import { split, combine } from '../../../src/vault/crypto/shamir';

describe('Shamir SSS', () => {
  it('2-of-3 round-trip', () => {
    const secret = new Uint8Array([42]);
    const shares = split(secret, 3, 2);
    expect(shares).toHaveLength(3);

    const recovered = combine([shares[0]!, shares[1]!]);
    expect(recovered).toEqual(secret);

    const recovered2 = combine([shares[0]!, shares[2]!]);
    expect(recovered2).toEqual(secret);
  });

  it('3-of-5 multi-byte', () => {
    const secret = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
    const shares = split(secret, 5, 3);
    expect(shares).toHaveLength(5);
    const recovered = combine([shares[0]!, shares[2]!, shares[4]!]);
    expect(recovered).toEqual(secret);
  });

  it('all C(5,3) subsets reconstruct', () => {
    const secret = new Uint8Array([42, 137, 255, 0]);
    const shares = split(secret, 5, 3);
    for (let i = 0; i < 5; i++) {
      for (let j = i + 1; j < 5; j++) {
        for (let l = j + 1; l < 5; l++) {
          const recovered = combine([shares[i]!, shares[j]!, shares[l]!]);
          expect(recovered).toEqual(secret);
        }
      }
    }
  });

  it('share indices are 1..N', () => {
    const shares = split(new Uint8Array([42]), 5, 3);
    expect(shares.map(s => s.x)).toEqual([1, 2, 3, 4, 5]);
  });

  it('throws on K < 2', () => {
    expect(() => split(new Uint8Array([1]), 3, 1)).toThrow('Threshold K must be at least 2');
  });

  it('throws on N < K', () => {
    expect(() => split(new Uint8Array([1]), 2, 3)).toThrow('Share count N must be >= threshold K');
  });

  it('throws on N > 255', () => {
    expect(() => split(new Uint8Array([1]), 256, 2)).toThrow('Maximum 255 shares');
  });

  it('throws on empty secret', () => {
    expect(() => split(new Uint8Array(0), 3, 2)).toThrow('Secret must not be empty');
  });

  it('throws on duplicate shares', () => {
    expect(() => combine([
      { x: 1, y: new Uint8Array([1]) },
      { x: 1, y: new Uint8Array([2]) },
    ])).toThrow('Duplicate share indices');
  });

  it('throws on mismatched lengths', () => {
    expect(() => combine([
      { x: 1, y: new Uint8Array([1, 2]) },
      { x: 2, y: new Uint8Array([3]) },
    ])).toThrow('same data length');
  });

  it('large secret (256 bytes)', () => {
    const secret = new Uint8Array(256);
    for (let i = 0; i < 256; i++) secret[i] = i;
    const shares = split(secret, 10, 5);
    const recovered = combine(shares.slice(0, 5));
    expect(recovered).toEqual(secret);
  });
});
