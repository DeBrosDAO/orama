import type { WalletFilter } from "../../model/types";
import type { ChipOption } from "../../ui/chips";

export const ACTIVITY_PAGE_SIZE = 20;
export const COUNTERPARTY_LIMIT = 5;
/** Digits after the point in balance figures; the hover title holds the exact amount. */
export const BALANCE_FRACTION = 4;
export const MAP_SIZE = { width: 310, height: 220 } as const;
export const ACTIVITY_SKELETON_ROWS = 5;
export const MAP_LABEL_MAX_CHARS = 10;

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
