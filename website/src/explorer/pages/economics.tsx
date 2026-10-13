import { Link } from "react-router";
import { useQuery } from "../data/use-query";
import type { SupplyPoint } from "../model/epochs";
import { explorerPaths } from "../model/routes";
import { Card } from "../ui/card";
import { IndexQuery, isNoIndexer } from "../ui/index-query";
import { Page } from "../ui/page";
import { Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { BaseFeeCard } from "./economics/base-fee-card";
import { AsOf, CompositionCard, SupplyTiles } from "./economics/supply-cards";
import { BurnTrend, SupplyTrends } from "./economics/trend-cards";

/** The newest closed epochs the charts draw: the most the indexer returns in one page. */
const CHART_EPOCHS = 100;

function SupplyLoading() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-28 w-full" />
      <Skeleton className="h-64 w-full" />
    </div>
  );
}

function SupplyBody({ points }: { points: readonly SupplyPoint[] }) {
  const latest = points[0];
  if (latest === undefined) {
    return (
      <Card>
        <p className="py-6 text-center text-sm text-muted">
          No epoch has closed in this index yet, and supply is recorded once per closed epoch. Come back after the first one.
        </p>
      </Card>
    );
  }
  return (
    <>
      <AsOf point={latest} />
      <SupplyTiles point={latest} />
      <CompositionCard point={latest} />
      <SupplyTrends points={points} />
    </>
  );
}

export function EconomicsPage() {
  useDocumentTitle("Supply");
  const supply = useQuery((s) => s.getSupplyHistory(CHART_EPOCHS), []);
  const epochs = useQuery((s) => s.getEpochs({ page: 1, limit: CHART_EPOCHS }), []);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Supply and economics</h1>
        <p className="mt-1 max-w-prose text-sm text-muted">
          How much ORAMA exists, where it sits, and what has been created and destroyed. New ORAMA follows the{" "}
          <Link to={explorerPaths.emission} className="underline decoration-dotted underline-offset-4 hover:text-fg">
            emission schedule
          </Link>
          ; each epoch's figures are on its{" "}
          <Link to={explorerPaths.epochs} className="underline decoration-dotted underline-offset-4 hover:text-fg">
            epoch page
          </Link>
          .
        </p>
      </div>
      <IndexQuery state={supply.state} onRetry={supply.refetch} loading={<SupplyLoading />}>
        {(points) => <SupplyBody points={points} />}
      </IndexQuery>
      <BaseFeeCard />
      {!isNoIndexer(supply.state) && (
        <Card title="Burned per epoch">
          <IndexQuery state={epochs.state} onRetry={epochs.refetch} loading={<Skeleton className="h-52 w-full" />}>
            {(page) => <BurnTrend rows={page.items} />}
          </IndexQuery>
        </Card>
      )}
    </Page>
  );
}
