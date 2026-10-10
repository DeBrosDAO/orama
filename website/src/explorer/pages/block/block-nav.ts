import { isTypingTarget } from "../../ui/dom";

const HEIGHT_PATTERN = /^-?[0-9]+$/;

/** The first block. There is no block 0 to step back to. */
export const FIRST_BLOCK = 1;

/** A block height from the URL, or null when the text is not a whole number. */
export function parseBlockHeight(raw: string | undefined): number | null {
  if (raw === undefined || !HEIGHT_PATTERN.test(raw)) return null;
  const n = Number(raw);
  return Number.isSafeInteger(n) ? n : null;
}

export interface BlockNeighbours {
  prev: number | null;
  next: number | null;
}

/** The blocks on either side of `height`. The head has no next; the head is unknown until it loads. */
export function blockNeighbours(height: number, head: number | null): BlockNeighbours {
  return {
    prev: height > FIRST_BLOCK ? height - 1 : null,
    next: head !== null && height < head ? height + 1 : null,
  };
}

/** What to tell a reader when there is no block to show. */
export function missingBlockHint(height: number | null, head: number | null): string {
  if (height === null) return "A block height is a whole number, like 1,284,410.";
  if (height < FIRST_BLOCK) return `Block heights start at ${FIRST_BLOCK}.`;
  if (head !== null && height > head) {
    return `The chain has not reached block ${height.toLocaleString("en-US")} yet. The latest block is ${head.toLocaleString("en-US")}.`;
  }
  return `There is no block ${height.toLocaleString("en-US")} on this chain.`;
}

export interface KeyEventLike {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  target: EventTarget | null;
}

/** The block a key press should go to, or null when it should be left alone. */
export function blockKeyTarget(e: KeyEventLike, nav: BlockNeighbours): number | null {
  if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return null;
  if (e.key === "ArrowLeft") return nav.prev;
  if (e.key === "ArrowRight") return nav.next;
  return null;
}
