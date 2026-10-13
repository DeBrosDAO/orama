import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router";
import { useHead } from "../data/provider";
import { clean } from "../data/chain/text";
import { useNetwork } from "../network/active";
import { pinnedChain, withPinnedChain } from "../network/selection";
import { Badge } from "../ui/badge";
import { CopyChip } from "../ui/copy";
import { NetworkSwitcher } from "./network-switcher";

const MAX_CHAIN_ID = 64;

/** The chain id the page shows: the gateway's own once it has answered, the registry's until then. */
export function useShownChainId(): { id: string | null; live: boolean } {
  const head = useHead();
  const network = useNetwork();
  if (head) return { id: clean(head.chainId, MAX_CHAIN_ID), live: true };
  return { id: network.expectedChainId, live: false };
}

/**
 * Puts the chain id into the address bar once the gateway has named it, so a
 * link copied from it says which chain it was made on. It only adds a missing
 * (or malformed) `chain`; a link made on another chain keeps its own id and is
 * answered by the reset page. `replace`, so the back button is not trapped.
 */
function useChainPin() {
  const head = useHead();
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  useEffect(() => {
    if (!head || pinnedChain(search) !== null) return;
    navigate({ pathname, search: withPinnedChain(search, head.chainId) }, { replace: true });
  }, [head, pathname, search, navigate]);
}

/** The strip under the header: which network this is, its chain id, and where its data comes from. */
export function NetworkBar() {
  const network = useNetwork();
  const { id, live } = useShownChainId();
  useChainPin();
  return (
    <div role="group" aria-label="Network and chain" className="border-b border-border bg-surface/60 text-xs">
      <div className="mx-auto flex max-w-[1140px] flex-wrap items-center gap-x-5 gap-y-1.5 px-4 py-2 sm:px-6">
        <NetworkSwitcher />
        <span className="inline-flex items-center gap-2">
          <span className="text-muted">Chain</span>
          {id ? (
            <>
              <code
                className="rounded bg-surface-3 px-1.5 py-0.5 font-mono text-fg"
                title={live ? "The chain id this network's gateway reports" : "The chain id the registry lists, until the gateway answers"}
              >
                {id}
              </code>
              <CopyChip value={id} label={`Copy chain id ${id}`} />
            </>
          ) : (
            <span className="text-muted">not known yet</span>
          )}
        </span>
        {network.custom && (
          <Badge tone="signal" className="font-normal">
            Not in the Orama registry
          </Badge>
        )}
      </div>
    </div>
  );
}
