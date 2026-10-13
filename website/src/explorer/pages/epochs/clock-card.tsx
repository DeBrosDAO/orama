import { Link } from "react-router";
import { useEffect } from "react";
import { useHead } from "../../data/provider";
import { useQuery } from "../../data/use-query";
import type { QueryState } from "../../data/use-query";
import type { EpochClock } from "../../model/epochs";
import { useExplorerPaths } from "../../network/active";
import { epochCountdown, formatSpan } from "../../model/schedule";
import { formatInt, formatPct } from "../../model/units";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Query, Skeleton } from "../../ui/query-states";
import { useSecond } from "../../ui/use-second";

/** An epoch whose two conditions both hold closes in the next block: read the clock again this often until it has. */
const DUE_RECHECK_MS = 5000;

const TIP_CLOSE =
  "An epoch closes when BOTH have passed: its minimum time and its minimum number of blocks. Whichever comes last decides. The chain never catches up on missed epochs.";

function ClockBody({ clock, refetch }: { clock: EpochClock; refetch: () => void }) {
  const now = useSecond();
  const head = useHead();
  const c = epochCountdown(clock, now, head?.height ?? null);

  useEffect(() => {
    if (!c.due) return;
    const timer = setInterval(refetch, DUE_RECHECK_MS);
    return () => clearInterval(timer);
  }, [c.due, refetch]);

  const pct = Math.floor(c.progress * 100);
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <div className="text-2xl font-semibold tracking-tight tabular-nums">Epoch {formatInt(clock.epoch)}</div>
        <div className="text-sm text-muted tabular-nums">
          {c.due ? "closing with the next block" : `${formatPct(c.progress)} of the way to closing`}
        </div>
      </div>
      <div
        role="progressbar"
        aria-label={`Epoch ${clock.epoch} progress toward closing`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
        className="h-1.5 overflow-hidden rounded-full bg-surface-3"
      >
        <div className="h-full rounded-full bg-fg/60" style={{ width: `${pct}%` }} />
      </div>
      <dl className="grid gap-3 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-muted">Minimum time left</dt>
          <dd className="font-mono tabular-nums">
            {formatSpan(c.timeLeftMs)}
            <span className="text-muted"> of {formatSpan(clock.durationSeconds * 1000)}</span>
          </dd>
        </div>
        <div>
          <dt className="text-muted">Minimum blocks left</dt>
          <dd className="font-mono tabular-nums">
            {formatInt(c.blocksLeft)}
            <span className="text-muted"> of {formatInt(clock.minBlocks)}</span>
          </dd>
        </div>
      </dl>
      <p className="text-[13px] text-muted">
        The epoch closes when both are done
        <Help tip={TIP_CLOSE} />. It began{" "}
        <time dateTime={clock.startedAt}>{clock.startedAt.slice(0, 16).replace("T", " ")} UTC</time>.
      </p>
    </div>
  );
}

/** The card around an epoch-clock query the page already runs. */
export function ClockPanel({ state, refetch, showLink = true }: { state: QueryState<EpochClock>; refetch: () => void; showLink?: boolean }) {
  const explorerPaths = useExplorerPaths();
  return (
    <Card
      title="Current epoch"
      action={
        showLink ? (
          <Link to={explorerPaths.emission} className="text-xs text-muted hover:text-fg">
            Emission schedule
          </Link>
        ) : undefined
      }
    >
      <Query state={state} onRetry={refetch} loading={<Skeleton className="h-32 w-full" />}>
        {(clock) => <ClockBody clock={clock} refetch={refetch} />}
      </Query>
    </Card>
  );
}

/** The epoch in progress: how long and how many blocks remain before it can close. Reads the chain, not the index. */
export function EpochClockCard({ showLink = true }: { showLink?: boolean }) {
  const { state, refetch } = useQuery((s) => s.getEpochClock(), []);
  return <ClockPanel state={state} refetch={refetch} showLink={showLink} />;
}
