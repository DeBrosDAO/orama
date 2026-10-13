import { groupedInteger } from "../../model/paging";
import type { ShieldedTx } from "../../model/shielded";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { IndexQuery } from "../../ui/index-query";
import { LoadMore } from "../../ui/load-more";
import { Skeleton } from "../../ui/query-states";
import type { KeyPaged } from "../../ui/use-key-paged";
import { ActivityTable } from "./activity-table";
import { AMOUNT_FRACTION, FEE_FRACTION } from "./constants";
import { summarizeWindow } from "./window-summary";


/** Totals over the rows on screen, in plain words. They are for those rows only: the index keeps no running total. */
export function PageTotals({ activity }: { activity: { items: ShieldedTx[] } }) {
  const t = summarizeWindow(activity.items);
  if (t.accepted === 0) return null;
  return (
    <p className="mb-3 text-[13px] text-muted">
      In the rows shown: <b className="font-medium text-fg"><Amount norama={t.shieldedAmount.toString()} maxFraction={AMOUNT_FRACTION} /></b> shielded in {t.shieldedCount}{" "}
      {t.shieldedCount === 1 ? "transaction" : "transactions"}, <b className="font-medium text-fg"><Amount norama={t.unshieldedAmount.toString()} maxFraction={AMOUNT_FRACTION} /></b>{" "}
      unshielded in {t.unshieldedCount}, and {t.transferCount} private {t.transferCount === 1 ? "transfer" : "transfers"} paying{" "}
      <b className="font-medium text-fg"><Amount norama={t.transferFees.toString()} maxFraction={FEE_FRACTION} /></b> in fees. The amounts of the private transfers are not on chain.
    </p>
  );
}

/** The transactions that touched the pool, newest first. More are read from the indexer's cursor on request, so a deep page costs what the first does. */
export function ActivityCard({ paged, total }: { paged: KeyPaged<ShieldedTx>; total: number | null }) {
  return (
    <Card title="Transactions that touched the pool">
      <IndexQuery state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-64 w-full" />}>
        {() =>
          paged.items.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted">No transaction has touched the shielded pool in this index.</p>
          ) : (
            <>
              <PageTotals activity={paged} />
              <ActivityTable rows={paged.items} />
              <LoadMore paged={paged} what="transactions" />
            </>
          )
        }
      </IndexQuery>
      <p className="mt-3 text-xs text-muted">
        {total === null ? "" : `${groupedInteger(String(total))} transactions in all since this node's index began. `}
        The index lists the transactions whose events name the pool's account: every shield, shield of earnings and unshield, and every private
        transfer, which burns its fee from the pool. A running total of all volume is not kept, so the totals above cover the rows shown only.
      </p>
    </Card>
  );
}
