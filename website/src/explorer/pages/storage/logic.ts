import { DEAL_CLASSES, DEAL_CLASS_LABEL, isRunning } from "../../model/storage";
import type { StorageDeal } from "../../model/storage";

export type DealFilter = "all" | (typeof DEAL_CLASSES)[number] | "protocol";

export const DEAL_FILTERS: { id: DealFilter; label: string }[] = [
  { id: "all", label: "All" },
  ...DEAL_CLASSES.map((c) => ({ id: c as DealFilter, label: DEAL_CLASS_LABEL[c] })),
  { id: "protocol", label: "Made by the protocol" },
];

export function filterDeals(deals: readonly StorageDeal[], filter: DealFilter): StorageDeal[] {
  if (filter === "all") return [...deals];
  if (filter === "protocol") return deals.filter((d) => d.protocol);
  return deals.filter((d) => d.class === filter);
}

export interface DealTotals {
  read: number;
  running: number;
  protocol: number;
  /** The highest deal id read; deal ids count up from 1, so it is how many deals the chain has made. */
  latestId: number | null;
}

export function dealTotals(deals: readonly StorageDeal[]): DealTotals {
  return {
    read: deals.length,
    running: deals.filter(isRunning).length,
    protocol: deals.filter((d) => d.protocol).length,
    latestId: deals.reduce<number | null>((max, d) => (max === null || d.id > max ? d.id : max), null),
  };
}

/** How many of the deals named an operator to repair for the client. */
export function repairDelegates(deals: readonly StorageDeal[]): number {
  return deals.filter((d) => d.repairDelegate !== null).length;
}
