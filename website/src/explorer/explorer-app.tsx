import { useMemo } from "react";
import { Route, Routes } from "react-router";
import { ExplorerProvider } from "./data/provider";
import type { ExplorerDataSource } from "./data/source";
import { createChainSource } from "./data/chain/source";
import { ExplorerShell } from "./shell/explorer-shell";
import { PaletteProvider } from "./shell/palette";
import { PeekProvider } from "./shell/peek";
import { TrailProvider } from "./shell/trail";
import { ErrorBoundary } from "./ui/error-boundary";
import { NotFoundBox } from "./ui/query-states";
import { Page } from "./ui/page";
import { BlockPage } from "./pages/block";
import { HomePage } from "./pages/home";
import { TxPage } from "./pages/tx";
import { ValidatorsPage } from "./pages/validators";
import { WalletPage } from "./pages/wallet";

export interface ExplorerAppProps {
  /** Where the data comes from. Defaults to the chain the site is served with. */
  source?: ExplorerDataSource;
}

export function ExplorerApp({ source }: ExplorerAppProps) {
  const active = useMemo(() => source ?? createChainSource(), [source]);
  return (
    <ExplorerProvider source={active}>
      <TrailProvider>
        <PaletteProvider>
          <PeekProvider>
            <ExplorerShell>
              <ErrorBoundary>
              <Routes>
                <Route index element={<HomePage />} />
                <Route path="tx/:hash" element={<TxPage />} />
                <Route path="wallet/:address" element={<WalletPage />} />
                <Route path="block/:height" element={<BlockPage />} />
                <Route path="validators" element={<ValidatorsPage />} />
                <Route
                  path="*"
                  element={
                    <Page>
                      <NotFoundBox title="That page is not in the explorer" hint="Use the search box to find a wallet, transaction or block." />
                    </Page>
                  }
                />
              </Routes>
              </ErrorBoundary>
            </ExplorerShell>
          </PeekProvider>
        </PaletteProvider>
      </TrailProvider>
    </ExplorerProvider>
  );
}
