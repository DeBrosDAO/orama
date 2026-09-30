import type { WalletActivity } from "./use-wallet-activity";
import type { WalletFilter, WalletRef } from "../../model/types";
import { shortAddress } from "../../model/units";
import { Card } from "../../ui/card";
import { ChipGroup } from "../../ui/chips";
import { Skeleton } from "../../ui/query-states";
import { useNow } from "../../ui/use-now";
import { ActivityRow } from "./activity-row";
import { groupByDay } from "./activity-model";
import { ACTIVITY_SKELETON_ROWS, BUTTON_CLASS, FILTER_OPTIONS } from "./constants";
import { cn } from "../../../lib/utils";

export interface ActivityCardProps {
  activity: WalletActivity;
  filter: WalletFilter;
  onFilter: (filter: WalletFilter) => void;
  counterparty: WalletRef | null;
  onClearCounterparty: () => void;
  onClearAll: () => void;
}

function CounterpartyChip({ who, onClear }: { who: WalletRef; onClear: () => void }) {
  const name = who.label ?? shortAddress(who.address);
  return (
    <button
      type="button"
      onClick={onClear}
      aria-label={`Remove filter: only transactions with ${name}`}
      className={cn("inline-flex cursor-pointer items-center gap-1.5 rounded-full border border-signal/40 bg-surface-2 px-3 py-1 text-[13px] text-signal hover:border-signal")}
    >
      with {name} <span aria-hidden="true">✕</span>
    </button>
  );
}

function Rows({ activity, filtered, onClearAll }: { activity: WalletActivity; filtered: boolean; onClearAll: () => void }) {
  const now = useNow();
  if (activity.phase === "loading") {
    return (
      <div role="status" aria-label="Loading activity" className="space-y-1">
        {Array.from({ length: ACTIVITY_SKELETON_ROWS }, (_, i) => (
          <Skeleton key={i} className="h-14 w-full" />
        ))}
      </div>
    );
  }
  if (activity.phase === "error" && activity.items.length === 0) return null;
  if (activity.items.length === 0) {
    return (
      <div className="space-y-3 py-6 text-sm text-muted">
        <p>{filtered ? "No transactions match these filters." : "No activity yet."}</p>
        {filtered && (
          <button type="button" onClick={onClearAll} className={BUTTON_CLASS}>
            Clear filters
          </button>
        )}
      </div>
    );
  }
  return (
    <>
      {groupByDay(activity.items, now).map((group) => (
        <section key={group.day}>
          <h3 className="mt-3 border-b border-border pb-1 text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">{group.heading}</h3>
          <ul>
            {group.items.map((item, i) => (
              <ActivityRow key={`${item.hash}:${i}`} item={item} />
            ))}
          </ul>
        </section>
      ))}
    </>
  );
}

function Footer({ activity }: { activity: WalletActivity }) {
  const loadingMore = activity.phase === "loading-more";
  return (
    <div className="pt-3">
      {activity.phase === "error" && (
        <div role="alert" className="mb-2 text-sm text-loss">
          Could not load activity: {activity.error?.message}{" "}
          <button type="button" onClick={activity.retry} className={cn("underline cursor-pointer")}>
            Try again
          </button>
        </div>
      )}
      {activity.hasMore && activity.phase !== "error" && (
        <button type="button" onClick={activity.loadMore} disabled={loadingMore} aria-busy={loadingMore} className={BUTTON_CLASS}>
          {loadingMore ? "Loading…" : "Load older activity"}
        </button>
      )}
    </div>
  );
}

export function ActivityCard({ activity, filter, onFilter, counterparty, onClearCounterparty, onClearAll }: ActivityCardProps) {
  const filtered = filter !== "all" || counterparty !== null;
  return (
    <Card title="Activity">
      <div className="flex flex-wrap gap-2">
        <ChipGroup options={FILTER_OPTIONS} value={filter} onChange={onFilter} label="Filter activity" />
        {counterparty && <CounterpartyChip who={counterparty} onClear={onClearCounterparty} />}
      </div>
      <div aria-busy={activity.phase === "loading"}>
        <Rows activity={activity} filtered={filtered} onClearAll={onClearAll} />
      </div>
      <Footer activity={activity} />
    </Card>
  );
}
