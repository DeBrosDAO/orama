import { useSearchParams } from "react-router";
import { useQuery } from "../data/use-query";
import { nftTarget, parseId } from "../model/lookup";
import { useExplorerPaths } from "../network/active";
import { Card } from "../ui/card";
import { LookupForm } from "../ui/lookup-form";
import { ModuleActivity } from "../ui/module-activity";
import { Page } from "../ui/page";
import { Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { CollectionList } from "./nfts/collection-list";

const COLLECTIONS_PER_PAGE = 10;
const FROM_PARAM = "from";

/** `/explorer/nfts` */
export function NftsPage() {
  useDocumentTitle("NFTs");
  const paths = useExplorerPaths();
  const [params] = useSearchParams();
  const from = parseId(params.get(FROM_PARAM) ?? "");
  const { state, refetch } = useQuery((s) => s.assets.getCollections(from, COLLECTIONS_PER_PAGE), [from]);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">NFTs</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          x/cnft keeps NFTs as compressed leaves of Merkle trees: the chain stores a tree's root, and the chain indexer keeps each asset, its
          owner and the content id of its metadata. The explorer shows those; it does not fetch the metadata document a content id names.
        </p>
      </div>
      <Card title="Find an NFT">
        <LookupForm
          label="Asset id, owner address or collection number"
          placeholder="Asset id (64 hex), owner orama1…, or collection number"
          resolve={(text) => nftTarget(text, paths.assets)}
          hint="Paste an asset id (64 hexadecimal characters), an owner's orama1… address or a collection's number."
        />
        <p className="mt-2 text-xs text-muted">The chain keeps no list of every asset, so an asset is found by its id or by who holds it.</p>
      </Card>
      <Query state={state} onRetry={refetch} loading={<Skeleton className="h-64 w-full" />}>
        {(page) => <CollectionList page={page} atNewest={from === null} />}
      </Query>
      <ModuleActivity title="Recent NFT activity" modules={["Compressed NFTs"]} empty="None of the newest transactions touched an NFT." />
    </Page>
  );
}
