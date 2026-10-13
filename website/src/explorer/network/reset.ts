/**
 * A reset gives a network a new chain id (orama-stagenet-5 becomes
 * orama-stagenet-6, networks/README.md) and starts it again at block 1, so
 * nothing a link names exists on the new chain. Three chain ids can disagree:
 * the one the registry lists for the network, the one its gateway reports now,
 * and the one a URL was made on (`?chain=`).
 */
export type ResetState =
  /** The URL was made on another chain than the one the gateway serves now: the page it names is not here. */
  | { kind: "link"; pinned: string; live: string }
  /** The registry and the gateway name different chains for the network: one of them has been reset and the other not caught up. */
  | { kind: "registry"; expected: string; live: string };

export interface ChainIds {
  /** From the registry; null for a custom gateway. */
  expected: string | null;
  /** From the gateway's status; null until the first read. */
  live: string | null;
  /** From the URL's `chain` parameter; null when absent. */
  pinned: string | null;
}

/** What, if anything, a reset means for this page. A link made on another chain takes precedence: its page cannot be shown. */
export function detectReset({ expected, live, pinned }: ChainIds): ResetState | null {
  if (live === null) return null;
  if (pinned !== null && pinned !== live) return { kind: "link", pinned, live };
  if (expected !== null && expected !== live) return { kind: "registry", expected, live };
  return null;
}
