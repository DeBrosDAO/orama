import { useQuery } from "../data/use-query";
import { Page } from "../ui/page";
import { Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { useKeyPaged } from "../ui/use-key-paged";
import { ActivityCard } from "./shielded/activity-card";
import { PoolStats, RulesCard } from "./shielded/pool-cards";
import { VisibilityCard } from "./shielded/visibility-card";

const PER_PAGE = 20;

/** `/explorer/shielded` */
export function ShieldedPage() {
  useDocumentTitle("Shielded pool");
  const pool = useQuery((s) => s.assets.getShieldedPool(), []);
  const total = useQuery((s) => s.assets.getShieldedActivityTotal(), []);
  const paged = useKeyPaged((s, q) => s.assets.getShieldedActivity(q), [], PER_PAGE);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Shielded pool</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          The private side of the chain. Value that enters the pool moves between notes with no visible sender, receiver or amount, and comes out
          again to its owner. Using the pool is optional.
        </p>
      </div>
      <VisibilityCard />
      <Query state={pool.state} onRetry={pool.refetch} loading={<Skeleton className="h-48 w-full" />}>
        {(p) => (
          <>
            <PoolStats pool={p} />
            <RulesCard pool={p} />
          </>
        )}
      </Query>
      <ActivityCard paged={paged} total={total.state.status === "ready" ? total.state.data : null} />
    </Page>
  );
}
