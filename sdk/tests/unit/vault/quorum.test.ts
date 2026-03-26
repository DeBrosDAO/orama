import { describe, it, expect } from 'vitest';
import { adaptiveThreshold, writeQuorum } from '../../../src/vault/quorum';

describe('adaptiveThreshold', () => {
  it('returns max(3, floor(N/3))', () => {
    expect(adaptiveThreshold(3)).toBe(3);
    expect(adaptiveThreshold(9)).toBe(3);
    expect(adaptiveThreshold(12)).toBe(4);
    expect(adaptiveThreshold(30)).toBe(10);
    expect(adaptiveThreshold(100)).toBe(33);
  });

  it('minimum is 3', () => {
    for (let n = 0; n <= 9; n++) {
      expect(adaptiveThreshold(n)).toBeGreaterThanOrEqual(3);
    }
  });

  it('monotonically non-decreasing', () => {
    let prev = adaptiveThreshold(0);
    for (let n = 1; n <= 255; n++) {
      const current = adaptiveThreshold(n);
      expect(current).toBeGreaterThanOrEqual(prev);
      prev = current;
    }
  });
});

describe('writeQuorum', () => {
  it('returns ceil(2N/3) for N >= 3', () => {
    expect(writeQuorum(3)).toBe(2);
    expect(writeQuorum(6)).toBe(4);
    expect(writeQuorum(10)).toBe(7);
    expect(writeQuorum(100)).toBe(67);
  });

  it('returns 0 for N=0', () => {
    expect(writeQuorum(0)).toBe(0);
  });

  it('returns N for N <= 2', () => {
    expect(writeQuorum(1)).toBe(1);
    expect(writeQuorum(2)).toBe(2);
  });

  it('always > N/2 for N >= 3', () => {
    for (let n = 3; n <= 255; n++) {
      expect(writeQuorum(n)).toBeGreaterThan(n / 2);
    }
  });

  it('never exceeds N', () => {
    for (let n = 0; n <= 255; n++) {
      expect(writeQuorum(n)).toBeLessThanOrEqual(n);
    }
  });
});
