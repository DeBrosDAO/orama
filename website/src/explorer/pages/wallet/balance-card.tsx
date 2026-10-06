import { useEffect, useState } from "react";
import { useQuery } from "../../data/use-query";
import type { BalancePoint, BalanceRange, WalletBalance } from "../../model/types";
import { parseNorama } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { ChipGroup } from "../../ui/chips";
import { ErrorBox, Skeleton } from "../../ui/query-states";
import { BalanceChart } from "./balance-chart";
import { BALANCE_FRACTION, CHART_SKELETON_HEIGHT, DEFAULT_RANGE, RANGE_OPTIONS } from "./constants";
import { SplitBar } from "./split-bar";

interface Shown {
  range: BalanceRange;
  points: BalancePoint[];
}

/** The newest history that has arrived, kept on screen while the next range loads. */
function useHistory(address: string, range: BalanceRange) {
  const { state, refetch } = useQuery((s) => s.getBalanceHistory(address, range), [address, range]);
  const [last, setLast] = useState<Shown | null>(null);
  useEffect(() => {
    if (state.status === "ready") setLast({ range, points: state.data });
  }, [state, range]);
  const shown: Shown | null = state.status === "ready" ? { range, points: state.data } : last;
  return { state, shown, refetch };
}

export function BalanceCard({ address, balance }: { address: string; balance: WalletBalance }) {
  const [range, setRange] = useState<BalanceRange>(DEFAULT_RANGE);
  const { state, shown, refetch } = useHistory(address, range);
  const claimable = parseNorama(balance.claimableRewards) > 0n;
  return (
    <Card title="Balance" action={<ChipGroup options={RANGE_OPTIONS} value={range} onChange={setRange} label="Balance range" className="gap-1.5" />}>
      <div className="flex flex-wrap items-baseline gap-x-2.5">
        <span className="text-[32px] font-semibold leading-tight tracking-tight">
          <Amount norama={balance.total} bare maxFraction={BALANCE_FRACTION} className="font-sans" />
        </span>
        <span className="text-sm text-muted">ORAMA total</span>
      </div>
      <SplitBar balance={balance} />
      {claimable && (
        <p className="mt-2 text-sm text-signal">
          <Amount norama={balance.claimableRewards} maxFraction={BALANCE_FRACTION} /> in rewards ready to claim
        </p>
      )}
      {state.status === "error" && (
        <div className="mt-3.5">
          <ErrorBox message={state.error.message} onRetry={refetch} />
        </div>
      )}
      {state.status !== "error" && shown === null && (
        <div className="mt-3.5" style={{ height: CHART_SKELETON_HEIGHT }}>
          <Skeleton className="h-full w-full" />
        </div>
      )}
      {state.status !== "error" && shown !== null && (
        <BalanceChart points={shown.points} range={shown.range} stale={state.status === "loading"} />
      )}
    </Card>
  );
}
