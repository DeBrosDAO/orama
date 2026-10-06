import type { Head } from "../model/types";

/**
 * The head to keep after `incoming` arrives. The head only moves forward: a
 * slow or reordered answer that is behind what is already known is ignored,
 * and an announcement for the same height keeps the existing object so
 * nothing re-renders for it.
 */
export function nextHead(prev: Head | null, incoming: Head): Head {
  return prev !== null && incoming.height <= prev.height ? prev : incoming;
}
