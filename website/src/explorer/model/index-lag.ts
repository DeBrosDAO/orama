import type { IndexStatus } from "./types";
import { formatInt } from "./units";

export type LagTone = "current" | "behind" | "stuck" | "empty";

export interface LagNote {
  tone: LagTone;
  text: string;
}

/**
 * What a screen reader is told about the indexer, apart from the visible line. The visible line carries
 * block numbers that change with every block, and a live region that repeats it would speak every two
 * seconds; this changes only when the indexer's state does. Nothing is said while it is current.
 */
export interface LagAnnouncements {
  /** Polite: the indexer is behind or has indexed nothing. */
  status: string;
  /** Assertive: the indexer cannot go on. */
  alert: string;
}

/**
 * How many blocks behind the chain the indexer may be before a screen reader is told. A healthy
 * indexer is a block or two behind the head as each block is indexed, and moves between 0, 1 and 2 with
 * every block; telling the reader each time it crossed from 0 to 1 would be the chatter this avoids.
 */
export const LAG_ANNOUNCE_BLOCKS = 5;

export function lagAnnouncements(index: IndexStatus): LagAnnouncements {
  if (index.stuck !== null) {
    return { status: "", alert: `The indexer is stuck at block ${formatInt(index.stuck.height)}; history stops at block ${formatInt(index.cursor)}.` };
  }
  if (index.cursor === 0) return { status: "The indexer has not indexed a block yet.", alert: "" };
  return { status: index.lag >= LAG_ANNOUNCE_BLOCKS ? "The indexed history is behind the chain." : "", alert: "" };
}

/**
 * What a reader should know about how current the history on a page is. The lists that come from the
 * chain indexer (transactions, epochs, rewards) end at its cursor; the chain's head may be ahead.
 * A stuck indexer is said so with its reason, because its lag then does not close by itself.
 */
export function lagNote(index: IndexStatus): LagNote {
  if (index.stuck !== null) {
    const why = index.stuck.statePruned
      ? "the node no longer keeps the chain state it needs there"
      : index.stuck.reason === ""
        ? "it has failed there repeatedly"
        : index.stuck.reason;
    return {
      tone: "stuck",
      text: `The indexer is stuck at block ${formatInt(index.stuck.height)}: ${why}. History stops at block ${formatInt(index.cursor)}, ${formatInt(index.lag)} behind the chain.`,
    };
  }
  if (index.cursor === 0) return { tone: "empty", text: "The indexer has not indexed a block yet, so the history on this page is empty." };
  if (index.lag === 0) return { tone: "current", text: `History is up to date at block ${formatInt(index.cursor)}.` };
  const blocks = index.lag === 1 ? "1 block" : `${formatInt(index.lag)} blocks`;
  return {
    tone: "behind",
    text: `History is ${blocks} behind the chain: indexed to block ${formatInt(index.cursor)}, the chain is at ${formatInt(index.tip)}.`,
  };
}
