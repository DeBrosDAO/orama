import { useLiveQuery } from "../data/use-query";
import { ACTIVITY_ROWS, ACTIVITY_WINDOW, moduleTxs } from "../model/module-activity";
import type { TxSummary } from "../model/types";
import { Card } from "./card";
import { IndexQuery } from "./index-query";
import { Skeleton } from "./query-states";
import { TxRow } from "./tx-row";

/** The rows of a module's recent activity, or the sentence that says there is none. */
export function ActivityRows({ txs, empty }: { txs: readonly TxSummary[]; empty: string }) {
  const rows = txs.slice(0, ACTIVITY_ROWS);
  if (rows.length === 0) return <p className="py-4 text-center text-sm text-muted">{empty}</p>;
  return (
    <div className="divide-y divide-border">
      {rows.map((tx) => (
        <TxRow key={tx.hash} tx={tx} />
      ))}
    </div>
  );
}

/**
 * What the newest transactions did with one module. The chain has no list of a module's records, so
 * this is the part of the newest transactions that carries the module's messages, and it says so.
 */
export function ModuleActivity({ title, modules, empty }: { title: string; modules: readonly string[]; empty: string }) {
  const { state, refetch } = useLiveQuery((s) => s.getLatestActivity("all", ACTIVITY_WINDOW), []);
  return (
    <Card title={title}>
      <IndexQuery state={state} onRetry={refetch} loading={<Skeleton className="h-40 w-full" />}>
        {(txs) => <ActivityRows txs={moduleTxs(txs, modules)} empty={empty} />}
      </IndexQuery>
      <p className="mt-3 text-xs text-muted">Picked from the newest {ACTIVITY_WINDOW} transactions: the chain keeps no list of its own, so older activity is not shown here.</p>
    </Card>
  );
}
