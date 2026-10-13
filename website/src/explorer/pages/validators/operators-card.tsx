import { Link } from "react-router";
import type { QueryState } from "../../data/use-query";
import { useExplorerPaths } from "../../network/active";
import type { OperatorGroup, OperatorGroups, PowerCap } from "../../model/types";
import { formatPct } from "../../model/units";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Query, Skeleton } from "../../ui/query-states";
import { WalletLink } from "../../ui/links";
import { cn } from "../../../lib/utils";

const CAP_TIP =
  "One operator may run several validators. The cap applies to the operator's stake, summed over all of them, so splitting stake over more validators does not buy more power. Validators with no registered node share one operator, and so one cap.";

export function capSentence(cap: PowerCap): string {
  return `An operator's stake counts for at most ${formatPct(cap.current)} of the stake-backed power, however many validators it runs. The cap is ${formatPct(cap.normal)} while ${cap.stepDownCount} validators or fewer are active and ${formatPct(cap.reduced)} above that.`;
}

function Group({ group }: { group: OperatorGroup }) {
  const paths = useExplorerPaths();
  const count = group.validators.length;
  return (
    <li className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1 border-t border-border/60 py-3 first:border-t-0">
      <div className="min-w-0 flex-1 basis-64">
        <div className="text-sm font-medium">
          {group.operator.kind === "registered" ? (
            <WalletLink wallet={{ address: group.operator.address }} />
          ) : (
            <span title="Their consensus keys are not bound to a registered node (x/nodes), so they share one cap">No registered node</span>
          )}
          <span className="ml-2 text-xs font-normal text-muted">
            {count} validator{count === 1 ? "" : "s"}
          </span>
        </div>
        <ul className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[13px]">
          {group.validators.map((v) => (
            <li key={v.ref.operator}>
              <Link to={paths.validator(v.ref.operator)} className="underline decoration-dotted underline-offset-4 hover:text-fg">
                {v.ref.moniker}
              </Link>
            </li>
          ))}
        </ul>
      </div>
      <div className="text-right">
        <div className="font-mono text-sm tabular-nums">{formatPct(group.power)}</div>
        <div className="text-xs text-muted">of voting power</div>
      </div>
    </li>
  );
}

function Groups({ data, cap }: { data: OperatorGroups; cap: PowerCap }) {
  return (
    <>
      <p className="mb-2 max-w-[75ch] text-[13px] text-muted">
        {capSentence(cap)} The cap is on the stake-backed part of power, which is λ of it; the founding committee&apos;s seats carry the rest, so
        an operator&apos;s combined share below can be above the cap.
      </p>
      {data.groups.length === 0 ? (
        <p className="text-sm text-muted">No active validator has an operator to group.</p>
      ) : (
        <ul>
          {data.groups.map((g) => (
            <Group key={g.operator.kind === "registered" ? g.operator.address : "unlinked"} group={g} />
          ))}
        </ul>
      )}
      {data.unresolved > 0 && (
        <p role="note" className={cn("mt-2 text-[13px] text-muted")}>
          Operators are read one validator at a time, so only the largest are grouped here: {data.unresolved} smaller validator
          {data.unresolved === 1 ? " is" : "s are"} not.
        </p>
      )}
    </>
  );
}

/** Validators grouped by the operator x/power counts their stake under, and the cap that applies to each group. */
export function OperatorsCard({ state, onRetry, cap }: { state: QueryState<OperatorGroups>; onRetry: () => void; cap: PowerCap }) {
  return (
    <Card
      title={
        <>
          Operators and the power cap
          <Help tip={CAP_TIP} />
        </>
      }
    >
      <Query state={state} onRetry={onRetry} loading={<Skeleton className="h-24 w-full" />}>
        {(data) => <Groups data={data} cap={cap} />}
      </Query>
    </Card>
  );
}
