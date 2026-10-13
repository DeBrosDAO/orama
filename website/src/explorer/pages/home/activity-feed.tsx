import { useState } from "react";
import { useLiveQuery } from "../../data/use-query";
import type { ActivityFilter } from "../../model/types";
import { Card } from "../../ui/card";
import { ChipGroup } from "../../ui/chips";
import { Query, Skeleton } from "../../ui/query-states";
import { TxRow } from "../../ui/tx-row";
import { ACTIVITY_FILTERS } from "./logic";

const FEED_LIMIT = 12;
const SKELETON_ROW_COUNT = 6;
const EMPTY_TEXT = "Nothing here yet for this filter.";

function FeedSkeleton() {
  return (
    <div className="space-y-2">
      {Array.from({ length: SKELETON_ROW_COUNT }, (_, i) => (
        <Skeleton key={i} className="h-14 w-full" />
      ))}
    </div>
  );
}

/** The card and its filter chips stay mounted; only the list underneath swaps. */
export function ActivityFeed() {
  const [filter, setFilter] = useState<ActivityFilter>("all");
  const { state, refetch } = useLiveQuery((s) => s.getLatestActivity(filter, FEED_LIMIT), [filter]);
  return (
    <Card title="Live activity">
      <ChipGroup label="Filter activity" options={ACTIVITY_FILTERS} value={filter} onChange={setFilter} className="mb-2" />
      <Query state={state} loading={<FeedSkeleton />} onRetry={refetch}>
        {(txs) =>
          txs.length === 0 ? (
            <p className="py-5 text-sm text-muted">{EMPTY_TEXT}</p>
          ) : (
            <ul className="divide-y divide-border/50">
              {txs.map((tx) => (
                <li key={tx.hash}>
                  <TxRow tx={tx} />
                </li>
              ))}
            </ul>
          )
        }
      </Query>
    </Card>
  );
}
