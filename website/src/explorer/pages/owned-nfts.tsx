import { useParams } from "react-router";
import { parseAccount } from "../model/lookup";
import type { NftRecord } from "../model/nfts";
import { shortAddress } from "../model/units";
import { Card } from "../ui/card";
import { IndexQuery } from "../ui/index-query";
import { LoadMore } from "../ui/load-more";
import { Page } from "../ui/page";
import { NotFoundBox, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { useKeyPaged } from "../ui/use-key-paged";
import type { KeyPaged } from "../ui/use-key-paged";
import { AssetTable } from "./nft/asset-table";

const PER_PAGE = 20;
const NOT_FOUND_TITLE = "Not an address";

/** The assets one address holds. More are read from the indexer's cursor on request, so a deep page costs what the first does. */
export function OwnedList({ paged }: { paged: KeyPaged<NftRecord> }) {
  if (paged.items.length === 0) {
    return <p className="py-6 text-center text-sm text-muted">The chain indexer lists no NFT held by this address.</p>;
  }
  return (
    <>
      <AssetTable rows={paged.items} />
      <LoadMore paged={paged} what="assets" />
    </>
  );
}

function OwnedLoader({ owner }: { owner: string }) {
  const paged = useKeyPaged((s, q) => s.assets.getOwnedNfts(owner, q), [owner], PER_PAGE);
  return (
    <>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">NFTs of {shortAddress(owner)}</h1>
        <p className="mt-1 break-all font-mono text-xs text-muted">{owner}</p>
      </div>
      <Card title="Assets held">
        <IndexQuery state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-48 w-full" />}>
          {() => <OwnedList paged={paged} />}
        </IndexQuery>
        <p className="mt-3 text-xs text-muted">Ownership is public: a compressed NFT's owner is a field of its leaf. The list is in asset id order, from the chain indexer.</p>
      </Card>
    </>
  );
}

/** `/explorer/nfts/:owner` */
export function OwnedNftsPage() {
  const owner = parseAccount(useParams().owner ?? "");
  useDocumentTitle(owner === null ? NOT_FOUND_TITLE : `NFTs of ${shortAddress(owner)}`);
  return (
    <Page>
      {owner === null ? (
        <NotFoundBox title={NOT_FOUND_TITLE} hint="An owner is an Orama wallet address, which starts with orama1." />
      ) : (
        <OwnedLoader key={owner} owner={owner} />
      )}
    </Page>
  );
}
