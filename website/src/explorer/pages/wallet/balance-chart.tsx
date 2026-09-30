import { useMemo } from "react";
import type { BalancePoint, BalanceRange } from "../../model/types";
import { Amount } from "../../ui/amount";
import { balanceChartLabel, buildChartPaths } from "./chart";
import { BALANCE_FRACTION, CHART_SIZE } from "./constants";

export interface BalanceChartProps {
  points: BalancePoint[];
  range: BalanceRange;
  /** A newer range is on its way; the old line stays until it lands. */
  stale: boolean;
}

/** An area chart of the total balance. Its footprint never changes. */
export function BalanceChart({ points, range, stale }: BalanceChartProps) {
  const { width, height } = CHART_SIZE;
  const paths = useMemo(() => buildChartPaths(points, width, height), [points, width, height]);
  return (
    <div aria-busy={stale} className={stale ? "opacity-50 transition-opacity" : "transition-opacity"}>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        className="mt-3.5 block w-full text-fg"
        style={{ height }}
        role="img"
        aria-label={balanceChartLabel(points, range)}
      >
        <defs>
          <linearGradient id="wallet-balance-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="currentColor" stopOpacity="0.22" />
            <stop offset="1" stopColor="currentColor" stopOpacity="0" />
          </linearGradient>
        </defs>
        {paths.area && <path d={paths.area} fill="url(#wallet-balance-fill)" />}
        {paths.line && <path d={paths.line} fill="none" stroke="currentColor" strokeWidth="1.6" vectorEffect="non-scaling-stroke" />}
      </svg>
      {points.length === 0 ? (
        <p className="mt-1 text-xs text-muted">No balance history in this period.</p>
      ) : (
        <p className="mt-1 flex justify-between text-xs text-muted">
          <span>
            Low <Amount norama={paths.min} bare maxFraction={BALANCE_FRACTION} />
          </span>
          <span>
            High <Amount norama={paths.max} bare maxFraction={BALANCE_FRACTION} />
          </span>
        </p>
      )}
    </div>
  );
}
