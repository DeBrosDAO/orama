import { Card } from "../../ui/card";
import { Skeleton } from "../../ui/query-states";

const STAT_CARDS = ["time", "proposer", "transactions"] as const;

/** Same footprint as the loaded page: header, three stat cards, signatures, list. */
export function BlockSkeleton() {
  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Skeleton className="h-8 w-56" />
        <Skeleton className="h-4 w-64" />
      </div>
      <div className="grid gap-4 sm:grid-cols-3">
        {STAT_CARDS.map((k) => (
          <Card key={k}><Skeleton className="h-16 w-full" /></Card>
        ))}
      </div>
      <Card><Skeleton className="h-6 w-full" /></Card>
      <Card><Skeleton className="h-56 w-full" /></Card>
    </div>
  );
}
