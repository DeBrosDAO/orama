import type { ReactNode } from "react";
import { hasFigures } from "../../model/epochs";
import type { EpochRow, SupplyPoint } from "../../model/epochs";
import { formatInt, formatNorama } from "../../model/units";
import { Card } from "../../ui/card";
import { SeriesChart } from "../../ui/series-chart";
import type { ChartTone } from "../../ui/series-chart";
import { gapEpochs, NOT_AVAILABLE, UnavailableNote } from "../epochs/unavailable";
import { epochSeries, supplySeries, supplySlots, supplySpan } from "./model";

const BURN_FRACTION = 3;
const SUPPLY_GAP_NOTE =
  "A blank stretch is an epoch the index has no supply point for: it closed while the node no longer kept its state. The line stops there and starts again; nothing is drawn or filled in for it.";

const whole = (v: bigint) => formatNorama(v.toString(), 0);
const precise = (v: bigint) => formatNorama(v.toString(), BURN_FRACTION);

interface SupplyTrend {
  title: string;
  label: string;
  pick: (p: SupplyPoint) => string;
  tone: ChartTone;
}

const SUPPLY_TRENDS: SupplyTrend[] = [
  { title: "Total supply", label: "Total supply after each closed epoch", pick: (p) => p.totalSupply, tone: "signal" },
  { title: "Circulating", label: "Circulating supply after each closed epoch", pick: (p) => p.circulating, tone: "gain" },
  { title: "Staked", label: "ORAMA bonded to validators after each closed epoch", pick: (p) => p.bonded, tone: "signal" },
  { title: "Burned to date", label: "Cumulative ORAMA burned after each closed epoch", pick: (p) => p.burnedToDate, tone: "loss" },
];

function SupplyTrendCard({ trend, points }: { trend: SupplyTrend; points: readonly SupplyPoint[] }) {
  const span = supplySpan(points);
  if (span === null) return null;
  return (
    <Card title={trend.title}>
      <SeriesChart
        label={trend.label}
        kind="line"
        values={supplySeries(points, trend.pick)}
        format={whole}
        from={`epoch ${formatInt(span.first)}`}
        to={`epoch ${formatInt(span.last)}`}
        tone={trend.tone}
        table={{
          head: ["Epoch", "ORAMA"],
          rows: supplySlots(points)
            .reverse()
            .map((slot) => ("point" in slot ? [formatInt(slot.point.epoch), formatNorama(trend.pick(slot.point))] : [gapEpochs(slot.gapFrom, slot.gapTo), NOT_AVAILABLE])),
        }}
      />
      {supplySlots(points).some((slot) => "gapFrom" in slot) && <p className="mt-2 text-[13px] text-muted">{SUPPLY_GAP_NOTE}</p>}
    </Card>
  );
}

/** Supply, circulation, stake and cumulative burn over the newest closed epochs. */
export function SupplyTrends({ points }: { points: readonly SupplyPoint[] }): ReactNode {
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {SUPPLY_TRENDS.map((trend) => (
        <SupplyTrendCard key={trend.title} trend={trend} points={points} />
      ))}
    </div>
  );
}

/** What each of the newest closed epochs burned. */
export function BurnTrend({ rows }: { rows: readonly EpochRow[] }) {
  if (rows.length === 0) {
    return <p className="py-4 text-center text-sm text-muted">No epoch has closed in this index yet, so there is no burn to chart.</p>;
  }
  const oldest = rows[rows.length - 1]!;
  const newest = rows[0]!;
  return (
    <div className="space-y-2">
      <SeriesChart
        label={`ORAMA burned in each of the newest ${rows.length} closed epochs`}
        kind="bars"
        values={epochSeries(rows, (r) => r.burned)}
        format={precise}
        from={`epoch ${formatInt(oldest.epoch)}`}
        to={`epoch ${formatInt(newest.epoch)}`}
        tone="loss"
        table={{
          head: ["Epoch", "Burned (ORAMA)", "Fees burned (ORAMA)", "Partial"],
          rows: rows.map((r) =>
            hasFigures(r)
              ? [formatInt(r.epoch), formatNorama(r.burned), formatNorama(r.feesBurned), r.partial ? "yes" : ""]
              : [formatInt(r.epoch), NOT_AVAILABLE, NOT_AVAILABLE, r.partial ? "yes" : ""],
          ),
        }}
      />
      {rows.some((r) => r.unavailable) && <UnavailableNote className="text-[13px] text-muted" />}
      <p className="text-[13px] text-muted">
        Burned is the chain's own count of everything it destroyed in the epoch. The base fee is burned, so transaction volume drives it. A partial
        epoch (the index began inside it) counts only the blocks the index saw.
      </p>
    </div>
  );
}
