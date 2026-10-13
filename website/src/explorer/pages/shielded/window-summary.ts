import type { ShieldedTx } from "../../model/shielded";

/** What crossed the pool's boundary in a run of transactions, in norama. */
export interface WindowSummary {
  /** Transactions counted: the ones the chain accepted, each with a bundle. */
  accepted: number;
  /** Transactions without a bundle: ones the chain refused, which moved nothing. */
  refused: number;
  shieldedAmount: bigint;
  shieldedCount: number;
  unshieldedAmount: bigint;
  unshieldedCount: number;
  /** Private transfers: their fee is all that is public of them. */
  transferCount: number;
  transferFees: bigint;
}

/**
 * Totals over the transactions given. A shield (of a balance or of earnings) adds to the shielded
 * amount and an unshield to the unshielded one; a private transfer adds only its count and fee,
 * since it moves value inside the pool and the chain does not say how much.
 */
export function summarizeWindow(txs: readonly ShieldedTx[]): WindowSummary {
  const out: WindowSummary = {
    accepted: 0,
    refused: 0,
    shieldedAmount: 0n,
    shieldedCount: 0,
    unshieldedAmount: 0n,
    unshieldedCount: 0,
    transferCount: 0,
    transferFees: 0n,
  };
  for (const tx of txs) {
    if (!tx.ok || tx.bundle === null) {
      out.refused += 1;
      continue;
    }
    out.accepted += 1;
    const amount = BigInt(tx.bundle.amount);
    switch (tx.bundle.kind) {
      case "shield":
      case "shield_earnings":
        out.shieldedAmount += amount;
        out.shieldedCount += 1;
        break;
      case "unshield":
        out.unshieldedAmount += amount;
        out.unshieldedCount += 1;
        break;
      case "transfer":
        out.transferFees += amount;
        out.transferCount += 1;
        break;
    }
  }
  return out;
}
