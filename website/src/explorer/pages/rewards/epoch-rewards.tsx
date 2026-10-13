import { useSearchParams } from "react-router";
import { useQuery } from "../../data/use-query";
import type { QueryState } from "../../data/use-query";
import type { EpochRewards } from "../../model/rewards";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { IndexQuery } from "../../ui/index-query";
import { ValidatorLink } from "../../ui/links";
import { Skeleton } from "../../ui/query-states";
import { parsePageParam } from "../epochs/logic";
import { Pager } from "../epochs/pager";
import { operatorOwn } from "./logic";

const VALIDATORS_PER_PAGE = 25;
export const REWARD_PAGE_PARAM = "rpage";
const AMOUNT_FRACTION = 2;
const TH = "px-3 py-2 font-medium";
const TD = "px-3 py-2";

const TIP_BONDED = "Staked straight into a founding-committee validator's own stake instead of credited to its earnings; part of its own cut.";

export const NOT_RECORDED_EPOCH =
  "The chain did not report who was paid in this epoch (it closed before the chain did), so no per-validator figure is shown.";
export const NOTHING_PAID = "Nothing was paid to validators in this epoch.";

function Table({ data }: { data: EpochRewards }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[640px] text-left text-sm">
        <caption className="sr-only">What each validator was paid in this epoch</caption>
        <thead className="text-xs text-muted">
          <tr>
            <th scope="col" className={TH}>Validator</th>
            <th scope="col" className={`${TH} text-right`}>Paid (ORAMA)</th>
            <th scope="col" className={`${TH} text-right`}>Commission</th>
            <th scope="col" className={`${TH} text-right`}>Operator&apos;s own</th>
            <th scope="col" className={`${TH} text-right`}>Delegators</th>
            <th scope="col" className={`${TH} text-right`}>
              Staked
              <Help tip={TIP_BONDED} />
            </th>
          </tr>
        </thead>
        <tbody>
          {data.items.map((r) => (
            <tr key={r.ref.operator} className="border-t border-border">
              <td className={TD}>
                <ValidatorLink validator={r.ref} />
              </td>
              <td className={`${TD} text-right`}><Amount norama={r.amount} bare maxFraction={AMOUNT_FRACTION} /></td>
              <td className={`${TD} text-right`}><Amount norama={r.commission} bare maxFraction={AMOUNT_FRACTION} /></td>
              <td className={`${TD} text-right`}><Amount norama={operatorOwn(r)} bare maxFraction={AMOUNT_FRACTION} /></td>
              <td className={`${TD} text-right`}><Amount norama={r.delegators} bare maxFraction={AMOUNT_FRACTION} /></td>
              <td className={`${TD} text-right`}><Amount norama={r.forceBonded} bare maxFraction={AMOUNT_FRACTION} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** The ready state of one epoch's rewards: the table, or why there is none. */
export function EpochRewardsBody({ data, page }: { data: EpochRewards | null; page: number }) {
  if (data === null) return <p className="py-4 text-center text-sm text-muted">The index does not hold this epoch.</p>;
  if (!data.recorded) return <p className="py-4 text-sm text-muted">{NOT_RECORDED_EPOCH}</p>;
  if (data.items.length === 0) {
    return <p className="py-4 text-center text-sm text-muted">{page === 1 ? NOTHING_PAID : "There are no more validators for this epoch."}</p>;
  }
  return (
    <>
      <Table data={data} />
      <Pager param={REWARD_PAGE_PARAM} page={page} hasMore={data.hasMore} />
    </>
  );
}

export function EpochRewardsView({ state, page, onRetry }: { state: QueryState<EpochRewards | null>; page: number; onRetry: () => void }) {
  return (
    <Card title="Paid to each validator">
      <IndexQuery state={state} onRetry={onRetry} loading={<Skeleton className="h-40 w-full" />}>
        {(data) => <EpochRewardsBody data={data} page={page} />}
      </IndexQuery>
    </Card>
  );
}

/** What each validator was paid in one closed epoch. Its page lives in the URL as ?rpage=. */
export function EpochRewardsCard({ epoch }: { epoch: number }) {
  const [params] = useSearchParams();
  const page = parsePageParam(params.get(REWARD_PAGE_PARAM));
  const { state, refetch } = useQuery((s) => s.getEpochRewards(epoch, { page, limit: VALIDATORS_PER_PAGE }), [epoch, page]);
  return <EpochRewardsView state={state} page={page} onRetry={refetch} />;
}
