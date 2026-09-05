/**
 * Shamir's Secret Sharing over GF(2^8)
 *
 * Information-theoretic secret splitting: any K shares reconstruct the secret,
 * K-1 shares reveal zero information.
 *
 * Uses GF(2^8) with irreducible polynomial x^8 + x^4 + x^3 + x + 1 (0x11B),
 * same as AES. This is the standard choice for byte-level SSS.
 */

import { randomBytes } from '@noble/ciphers/webcrypto';

// ── GF(2^8) Arithmetic ─────────────────────────────────────────────────────

const IRREDUCIBLE = 0x11b;

/** Exponential table: exp[log[a] + log[b]] = a * b */
const EXP_TABLE = new Uint8Array(512);

/** Logarithm table: log[a] for a in 1..255 (log[0] is undefined) */
const LOG_TABLE = new Uint8Array(256);

// Build log/exp tables using generator 3
(function buildTables() {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    EXP_TABLE[i] = x;
    LOG_TABLE[x] = i;
    x = x ^ (x << 1); // multiply by generator (3 is primitive in this field)
    if (x >= 256) x ^= IRREDUCIBLE;
  }
  // Extend exp table for easy modular arithmetic (avoid mod 255)
  for (let i = 255; i < 512; i++) {
    EXP_TABLE[i] = EXP_TABLE[i - 255]!;
  }
})();

/** GF(2^8) addition: XOR */
function gfAdd(a: number, b: number): number {
  return a ^ b;
}

/** GF(2^8) multiplication via log/exp tables */
function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0;
  return EXP_TABLE[LOG_TABLE[a]! + LOG_TABLE[b]!]!;
}

/** GF(2^8) multiplicative inverse */
function gfInv(a: number): number {
  if (a === 0) throw new Error('GF(2^8): division by zero');
  return EXP_TABLE[255 - LOG_TABLE[a]!]!;
}

/** GF(2^8) division: a / b */
function gfDiv(a: number, b: number): number {
  if (b === 0) throw new Error('GF(2^8): division by zero');
  if (a === 0) return 0;
  return EXP_TABLE[(LOG_TABLE[a]! - LOG_TABLE[b]! + 255) % 255]!;
}

// ── Share Type ──────────────────────────────────────────────────────────────

/** A single Shamir share */
export interface Share {
  /** Share index (1..N, never 0) */
  x: number;
  /** Share data (same length as secret) */
  y: Uint8Array;
}

// ── Split ───────────────────────────────────────────────────────────────────

/**
 * Splits a secret into N shares with threshold K.
 *
 * @param secret - Secret bytes to split (any length)
 * @param n - Total number of shares to create (2..255)
 * @param k - Minimum shares needed for reconstruction (2..n)
 * @returns Array of N shares
 */
export function split(secret: Uint8Array, n: number, k: number): Share[] {
  if (k < 2) throw new Error('Threshold K must be at least 2');
  if (n < k) throw new Error('Share count N must be >= threshold K');
  if (n > 255) throw new Error('Maximum 255 shares (GF(2^8) limit)');
  if (secret.length === 0) throw new Error('Secret must not be empty');

  const coefficients = new Array<Uint8Array>(secret.length);
  for (let i = 0; i < secret.length; i++) {
    const poly = new Uint8Array(k);
    poly[0] = secret[i]!;
    const rand = randomBytes(k - 1);
    poly.set(rand, 1);
    coefficients[i] = poly;
  }

  const shares: Share[] = [];
  for (let xi = 1; xi <= n; xi++) {
    const y = new Uint8Array(secret.length);
    for (let byteIdx = 0; byteIdx < secret.length; byteIdx++) {
      y[byteIdx] = evaluatePolynomial(coefficients[byteIdx]!, xi);
    }
    shares.push({ x: xi, y });
  }

  for (const poly of coefficients) {
    poly.fill(0);
  }

  return shares;
}

function evaluatePolynomial(coeffs: Uint8Array, x: number): number {
  let result = 0;
  for (let i = coeffs.length - 1; i >= 0; i--) {
    result = gfAdd(gfMul(result, x), coeffs[i]!);
  }
  return result;
}

// ── Combine ─────────────────────────────────────────────────────────────────

/**
 * Reconstructs a secret from K or more shares using Lagrange interpolation.
 *
 * @param shares - Array of K or more shares (must all have same y.length)
 * @returns Reconstructed secret
 */
export function combine(shares: Share[]): Uint8Array {
  if (shares.length < 2) throw new Error('Need at least 2 shares');

  const secretLength = shares[0]!.y.length;
  for (const share of shares) {
    if (share.y.length !== secretLength) {
      throw new Error('All shares must have the same data length');
    }
    if (share.x === 0) {
      throw new Error('Share index must not be 0');
    }
  }

  const xValues = new Set(shares.map(s => s.x));
  if (xValues.size !== shares.length) {
    throw new Error('Duplicate share indices');
  }

  const secret = new Uint8Array(secretLength);

  for (let byteIdx = 0; byteIdx < secretLength; byteIdx++) {
    let value = 0;

    for (let i = 0; i < shares.length; i++) {
      const xi = shares[i]!.x;
      const yi = shares[i]!.y[byteIdx]!;

      let basis = 1;
      for (let j = 0; j < shares.length; j++) {
        if (i === j) continue;
        const xj = shares[j]!.x;
        basis = gfMul(basis, gfDiv(xj, gfAdd(xi, xj)));
      }

      value = gfAdd(value, gfMul(yi, basis));
    }

    secret[byteIdx] = value;
  }

  return secret;
}

/** @internal Exported for cross-platform test vector validation */
export const _gf = { add: gfAdd, mul: gfMul, inv: gfInv, div: gfDiv, EXP_TABLE, LOG_TABLE } as const;
