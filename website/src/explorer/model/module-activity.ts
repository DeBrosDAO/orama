import type { TxSummary } from "./types";

/** How many of the newest transactions a module's recent activity is picked from: the most the chain indexer lists at once. */
export const ACTIVITY_WINDOW = 100;
/** The most rows a module's recent activity shows. */
export const ACTIVITY_ROWS = 12;

/**
 * The transactions that carry a message of one of the modules, in the order given. A module is the
 * name its decoder gives its messages: "Tokens", "Compressed NFTs", "Contracts", "Market", "Houses".
 */
export function moduleTxs(txs: readonly TxSummary[], modules: readonly string[]): TxSummary[] {
  return txs.filter((tx) => tx.messages.some((m) => m.type === "action" && modules.includes(m.module)));
}
