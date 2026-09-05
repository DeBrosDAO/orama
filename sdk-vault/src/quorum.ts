/**
 * Quorum calculations for distributed vault operations.
 * Must match orama-vault (Zig side).
 */

/** Adaptive Shamir read threshold K = max(2, floor(N/3)). */
export function adaptiveThreshold(n: number): number {
  return Math.max(2, Math.floor(n / 3));
}

/**
 * Write quorum W = min(N, max(K+1, ceil(2N/3))).
 *
 * The invariant W > K guarantees a write reported successful has persisted more
 * shares than a read requires, so it is always recoverable (and survives losing
 * at least one guardian). The old formula (ceil(2N/3), K floored at 3) gave
 * W=2 < K=3 at N=3 — a "successful" write could be permanently unrecoverable.
 */
export function writeQuorum(n: number): number {
  if (n <= 0) return 0;
  const k = adaptiveThreshold(n);
  let w = Math.ceil((2 * n) / 3);
  if (w < k + 1) w = k + 1;
  if (w > n) w = n;
  return w;
}
