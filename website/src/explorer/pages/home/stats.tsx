import type { QueryState } from "../../data/use-query";
import type { NetworkSnapshot } from "../../model/types";
import { formatCompact, formatInt, formatNorama } from "../../model/units";
import { formatCountdown } from "../../model/time";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Query, Skeleton } from "../../ui/query-states";
import { Sparkline } from "../../ui/sparkline";
import { Stat } from "../../ui/stat";
import { useNow } from "../../ui/use-now";
import { cn } from "../../../lib/utils";
import { deltaText, epochPercent } from "./logic";
import type { DeltaTone } from "./logic";

const GRID = "grid grid-cols-2 gap-3.5 lg:grid-cols-4";
const STAT_COUNT = 4;
const FEE_MAX_FRACTION = 2;
const BASE_FEE_MAX_FRACTION = 3;

const TIP_BURN = "Every transaction pays a base fee that is destroyed, not paid to anyone. It slowly reduces supply.";
const TIP_EPOCH = "An epoch is a day-long accounting period. New ORAMA is created once per epoch, following a fixed public schedule.";

const DELTA_CLASS: Record<DeltaTone, string> = { up: "text-gain", down: "text-loss", flat: "text-muted" };

function StatsSkeleton() {
  return (
    <div className={GRID}>
      {Array.from({ length: STAT_COUNT }, (_, i) => (
        <Card key={i}>
          <Skeleton className="mb-3 h-3 w-24" />
          <Skeleton className="h-8 w-28" />
          <Skeleton className="mt-2 h-4 w-32" />
          <Skeleton className="mt-2 h-[26px] w-[100px]" />
        </Card>
      ))}
    </div>
  );
}

function TransactionsDetail({ changePct }: { changePct: number | null }) {
  const delta = deltaText(changePct);
  if (!delta) return null;
  return (
    <>
      <span className={cn(DELTA_CLASS[delta.tone])}>{delta.text}</span>
      {delta.tone !== "flat" && " vs yesterday"}
    </>
  );
}

function EpochBar({ progress, number }: { progress: number; number: number }) {
  const pct = epochPercent(progress);
  return (
    <div
      role="progressbar"
      aria-label={`Epoch ${number} progress`}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={pct}
      className="h-1 overflow-hidden rounded-full bg-surface-3"
    >
      <div className="h-full rounded-full bg-fg/60" style={{ width: `${pct}%` }} />
    </div>
  );
}

function StatTiles({ network }: { network: NetworkSnapshot }) {
  const now = useNow();
  const n = network;
  return (
    <div className={GRID}>
      <Stat
        label="Transactions · 24h"
        value={formatInt(n.transactions24h)}
        detail={<TransactionsDetail changePct={n.transactionsChangePct} />}
        footer={<Sparkline values={n.transactionsSeries} />}
      />
      <Stat
        label="Failed · 24h"
        value={formatInt(n.failed24h)}
        detail={`of ${formatInt(n.transactions24h)} transactions`}
      />
      <Stat
        label={<>Fees burned · 24h<Help tip={TIP_BURN} /></>}
        value={
          <>
            {formatNorama(n.burned24h, FEE_MAX_FRACTION)}
            <small className="ml-1 text-[13px] font-normal text-muted">ORAMA</small>
          </>
        }
        detail={`base fee ${n.baseFee.toLocaleString("en-US", { maximumFractionDigits: BASE_FEE_MAX_FRACTION })} norama/gas`}
      />
      <Stat
        label="Total supply"
        value={formatCompact(n.supply)}
        detail={
          <>
            epoch {n.epoch.number} · ends in {formatCountdown(n.epoch.endsAt, now)}
            <Help tip={TIP_EPOCH} />
          </>
        }
        footer={<EpochBar progress={n.epoch.progress} number={n.epoch.number} />}
      />
    </div>
  );
}

export function StatTilesSection({ state, onRetry }: { state: QueryState<NetworkSnapshot>; onRetry: () => void }) {
  return (
    <Query state={state} loading={<StatsSkeleton />} onRetry={onRetry}>
      {(network) => <StatTiles network={network} />}
    </Query>
  );
}
