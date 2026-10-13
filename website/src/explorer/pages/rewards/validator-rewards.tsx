import { Link } from "react-router";
import { useQuery } from "../../data/use-query";
import type { QueryState } from "../../data/use-query";
import type { ValidatorRewardHistory } from "../../model/rewards";
import { useExplorerPaths } from "../../network/active";
import { formatInt } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { IndexQuery } from "../../ui/index-query";
import { Skeleton } from "../../ui/query-states";
import { Stat } from "../../ui/stat";
import { operatorOwn, summarise } from "./logic";

const EPOCHS_SHOWN = 20;
const AMOUNT_FRACTION = 2;
const TH = "pb-2 pr-3 font-medium";

export const NOT_SEEN_REWARDS = "The chain indexer has not seen this validator yet, so it has no reward history.";
export const NO_EPOCHS = "No closed epoch yet.";
const SUMMARY_NOTE =
  "Over the recorded epochs listed. An epoch marked not recorded closed before the chain reported who was paid; it adds nothing here, and is not a zero.";

function Summary({ history }: { history: ValidatorRewardHistory }) {
  const total = summarise(history.items);
  if (total.epochs === 0) return null;
  return (
    <>
      <div className="mb-3 grid gap-4 sm:grid-cols-3">
        <Stat label={`Paid, ${total.epochs} recorded epoch${total.epochs === 1 ? "" : "s"}`} value={<Amount norama={total.paid} maxFraction={AMOUNT_FRACTION} />} />
        <Stat label="Of which commission" value={<Amount norama={total.commission} maxFraction={AMOUNT_FRACTION} />} />
        <Stat label="Of which delegators" value={<Amount norama={total.delegators} maxFraction={AMOUNT_FRACTION} />} />
      </div>
      <p className="mb-3 text-[13px] text-muted">{SUMMARY_NOTE}</p>
    </>
  );
}

function Rows({ history }: { history: ValidatorRewardHistory }) {
  const paths = useExplorerPaths();
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
        <caption className="sr-only">What this validator was paid in each closed epoch, newest first</caption>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
            <th scope="col" className={TH}>Epoch</th>
            <th scope="col" className={`${TH} text-right`}>Paid</th>
            <th scope="col" className={`${TH} text-right`}>Commission</th>
            <th scope="col" className={`${TH} text-right`}>Own</th>
            <th scope="col" className={`${TH} text-right`}>Delegators</th>
          </tr>
        </thead>
        <tbody>
          {history.items.map((item) => (
            <tr key={item.epoch} className="border-t border-border/60">
              <th scope="row" className="py-2 pr-3 text-left font-mono font-normal tabular-nums">
                <Link to={paths.epoch(item.epoch)} className="underline decoration-dotted underline-offset-4">
                  {formatInt(item.epoch)}
                </Link>
              </th>
              {!item.recorded ? (
                <td colSpan={4} className="py-2 pr-3 text-right text-muted">not recorded</td>
              ) : item.reward === null ? (
                <td colSpan={4} className="py-2 pr-3 text-right text-muted">nothing paid</td>
              ) : (
                <>
                  <td className="py-2 pr-3 text-right"><Amount norama={item.reward.amount} bare maxFraction={AMOUNT_FRACTION} /></td>
                  <td className="py-2 pr-3 text-right"><Amount norama={item.reward.commission} bare maxFraction={AMOUNT_FRACTION} /></td>
                  <td className="py-2 pr-3 text-right"><Amount norama={operatorOwn(item.reward)} bare maxFraction={AMOUNT_FRACTION} /></td>
                  <td className="py-2 pr-3 text-right"><Amount norama={item.reward.delegators} bare maxFraction={AMOUNT_FRACTION} /></td>
                </>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** The ready state: a summary of the recorded epochs and one row per closed epoch. */
export function ValidatorRewardsBody({ history }: { history: ValidatorRewardHistory | null }) {
  if (history === null) return <p className="text-sm text-muted">{NOT_SEEN_REWARDS}</p>;
  if (history.items.length === 0) return <p className="text-sm text-muted">{NO_EPOCHS}</p>;
  return (
    <>
      <Summary history={history} />
      <Rows history={history} />
    </>
  );
}

export function ValidatorRewardsView({ state, onRetry }: { state: QueryState<ValidatorRewardHistory | null>; onRetry: () => void }) {
  return (
    <Card title="Rewards, by epoch">
      <IndexQuery state={state} onRetry={onRetry} loading={<Skeleton className="h-48 w-full" />}>
        {(history) => <ValidatorRewardsBody history={history} />}
      </IndexQuery>
    </Card>
  );
}

/** What a validator was paid in its newest closed epochs, split into commission, its own cut and its delegators'. */
export function ValidatorRewardsCard({ address }: { address: string }) {
  const { state, refetch } = useQuery((s) => s.getValidatorRewards(address, { page: 1, limit: EPOCHS_SHOWN }), [address]);
  return <ValidatorRewardsView state={state} onRetry={refetch} />;
}
