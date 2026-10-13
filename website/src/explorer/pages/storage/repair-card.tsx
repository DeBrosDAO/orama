import type { QueryState } from "../../data/use-query";
import type { DealList, SettlementQueue } from "../../model/storage";
import { formatInt } from "../../model/units";
import { Card } from "../../ui/card";
import { Query, Skeleton } from "../../ui/query-states";
import { repairDelegates } from "./logic";

function QueueLine({ state, onRetry }: { state: QueryState<SettlementQueue>; onRetry: () => void }) {
  return (
    <Query state={state} onRetry={onRetry} loading={<Skeleton className="h-6 w-full" />}>
      {(queue) => (
        <p className="text-sm">
          <span className="font-mono tabular-nums">{formatInt(queue.pending)}</span> settlement{queue.pending === 1 ? "" : "s"} waiting.{" "}
          <span className="text-muted">
            Each is a payment to a provider that proved its replica, or a penalty for one that missed; a block applies a limited number.
          </span>
        </p>
      )}
    </Query>
  );
}

function DelegateLine({ deals }: { deals: QueryState<DealList> }) {
  if (deals.status !== "ready") return null;
  const count = repairDelegates(deals.data.deals);
  return (
    <p className="mt-2 text-sm">
      <span className="font-mono tabular-nums">{formatInt(count)}</span> of the newest {formatInt(deals.data.deals.length)} deals name an operator to repair for the
      client.
    </p>
  );
}

/** What the chain shows of replica upkeep. Repair work itself happens off chain. */
export function RepairCard({ queue, deals, onRetry }: { queue: QueryState<SettlementQueue>; deals: QueryState<DealList>; onRetry: () => void }) {
  return (
    <Card title="Settlement and repair">
      <QueueLine state={queue} onRetry={onRetry} />
      <DelegateLine deals={deals} />
      <p className="mt-3 text-[13px] text-muted">
        A deal can name a repair delegate: a service that copies a surviving replica to a replacement provider when a slot loses its provider. That copying
        happens off chain and the chain keeps no record of it. What the chain shows is each replica&apos;s state (active, evicted, released) and how many
        challenges in a row it missed; open a deal above to read them.
      </p>
    </Card>
  );
}
