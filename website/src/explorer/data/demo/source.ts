import type { ExplorerDataSource } from "../source";
import { createView, chainReads, headOf, ok, subscribeToHead, walletReads } from "./source-reads";

const DEFAULT_SEED = 20260929;

export interface DemoSourceOptions {
  seed?: number;
  /** The clock the demo world follows. Tests pass a fixed one. */
  now?: () => number;
  /** Advance and announce the head on a timer. Off in tests and server rendering. */
  live?: boolean;
}

/**
 * A believable Orama-like chain that lives entirely in the browser: about
 * 20,000 transactions over 30 days with exact replayed balances, and a head
 * that keeps advancing. It exists so the explorer can be designed and used
 * before the real chain is reachable; swap it for the chain adapter by
 * passing a different ExplorerDataSource to <ExplorerProvider>.
 *
 * The head is a finished block: the block in progress is never shown, and no
 * transaction is generated for it until it is final.
 */
export function createDemoSource(opts: DemoSourceOptions = {}): ExplorerDataSource {
  const view = createView(opts.seed ?? DEFAULT_SEED, opts.now ?? Date.now);
  return {
    origin: { kind: "demo", label: "Demo data" },
    ...chainReads(view),
    ...walletReads(view),
    getHead: () => ok(() => headOf(view())),
    subscribeHead: (listener) => subscribeToHead(view, opts.live === true, listener),
  };
}
