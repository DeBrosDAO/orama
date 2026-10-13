import { useQuery } from "../../data/use-query";
import { MAX_REPLICAS_READ } from "../../data/chain/storage";
import type { DealReplica, SlotStatus, StorageDeal } from "../../model/storage";
import { formatBytes } from "../../model/units";
import { Badge } from "../../ui/badge";
import type { BadgeTone } from "../../ui/badge";
import { NodeLink } from "../../ui/links";
import { Query, Skeleton } from "../../ui/query-states";

const SLOT_TONE: Record<SlotStatus, BadgeTone> = {
  unassigned: "mute",
  assigned: "signal",
  active: "ok",
  evicted: "bad",
  released: "mute",
};

const SLOT_TIP: Record<SlotStatus, string> = {
  unassigned: "No provider holds this replica yet.",
  assigned: "A provider was assigned the replica and has not accepted it yet.",
  active: "A provider holds the replica and answers its proof challenges.",
  evicted: "The provider missed too many challenges and lost the replica; it waits for another provider.",
  released: "The provider gave the replica up for a legal reason.",
};

function ReplicaRows({ replicas }: { replicas: readonly DealReplica[] }) {
  return (
    <ul className="divide-y divide-border/60 text-sm">
      {replicas.map((r) => (
        <li key={r.index} className="flex flex-wrap items-center gap-x-4 gap-y-1 py-1.5">
          <span className="w-16 text-muted">Replica {r.index + 1}</span>
          <span title={SLOT_TIP[r.status]}>
            <Badge tone={SLOT_TONE[r.status]}>{r.status}</Badge>
          </span>
          <span className="min-w-0">{r.nodeId === null ? <span className="text-muted">no provider</span> : <NodeLink id={r.nodeId} />}</span>
          {r.consecutiveMisses > 0 && <span className="text-loss">{r.consecutiveMisses} challenge{r.consecutiveMisses === 1 ? "" : "s"} missed in a row</span>}
          {r.status !== "unassigned" && <span className="text-muted">{formatBytes(r.pieceBytes)}</span>}
        </li>
      ))}
    </ul>
  );
}

/** A deal's replicas, read when the row is opened. */
export function DealReplicas({ deal }: { deal: StorageDeal }) {
  const { state, refetch } = useQuery((s) => s.getDealReplicas(deal.id, deal.replicas), [deal.id, deal.replicas]);
  return (
    <div className="px-2 py-2">
      <Query state={state} onRetry={refetch} loading={<Skeleton className="h-16 w-full" />}>
        {(replicas) =>
          replicas.length === 0 ? <p className="text-sm text-muted">This deal has no replicas to show.</p> : <ReplicaRows replicas={replicas} />
        }
      </Query>
      {deal.replicas > MAX_REPLICAS_READ && (
        <p className="mt-1 text-[13px] text-muted">The deal asks for {deal.replicas} replicas; the first {MAX_REPLICAS_READ} are shown.</p>
      )}
    </div>
  );
}
