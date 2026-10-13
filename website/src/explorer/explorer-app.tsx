import { useMemo } from "react";
import { Route, Routes, useLocation } from "react-router";
import { ExplorerProvider } from "./data/provider";
import type { ExplorerDataSource } from "./data/source";
import { chainBase } from "./data/chain/client";
import { createChainSource } from "./data/chain/source";
import { NetworkProvider } from "./network/active";
import { isConfirmed } from "./network/gate";
import { REGISTRY } from "./network/registry";
import type { Registry } from "./network/registry";
import { selectNetwork } from "./network/selection";
import { ExplorerShell } from "./shell/explorer-shell";
import { PaletteProvider } from "./shell/palette";
import { PeekProvider } from "./shell/peek";
import { ConfirmGateway, NeedsGateway } from "./shell/standalone";
import { TrailProvider } from "./shell/trail";
import { ErrorBoundary } from "./ui/error-boundary";
import { NotFoundBox } from "./ui/query-states";
import { Page } from "./ui/page";
import { useDocumentTitle } from "./ui/use-document-title";
import { ASSET_ROUTES } from "./pages/asset-routes";
import { BlockPage } from "./pages/block";
import { EconomicsPage } from "./pages/economics";
import { EmissionPage } from "./pages/emission";
import { EpochPage } from "./pages/epoch";
import { EpochsPage } from "./pages/epochs";
import { HomePage } from "./pages/home";
import { RewardsPage } from "./pages/rewards";
import { NodePage } from "./pages/node";
import { NodesPage } from "./pages/nodes";
import { StoragePage } from "./pages/storage";
import { TorPage } from "./pages/tor";
import { TxPage } from "./pages/tx";
import { ValidatorPage } from "./pages/validator";
import { ValidatorsPage } from "./pages/validators";
import { WalletPage } from "./pages/wallet";

export interface ExplorerAppProps {
  /** Where the data comes from, for every network. Defaults to the gateway of the network the URL names. */
  source?: ExplorerDataSource;
  /** The networks the switcher offers. Defaults to the repository's networks/ directory. */
  registry?: Registry;
}

function NotInExplorer() {
  useDocumentTitle("Page not found");
  return (
    <Page>
      <NotFoundBox title="That page is not in the explorer" hint="Use the search box to find a wallet, transaction or block." />
    </Page>
  );
}

/** The explorer's pages, routed by the URL without its network segment. */
function Pages({ routePath }: { routePath: string }) {
  return (
    <ErrorBoundary>
      <Routes location={routePath}>
        <Route index element={<HomePage />} />
        <Route path="tx/:hash" element={<TxPage />} />
        <Route path="wallet/:address" element={<WalletPage />} />
        <Route path="block/:height" element={<BlockPage />} />
        <Route path="validators" element={<ValidatorsPage />} />
        <Route path="epochs" element={<EpochsPage />} />
        <Route path="epoch/:epoch" element={<EpochPage />} />
        <Route path="emission" element={<EmissionPage />} />
        <Route path="economics" element={<EconomicsPage />} />
        <Route path="rewards" element={<RewardsPage />} />
        <Route path="nodes" element={<NodesPage />} />
        <Route path="node/:id" element={<NodePage />} />
        <Route path="storage" element={<StoragePage />} />
        <Route path="tor" element={<TorPage />} />
        <Route path="validator/:address" element={<ValidatorPage />} />
        {ASSET_ROUTES}
        <Route path="*" element={<NotInExplorer />} />
      </Routes>
    </ErrorBoundary>
  );
}

export function ExplorerApp({ source, registry = REGISTRY }: ExplorerAppProps) {
  const location = useLocation();
  const selection = useMemo(
    () => selectNetwork(location.pathname, location.search, registry),
    [location.pathname, location.search, registry],
  );
  const network = useMemo(() => ({ ...selection, registry }), [selection, registry]);
  const { gateway, label } = selection;
  const active = useMemo(() => {
    if (source) return source;
    if (gateway === null) return null;
    return createChainSource({ base: chainBase(gateway), label: `${label} at ${new URL(gateway).host}` });
  }, [source, gateway, label]);

  let body;
  if (active === null) {
    body = <NeedsGateway />;
  } else if (selection.custom && !source && gateway !== null && !isConfirmed(gateway)) {
    body = <ConfirmGateway gateway={gateway} />;
  } else {
    body = (
      <ExplorerProvider source={active}>
        {/* A trail follows one network: another network is another chain, whose pages the trail's links do not name. */}
        <TrailProvider key={`${selection.key}@${gateway ?? ""}`}>
          <PaletteProvider>
            <PeekProvider>
              <ExplorerShell>
                <Pages routePath={selection.routePath} />
              </ExplorerShell>
            </PeekProvider>
          </PaletteProvider>
        </TrailProvider>
      </ExplorerProvider>
    );
  }
  return <NetworkProvider value={network}>{body}</NetworkProvider>;
}
