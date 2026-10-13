import { Link, useLocation } from "react-router";
import { useHead } from "../data/provider";
import { clean } from "../data/chain/text";
import { useNetwork } from "../network/active";
import { detectReset } from "../network/reset";
import type { ResetState } from "../network/reset";
import { pinnedChain } from "../network/selection";
import { Card } from "../ui/card";
import { Page } from "../ui/page";

const MAX_CHAIN_ID = 64;

/** What a reset means for the page being shown, from the registry, the gateway and the URL. Null while the gateway has not answered. */
export function useReset(): ResetState | null {
  const head = useHead();
  const { search } = useLocation();
  const { expectedChainId } = useNetwork();
  return detectReset({ expected: expectedChainId, live: head ? head.chainId : null, pinned: pinnedChain(search) });
}

const Id = ({ children }: { children: string }) => (
  <code className="rounded bg-surface-3 px-1.5 py-0.5 font-mono text-[0.9em]">{clean(children, MAX_CHAIN_ID)}</code>
);

/**
 * The registry and the gateway disagree about the network's chain: the network
 * has been reset and one side has not caught up. The page below is the chain
 * the gateway reports, and says so.
 */
export function ResetBanner({ state }: { state: Extract<ResetState, { kind: "registry" }> }) {
  const network = useNetwork();
  return (
    <div role="status" className="border-b border-loss/40 bg-loss/10 text-sm">
      <div className="mx-auto max-w-[1140px] space-y-1 px-4 py-3 sm:px-6">
        <p className="font-semibold text-loss">This network has been reset.</p>
        <p>
          The registry lists <Id>{state.expected}</Id> for {network.label}, but its gateway answers as <Id>{state.live}</Id>. What is shown
          below is the chain <Id>{state.live}</Id>. A reset starts the chain again from block 1, so links, transactions and balances of the
          other chain do not exist on this one.
        </p>
      </div>
    </div>
  );
}

/** A link made on one chain, opened where the gateway now serves another: nothing it names is here, so the page is not shown. */
export function LinkResetPage({ state }: { state: Extract<ResetState, { kind: "link" }> }) {
  const { paths, label } = useNetwork();
  return (
    <Page>
      <Card>
        <div role="alert" className="space-y-3 py-4">
          <h1 className="font-display text-2xl font-semibold">This network has been reset</h1>
          <p className="max-w-prose text-sm text-muted">
            This link was made on the chain <Id>{state.pinned}</Id>. {label === "Custom gateway" ? "This gateway" : `The ${label} network`} now
            runs <Id>{state.live}</Id>, a new chain that started again from block 1. The transaction, wallet or block in the link is not on
            it, so nothing is shown rather than something that looks like it.
          </p>
          <Link to={paths.home} className="inline-block rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30">
            Open the current chain
          </Link>
        </div>
      </Card>
    </Page>
  );
}
