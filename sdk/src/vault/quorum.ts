/**
 * Quorum calculations for distributed vault operations.
 * Must match orama-vault (Zig side).
 */

/** Adaptive Shamir threshold: max(3, floor(N/3)). */
export function adaptiveThreshold(n: number): number {
  return Math.max(3, Math.floor(n / 3));
}

/** Write quorum: ceil(2N/3). Requires majority for consistency. */
export function writeQuorum(n: number): number {
  if (n === 0) return 0;
  if (n <= 2) return n;
  return Math.ceil((2 * n) / 3);
}
