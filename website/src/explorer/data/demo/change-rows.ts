import type { BalanceChange } from "../../model/types";
import type { Account } from "./ledger";
import type { SystemDelta } from "./plans";

/**
 * The balance rows of one transaction: the signer, then every other wallet
 * whose balance moved, then each pool the plan touched, then the fee as a
 * positive "burned" row. Wallet deltas plus pool deltas always sum to zero.
 */
export function changeRows(
  touched: readonly Account[],
  before: ReadonlyMap<Account, bigint>,
  fee: bigint,
  system: readonly SystemDelta[],
): BalanceChange[] {
  const rows: BalanceChange[] = [];
  touched.forEach((a, i) => {
    const b = before.get(a) as bigint;
    if (i > 0 && a.available === b) return;
    rows.push({
      party: { kind: "wallet", ref: a.ref },
      before: b.toString(),
      after: a.available.toString(),
      delta: (a.available - b).toString(),
    });
  });
  for (const s of system) rows.push({ party: { kind: "system", name: s.name }, before: null, after: null, delta: s.delta.toString() });
  rows.push({ party: { kind: "system", name: "burned" }, before: null, after: null, delta: fee.toString() });
  return rows;
}
