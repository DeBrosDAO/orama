import type { QueryState } from "../../data/use-query";
import { ARCHIVE_QUORUM } from "../../model/storage";
import type { ArchiveHealth, ArchiveRange, RangeState } from "../../model/storage";
import { formatInt } from "../../model/units";
import { Badge } from "../../ui/badge";
import type { BadgeTone } from "../../ui/badge";
import { BlockLink } from "../../ui/links";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Query, Skeleton } from "../../ui/query-states";

const RANGE_TONE: Record<RangeState, BadgeTone> = { none: "mute", attesting: "signal", decided: "signal", archived: "ok" };
const RANGE_LABEL: Record<RangeState, string> = {
  none: "no attestation yet",
  attesting: "collecting attestations",
  decided: "decided, waiting for replica deals",
  archived: "archived",
};
const RETAIN_TIP =
  "Nodes may delete blocks below this height. It is the older of: the tip minus the retention window, and the last archived height, so a node never deletes blocks the archive does not hold.";
const QUORUM_TIP = `A range is archived when at least ${ARCHIVE_QUORUM.attesters} distinct operators attested the same bundle and at least ${ARCHIVE_QUORUM.replicaDeals} archive deals hold it. Both numbers are fixed by the chain.`;

function Fact({ label, value, hint }: { label: React.ReactNode; value: React.ReactNode; hint?: React.ReactNode }) {
  return (
    <div>
      <dt className="text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">{label}</dt>
      <dd className="mt-0.5 text-lg font-semibold tabular-nums">{value}</dd>
      {hint && <dd className="text-[13px] text-muted">{hint}</dd>}
    </div>
  );
}

function RangeRow({ range }: { range: ArchiveRange }) {
  return (
    <li className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-border/60 py-2 first:border-t-0 text-sm">
      <span className="font-mono tabular-nums">
        {formatInt(range.start)} to {formatInt(range.end)}
      </span>
      <span className="flex items-center gap-3">
        {range.state !== "none" && (
          <span className="text-muted">
            {range.attesters} attesters ({ARCHIVE_QUORUM.attesters} needed) · {range.replicaDeals} deals ({ARCHIVE_QUORUM.replicaDeals} needed)
          </span>
        )}
        <Badge tone={RANGE_TONE[range.state]}>{RANGE_LABEL[range.state]}</Badge>
      </span>
    </li>
  );
}

function ArchiveBody({ health }: { health: ArchiveHealth }) {
  const nothingPrunable = health.retainHeight <= 0;
  return (
    <>
      <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
        <Fact
          label="Last archived height"
          value={health.lastArchivedHeight === 0 ? "none yet" : <BlockLink height={health.lastArchivedHeight} />}
          hint={`tip ${formatInt(health.tip)}`}
        />
        <Fact
          label={
            <>
              Retain height
              <Help tip={RETAIN_TIP} />
            </>
          }
          value={nothingPrunable ? "nothing prunable" : formatInt(health.retainHeight)}
          hint={`${formatInt(health.retentionWindowBlocks)} blocks are always kept`}
        />
        <Fact
          label="Retain lag"
          value={`${formatInt(health.retainLag)} blocks`}
          hint={health.retainLag === 0 ? "the archive keeps up with the window" : "the archive holds pruning back by this much"}
        />
        <Fact
          label={
            <>
              Not archived yet
              <Help tip={QUORUM_TIP} />
            </>
          }
          value={`${formatInt(health.unarchivedRanges)} range${health.unarchivedRanges === 1 ? "" : "s"}`}
          hint={`ranges are ${formatInt(health.rangeBlocks)} blocks wide`}
        />
      </dl>
      <h3 className="mt-5 text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">Oldest ranges not archived</h3>
      {health.oldestUnarchived.length === 0 ? (
        <p className="mt-1 text-sm text-muted">Every complete range up to the tip is archived.</p>
      ) : (
        <ul className="mt-1">
          {health.oldestUnarchived.map((r) => (
            <RangeRow key={r.start} range={r} />
          ))}
        </ul>
      )}
    </>
  );
}

/** The block archive: how far it has got, what may be pruned, and where the oldest gap stands. */
export function ArchiveCard({ state, onRetry }: { state: QueryState<ArchiveHealth>; onRetry: () => void }) {
  return (
    <Card title="Block archive">
      <Query state={state} onRetry={onRetry} loading={<Skeleton className="h-48 w-full" />}>
        {(health) => <ArchiveBody health={health} />}
      </Query>
    </Card>
  );
}
