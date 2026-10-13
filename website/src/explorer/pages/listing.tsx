import { Link, useParams } from "react-router";
import { useQuery } from "../data/use-query";
import { parseId } from "../model/lookup";
import type { Listing } from "../model/market";
import { royaltyPercent } from "../model/nfts";
import { shortHash } from "../model/units";
import { useExplorerPaths } from "../network/active";
import { Amount } from "../ui/amount";
import { Card } from "../ui/card";
import { Absent, Facts } from "../ui/facts";
import { WalletLink } from "../ui/links";
import { Page } from "../ui/page";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { BidLookup } from "./listing/bid-lookup";

const NOT_FOUND_TITLE = "Listing not found";

function ListingView({ listing }: { listing: Listing }) {
  const paths = useExplorerPaths();
  return (
    <>
      <header>
        <div className="text-[13px] text-muted">Listing {listing.id}</div>
        <h1 className="mt-1.5 font-display text-3xl font-semibold tracking-tight">
          For sale: <Amount norama={listing.price} />
        </h1>
      </header>
      <Card title="Listing">
        <Facts
          rows={[
            ["Seller", <WalletLink wallet={{ address: listing.seller }} />],
            [
              "NFT",
              <Link to={paths.assets.nft(listing.assetId)} title={listing.assetId} className="font-mono underline decoration-dotted underline-offset-4">
                {shortHash(listing.assetId)}
              </Link>,
            ],
            ["Tree and leaf", <span className="font-mono">tree {listing.treeId}, leaf {listing.leafIndex}</span>],
            [
              "Collection",
              listing.collectionId === 0 ? (
                <Absent>None</Absent>
              ) : (
                <Link to={paths.assets.collection(listing.collectionId)} className="underline decoration-dotted underline-offset-4">
                  Collection {listing.collectionId}
                </Link>
              ),
            ],
            [
              "Royalty",
              listing.royaltyCreator === null ? (
                <Absent>None paid</Absent>
              ) : (
                <>
                  {royaltyPercent(listing.royaltyBps)} to <WalletLink wallet={{ address: listing.royaltyCreator }} />
                </>
              ),
            ],
          ]}
        />
        <p className="mt-3 text-xs text-muted">The royalty was copied from the collection when the listing was made and is not read again when it settles.</p>
      </Card>
      <BidLookup listingId={listing.id} />
    </>
  );
}

function ListingLoader({ id }: { id: number }) {
  const { state, refetch } = useQuery((s) => s.assets.getListing(id), [id]);
  useDocumentTitle(`Listing ${id}`);
  return (
    <Query state={state} subject={`listing ${id}`} onRetry={refetch} loading={<Skeleton className="h-64 w-full" />}>
      {(listing) =>
        listing === null ? (
          <NotFoundBox
            title={NOT_FOUND_TITLE}
            hint={`The market has no open listing ${id}. It may never have been made, or it may have been cancelled or settled: the chain deletes a listing then.`}
          />
        ) : (
          <ListingView listing={listing} />
        )
      }
    </Query>
  );
}

/** `/explorer/listing/:id` */
export function ListingPage() {
  const id = parseId(useParams().id ?? "");
  useDocumentTitle(id === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {id === null ? <NotFoundBox title={NOT_FOUND_TITLE} hint="A listing is numbered from 1: /listing/1." /> : <ListingLoader key={id} id={id} />}
    </Page>
  );
}
