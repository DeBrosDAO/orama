import { listingTarget } from "../model/lookup";
import { useExplorerPaths } from "../network/active";
import { Card } from "../ui/card";
import { LookupForm } from "../ui/lookup-form";
import { ModuleActivity } from "../ui/module-activity";
import { Page } from "../ui/page";
import { useDocumentTitle } from "../ui/use-document-title";

/** `/explorer/market` */
export function MarketPage() {
  useDocumentTitle("Market");
  const paths = useExplorerPaths();
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Market</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          x/market sells compressed NFTs for a fixed price. A listing is open until the seller cancels it or a sale settles it; bidders escrow their
          coins in the market until their bid is accepted, cancelled or refunded.
        </p>
      </div>
      <Card title="Open a listing">
        <LookupForm
          label="Listing number"
          placeholder="Listing number"
          resolve={(text) => listingTarget(text, paths.assets)}
          hint="A listing is numbered from 1."
        />
        <p className="mt-2 max-w-[75ch] text-xs text-muted">
          The chain keeps no list of listings or bids. A listing that was cancelled or settled is deleted from the chain, so its number then opens
          nothing; the transaction that did it stays in the history.
        </p>
      </Card>
      <ModuleActivity title="Recent market activity" modules={["Market"]} empty="None of the newest transactions touched the market." />
    </Page>
  );
}
