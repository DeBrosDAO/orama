import type { Head } from "../model/types";

/**
 * The head to keep after `incoming` arrives. Within one chain the head only
 * moves forward: a slow or reordered answer that is behind what is already
 * known is ignored, and an announcement for the same height keeps the existing
 * object so nothing re-renders for it. A head of another chain id is a reset
 * (the network starts again at a low height), and replaces whatever was known.
 */
export function nextHead(prev: Head | null, incoming: Head): Head {
  if (prev === null || incoming.chainId !== prev.chainId) return incoming;
  return incoming.height <= prev.height ? prev : incoming;
}
