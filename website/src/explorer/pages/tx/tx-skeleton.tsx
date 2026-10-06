import { Card } from "../../ui/card";
import { Skeleton } from "../../ui/query-states";

/** Same grid and card heights as the loaded page, so nothing jumps when data arrives. */
export function TxSkeleton() {
  return (
    <div className="space-y-4">
      <div className="space-y-3">
        <Skeleton className="h-4 w-64" />
        <Skeleton className="h-6 w-56" />
        <Skeleton className="h-8 w-full max-w-xl" />
        <Skeleton className="h-4 w-72" />
      </div>
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="space-y-4">
          <Card><Skeleton className="h-44 w-full" /></Card>
          <Card><Skeleton className="h-40 w-full" /></Card>
          <Card><Skeleton className="h-48 w-full" /></Card>
        </div>
        <div className="space-y-4">
          <Card><Skeleton className="h-52 w-full" /></Card>
          <Card><Skeleton className="h-24 w-full" /></Card>
        </div>
      </div>
    </div>
  );
}
