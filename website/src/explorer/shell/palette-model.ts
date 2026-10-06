import type { Hit } from "../model/search";
import { explorerPaths } from "../model/routes";
import type { ExampleTargets, WalletRef } from "../model/types";
import { shortAddress, shortHash } from "../model/units";

export const MAX_LABEL_RESULTS = 6;
/** A wallet or validator name is short; anything longer is not worth sending to the source. */
export const MAX_LABEL_QUERY_LENGTH = 64;
export const LABEL_SEARCH_DEBOUNCE_MS = 200;

export type RowIcon = "wallet" | "tx" | "hash" | "block" | "validators" | "named";

export interface PaletteRow {
  key: string;
  icon: RowIcon;
  /** The words of the row. */
  text: string;
  /** The identifier shown after the words in monospace; a named wallet always carries its address here. */
  code?: string;
  hint: string;
  to: string;
  /** Identicon seed for named wallets. */
  seed?: string;
}

export const DETECTED: Record<Hit["kind"], string> = {
  wallet: "Looks like a wallet address",
  tx: "Looks like a transaction hash",
  block: "Looks like a block height",
  "shielded-address": "Looks like a shielded address",
};

/** Names are only searched when the query is not already an exact identifier. */
export function shouldSearchLabels(query: string, hit: Hit | null): boolean {
  return query.trim() !== "" && hit === null;
}

/** The part of the query that is sent to the source for name search. */
export function labelQueryOf(trimmed: string): string {
  return trimmed.slice(0, MAX_LABEL_QUERY_LENGTH);
}

export function hitRow(hit: Hit): PaletteRow | null {
  switch (hit.kind) {
    case "wallet":
      return { key: "wallet", icon: "wallet", text: "Open wallet", code: shortAddress(hit.address), hint: "Wallet", to: explorerPaths.wallet(hit.address) };
    case "tx":
      return { key: "tx", icon: "tx", text: "Open transaction", code: shortHash(hit.hash), hint: "Transaction", to: explorerPaths.tx(hit.hash) };
    case "block":
      return { key: "block", icon: "block", text: "Open block", code: hit.height.toLocaleString("en-US"), hint: "Block", to: explorerPaths.block(hit.height) };
    case "shielded-address":
      return null;
  }
}

/** A name is never shown alone: the short address beside it is the defence against look-alike names. */
export function labelRows(labels: readonly WalletRef[]): PaletteRow[] {
  return labels.map((w) => ({
    key: `label-${w.address}`,
    icon: "named",
    text: w.label ?? "Wallet",
    code: shortAddress(w.address),
    hint: "Named wallet",
    to: explorerPaths.wallet(w.address),
    seed: w.address,
  }));
}

/** Shortcuts shown before anything is typed. An example the source has none of is left out. */
export function quickRows(examples: ExampleTargets | null): PaletteRow[] {
  const rows: PaletteRow[] = [];
  if (examples?.latestTxHash) {
    rows.push({ key: "ex-tx", icon: "hash", text: "The latest transfer", hint: "Transaction", to: explorerPaths.tx(examples.latestTxHash) });
  }
  if (examples?.busyWalletAddress) {
    rows.push({ key: "ex-wallet", icon: "wallet", text: "A busy wallet", hint: "Wallet", to: explorerPaths.wallet(examples.busyWalletAddress) });
  }
  rows.push({ key: "validators", icon: "validators", text: "Who runs the chain", hint: "Validators", to: explorerPaths.validators });
  return rows;
}

export interface RowInputs {
  trimmed: string;
  hit: Hit | null;
  /** Name matches for exactly the current query; empty while they are pending or unwanted. */
  labels: readonly WalletRef[];
  examples: ExampleTargets | null;
}

export function buildRows({ trimmed, hit, labels, examples }: RowInputs): PaletteRow[] {
  const rows: PaletteRow[] = [];
  const exact = hit ? hitRow(hit) : null;
  if (exact) rows.push(exact);
  rows.push(...labelRows(labels));
  if (trimmed === "") rows.push(...quickRows(examples));
  return rows;
}

/** Arrow-key movement, clamped to the list. An empty list keeps the selection at 0. */
export function moveSelection(current: number, delta: number, count: number): number {
  if (count === 0) return 0;
  return Math.min(count - 1, Math.max(0, current + delta));
}

export function optionId(listId: string, index: number): string {
  return `${listId}-option-${index}`;
}
