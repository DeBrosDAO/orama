import { describe, expect, it } from 'vitest';
import { adaptiveThreshold, writeQuorum } from '../../src/quorum';

/**
 * These mirror vault/src/membership/quorum.zig, which is the implementation
 * the guardians actually run. The two must agree exactly: a TypeScript client
 * that thinks a write needed 2 acknowledgements when the guardian required 3
 * reports a write as successful that the guardian refused.
 *
 * The tests used to assert the old contract — adaptiveThreshold(3) === 3 and
 * writeQuorum(3) === 2 — long after both implementations had changed. Three of
 * them failed on every run, which is exactly the noise that makes a suite stop
 * being read.
 */

describe('adaptiveThreshold', () => {
  // The exact values asserted by readQuorum in quorum.zig.
  it('is max(2, floor(N/3)), matching the Zig guardian', () => {
    expect(adaptiveThreshold(0)).toBe(2);
    expect(adaptiveThreshold(3)).toBe(2); // 3/3 = 1, floored at 2
    expect(adaptiveThreshold(5)).toBe(2); // 5/3 = 1, floored at 2
    expect(adaptiveThreshold(9)).toBe(3);
    expect(adaptiveThreshold(14)).toBe(4);
    expect(adaptiveThreshold(100)).toBe(33);
  });

  // Two is the smallest threshold that keeps the secret secret: with K = 1 a
  // single guardian holds enough to reconstruct on its own.
  it('never drops below 2', () => {
    for (let n = 0; n <= 9; n++) {
      expect(adaptiveThreshold(n)).toBeGreaterThanOrEqual(2);
    }
  });

  it('never decreases as the cluster grows', () => {
    let previous = adaptiveThreshold(0);
    for (let n = 1; n <= 255; n++) {
      const current = adaptiveThreshold(n);
      expect(current, `adaptiveThreshold(${n}) went down`).toBeGreaterThanOrEqual(previous);
      previous = current;
    }
  });
});

describe('writeQuorum', () => {
  // The exact values asserted by writeQuorum in quorum.zig.
  it('is min(N, max(K+1, ceil(2N/3))), matching the Zig guardian', () => {
    expect(writeQuorum(0)).toBe(0);
    expect(writeQuorum(1)).toBe(1);
    expect(writeQuorum(2)).toBe(2);
    expect(writeQuorum(3)).toBe(3); // max(K+1 = 3, ceil(6/3) = 2)
    expect(writeQuorum(4)).toBe(3); // ceil(8/3)
    expect(writeQuorum(5)).toBe(4); // ceil(10/3)
    expect(writeQuorum(14)).toBe(10); // ceil(28/3)
  });

  /**
   * The reason the formula changed. Under the old one a three-guardian cluster
   * had W = 2 and K = 3, so a write reported successful had persisted fewer
   * shares than a read needs — permanently unrecoverable, and nothing said so.
   */
  it('always stores more shares than a read needs', () => {
    for (let n = 1; n <= 255; n++) {
      expect(
        writeQuorum(n),
        `a write to ${n} guardians would be unrecoverable`,
      ).toBeGreaterThan(Math.min(adaptiveThreshold(n), n) - 1);
    }
    // Stated exactly, wherever the cluster is big enough for K+1 to fit.
    for (let n = 3; n <= 255; n++) {
      expect(writeQuorum(n), `W must exceed K at N=${n}`).toBeGreaterThan(adaptiveThreshold(n));
    }
  });

  it('is a majority for N >= 3', () => {
    for (let n = 3; n <= 255; n++) {
      expect(writeQuorum(n)).toBeGreaterThan(n / 2);
    }
  });

  it('never asks for more guardians than exist', () => {
    for (let n = 0; n <= 255; n++) {
      expect(writeQuorum(n)).toBeLessThanOrEqual(n);
    }
  });

  /**
   * Any write set and any read set must share at least one guardian, or a read
   * can collect K shares none of which came from the newest write and
   * reconstruct a secret that is no longer current. The condition is
   * W + K >= N, which is what vault/src/test_integration.zig asserts.
   *
   * It is >= rather than >: at N=6, W=4 and K=2 meet exactly, and that is the
   * intended margin.
   */
  it('overlaps the read set', () => {
    for (let n = 1; n <= 255; n++) {
      expect(
        writeQuorum(n) + adaptiveThreshold(n),
        `read and write sets do not overlap at N=${n}`,
      ).toBeGreaterThanOrEqual(n);
    }
  });
});
