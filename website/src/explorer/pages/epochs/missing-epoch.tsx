import { Link } from "react-router";
import { useQuery } from "../../data/use-query";
import { useExplorerPaths } from "../../network/active";
import { formatInt } from "../../model/units";
import { Card } from "../../ui/card";
import { Skeleton } from "../../ui/query-states";
import { EpochClockCard } from "./clock-card";
import { whyMissing } from "./logic";
import type { MissingEpoch } from "./logic";

const ADVICE: Record<MissingEpoch, (epoch: number) => string> = {
  open: (epoch) => `Epoch ${formatInt(epoch)} is the epoch in progress. It appears here the moment it closes.`,
  future: (epoch) => `Epoch ${formatInt(epoch)} has not started: the chain has not closed the epochs before it.`,
  unindexed: (epoch) =>
    `This index did not see epoch ${formatInt(epoch)} close. An index holds only the epochs that closed after it began, so earlier ones are not here.`,
};

/** For an epoch the index has no row for: say which of the three reasons it is, from the chain's current epoch. */
export function MissingEpochBox({ epoch }: { epoch: number }) {
  const explorerPaths = useExplorerPaths();
  const { state } = useQuery((s) => s.getEpochClock(), []);
  const why = state.status === "ready" ? whyMissing(epoch, state.data) : null;
  return (
    <>
      <Card>
        <div className="space-y-3 py-4 text-center">
          <h1 className="font-display text-2xl font-semibold">No record of epoch {formatInt(epoch)}</h1>
          {state.status === "loading" && <Skeleton className="mx-auto h-4 w-64" />}
          {why !== null && <p className="mx-auto max-w-prose text-sm text-muted">{ADVICE[why](epoch)}</p>}
          {state.status === "error" && (
            <p className="mx-auto max-w-prose text-sm text-muted">The index has no closed epoch with that number, and the chain could not be asked which epoch is current.</p>
          )}
          <Link to={explorerPaths.epochs} className="inline-block rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30">
            All epochs
          </Link>
        </div>
      </Card>
      {why === "open" && <EpochClockCard showLink={false} />}
    </>
  );
}
