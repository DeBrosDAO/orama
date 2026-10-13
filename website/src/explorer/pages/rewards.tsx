import { useSearchParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { EpochRow, IndexPage } from "../model/epochs";
import { Card } from "../ui/card";
import { IndexLagNote } from "../ui/index-lag";
import { IndexQuery } from "../ui/index-query";
import { Page } from "../ui/page";
import { Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { parsePageParam } from "./epochs/logic";
import { Pager } from "./epochs/pager";
import { UnavailableNote } from "./epochs/unavailable";
import { RewardsTable } from "./rewards/rewards-table";

const EPOCHS_PER_PAGE = 20;
const PAGE_PARAM = "page";

const INTRO =
  "Each epoch, the chain pays the validators' share of the new ORAMA to the validators and the people who staked with them, on their capped voting power. The operator takes its commission first; the rest is shared by stake, the operator's own stake included. Open an epoch to see what each validator was paid.";
const NOT_RECORDED_NOTE =
  "An epoch marked not recorded closed before the chain reported who was paid. How its payout split is not known, so no figure is shown for it.";

function Loading() {
  return <Skeleton className="h-80 w-full" />;
}

export function RewardList({ data, page }: { data: IndexPage<EpochRow>; page: number }) {
  if (data.items.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-muted">
        {page === 1 ? "No epoch has closed in this index yet." : "There are no older epochs in this index."}
      </p>
    );
  }
  return (
    <>
      <RewardsTable rows={data.items} />
      <Pager param={PAGE_PARAM} page={page} hasMore={data.hasMore} />
      {data.items.some((row) => !row.rewardsRecorded) && <p className="mt-3 text-[13px] text-muted">{NOT_RECORDED_NOTE}</p>}
      {data.items.some((row) => row.unavailable) && <UnavailableNote />}
    </>
  );
}

/** `/explorer/rewards`: what each closed epoch paid validators and delegators. */
export function RewardsPage() {
  useDocumentTitle("Rewards");
  const [params] = useSearchParams();
  const page = parsePageParam(params.get(PAGE_PARAM));
  const { state, refetch } = useQuery((s) => s.getEpochs({ page, limit: EPOCHS_PER_PAGE }), [page]);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Rewards</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">{INTRO}</p>
        <IndexLagNote className="mt-2" />
      </div>
      <Card title="Paid to validators and delegators, by epoch">
        <IndexQuery state={state} onRetry={refetch} loading={<Loading />}>
          {(data) => <RewardList data={data} page={page} />}
        </IndexQuery>
      </Card>
    </Page>
  );
}
