import type { BalanceRange, WalletFilter } from "../../model/types";
import type { ChipOption } from "../../ui/chips";

export const ACTIVITY_PAGE_SIZE = 20;
export const COUNTERPARTY_LIMIT = 5;
/** Digits after the point in balance figures; the hover title holds the exact amount. */
export const BALANCE_FRACTION = 4;
export const DEFAULT_RANGE: BalanceRange = "30d";
export const CHART_SIZE = { width: 640, height: 110 } as const;
/** The chart plus its low/high caption, so the skeleton has the same footprint. */
export const CHART_SKELETON_HEIGHT = CHART_SIZE.height + 24;
export const MAP_SIZE = { width: 310, height: 220 } as const;
export const ACTIVITY_SKELETON_ROWS = 5;
export const MAP_LABEL_MAX_CHARS = 10;

export const RANGE_OPTIONS: readonly ChipOption<BalanceRange>[] = [
  { id: "7d", label: "7d" },
  { id: "30d", label: "30d" },
  { id: "all", label: "All" },
];

export const RANGE_WORDS: Record<BalanceRange, string> = {
  "7d": "the last 7 days",
  "30d": "the last 30 days",
  all: "all time",
};

export const FILTER_OPTIONS: readonly ChipOption<WalletFilter>[] = [
  { id: "all", label: "All" },
  { id: "in", label: "Received" },
  { id: "out", label: "Sent" },
  { id: "staking", label: "Staking" },
  { id: "storage", label: "Storage" },
  { id: "failed", label: "Failed" },
];

export const BUTTON_CLASS = `rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30 cursor-pointer disabled:cursor-not-allowed disabled:opacity-50`;

/** Activity rows show at most this many decimals; the exact value is in the hover title. */
export const ROW_AMOUNT_FRACTION = 4;
