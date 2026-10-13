import { useState } from "react";
import type { QueryState } from "../../data/use-query";
import { DEAL_CLASS_LABEL } from "../../model/storage";
import type { DealList, StorageDeal } from "../../model/storage";
import { formatInt } from "../../model/units";
import { cn } from "../../../lib/utils";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { ChipGroup } from "../../ui/chips";
import { WalletLink } from "../../ui/links";
import { Query, Skeleton } from "../../ui/query-states";
import { DealReplicas } from "./deal-replicas";
import { DEAL_FILTERS, dealTotals, filterDeals } from "./logic";
import type { DealFilter } from "./logic";

const HEAD_CELL = "pb-2 pr-3 font-medium";
const CELL = "py-2.5 pr-3 align-middle";

function DealRow({ deal, open, onToggle }: { deal: StorageDeal; open: boolean; onToggle: () => void }) {
  return (
    <>
      <tr className="border-t border-border/60 first:border-t-0 hover:bg-surface-2">
        <th scope="row" className={cn(CELL, "pl-2 text-left font-mono font-normal")}>
          <button type="button" onClick={onToggle} aria-expanded={open} className="cursor-pointer underline decoration-dotted underline-offset-4 hover:text-fg">
            #{deal.id}
          </button>
        </th>
        <td className={CELL}>
          <span className="flex flex-wrap gap-1">
            <Badge>{DEAL_CLASS_LABEL[deal.class]}</Badge>
            {deal.protocol && <Badge tone="signal">protocol</Badge>}
          </span>
        </td>
        <td className={CELL}>
          <Badge tone={deal.status === "active" ? "ok" : "mute"}>{deal.status}</Badge>
        </td>
        <td className={cn(CELL, "font-mono text-sm tabular-nums")}>{deal.replicas}</td>
        <td className={cn(CELL, "hidden text-sm sm:table-cell")}>
          <Amount norama={deal.pricePerEpoch} bare maxFraction={4} />
        </td>
        <td className={cn(CELL, "hidden font-mono text-sm tabular-nums md:table-cell")}>{formatInt(deal.durationEpochs)}</td>
        <td className={cn(CELL, "hidden lg:table-cell")}>
          <WalletLink wallet={{ address: deal.client }} bare />
        </td>
      </tr>
      {open && (
        <tr className="bg-surface-2/50">
          <td colSpan={7} className="pl-2">
            <DealReplicas deal={deal} />
          </td>
        </tr>
      )}
    </>
  );
}

function DealsBody({ list }: { list: DealList }) {
  const [filter, setFilter] = useState<DealFilter>("all");
  const [openId, setOpenId] = useState<number | null>(null);
  const totals = dealTotals(list.deals);
  const rows = filterDeals(list.deals, filter);
  if (list.deals.length === 0) return <p className="py-4 text-center text-sm text-muted">No storage deal has been made on this chain yet.</p>;
  return (
    <>
      <p className="mb-3 text-sm text-muted">
        {formatInt(totals.latestId ?? 0)} deals made so far. Of the newest {formatInt(totals.read)}, {formatInt(totals.running)} still run and{" "}
        {formatInt(totals.protocol)} were made by the protocol itself. Open a deal to see where its replicas are.
      </p>
      <ChipGroup options={DEAL_FILTERS} value={filter} onChange={setFilter} label="Filter deals" className="mb-3" />
      {rows.length === 0 ? (
        <p className="py-4 text-center text-sm text-muted">None of the newest deals match.</p>
      ) : (
        <table className="w-full border-collapse text-left">
          <caption className="sr-only">The newest storage deals: kind, state, replicas, price per epoch in ORAMA, length in epochs and the client</caption>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
              <th scope="col" className={cn(HEAD_CELL, "pl-2")}>Deal</th>
              <th scope="col" className={HEAD_CELL}>Kind</th>
              <th scope="col" className={HEAD_CELL}>State</th>
              <th scope="col" className={HEAD_CELL}>Replicas</th>
              <th scope="col" className={cn(HEAD_CELL, "hidden sm:table-cell")}>Price / epoch (ORAMA)</th>
              <th scope="col" className={cn(HEAD_CELL, "hidden md:table-cell")}>Epochs</th>
              <th scope="col" className={cn(HEAD_CELL, "hidden lg:table-cell")}>Client</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((deal) => (
              <DealRow key={deal.id} deal={deal} open={openId === deal.id} onToggle={() => setOpenId(openId === deal.id ? null : deal.id)} />
            ))}
          </tbody>
        </table>
      )}
      {list.hasOlder && <p className="mt-3 text-[13px] text-muted">Older deals exist; the explorer reads the newest {formatInt(totals.read)}.</p>}
    </>
  );
}

/** The newest storage deals; a deal opens to show its replicas and where they are held. */
export function DealsCard({ state, onRetry }: { state: QueryState<DealList>; onRetry: () => void }) {
  return (
    <Card title="Deals">
      <Query state={state} onRetry={onRetry} loading={<Skeleton className="h-64 w-full" />}>
        {(list) => <DealsBody list={list} />}
      </Query>
    </Card>
  );
}
