import { useSearchParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { IndexPage, EpochRow } from "../model/epochs";
import { Card } from "../ui/card";
import { IndexQuery } from "../ui/index-query";
import { Page } from "../ui/page";
import { Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { EpochClockCard } from "./epochs/clock-card";
import { EpochTable } from "./epochs/epoch-table";
import { JumpForm } from "./epochs/jump-form";
import { parsePageParam } from "./epochs/logic";
import { Pager } from "./epochs/pager";
import { UnavailableNote } from "./epochs/unavailable";

const EPOCHS_PER_PAGE = 20;
const PAGE_PARAM = "page";

function Loading() {
  return <Skeleton className="h-80 w-full" />;
}

function EpochList({ data, page }: { data: IndexPage<EpochRow>; page: number }) {
  if (data.items.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-muted">
        {page === 1 ? "No epoch has closed in this index yet." : "There are no older epochs in this index."}
      </p>
    );
  }
  return (
    <>
      <EpochTable rows={data.items} />
      <Pager param={PAGE_PARAM} page={page} hasMore={data.hasMore} />
      {data.items.some((row) => row.unavailable) && <UnavailableNote />}
    </>
  );
}

export function EpochsPage() {
  useDocumentTitle("Epochs");
  const [params] = useSearchParams();
  const page = parsePageParam(params.get(PAGE_PARAM));
  const { state, refetch } = useQuery((s) => s.getEpochs({ page, limit: EPOCHS_PER_PAGE }), [page]);
  return (
    <Page>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="font-display text-3xl font-semibold tracking-tight">Epochs</h1>
          <p className="mt-1 text-sm text-muted">
            New ORAMA is created once per epoch, up to a ceiling fixed in the chain. Each row is one closed epoch and what happened in its blocks.
          </p>
        </div>
        <JumpForm />
      </div>
      <EpochClockCard />
      <Card title="Closed epochs">
        <IndexQuery state={state} onRetry={refetch} loading={<Loading />}>
          {(data) => <EpochList data={data} page={page} />}
        </IndexQuery>
      </Card>
    </Page>
  );
}
