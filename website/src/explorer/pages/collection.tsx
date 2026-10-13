import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import { parseId } from "../model/lookup";
import type { Collection } from "../model/nfts";
import { royaltyPercent } from "../model/nfts";
import { Card } from "../ui/card";
import { Absent, Facts } from "../ui/facts";
import { WalletLink } from "../ui/links";
import { Page } from "../ui/page";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";

const NOT_FOUND_TITLE = "Collection not found";

function CollectionView({ collection }: { collection: Collection }) {
  return (
    <>
      <header>
        <div className="text-[13px] text-muted">Collection {collection.id}</div>
        <h1 className="mt-1.5 font-display text-3xl font-semibold tracking-tight">{collection.name === "" ? `Collection ${collection.id}` : collection.name}</h1>
      </header>
      <Card title="Collection">
        <Facts
          rows={[
            ["Creator", <WalletLink wallet={{ address: collection.creator }} />],
            ["Royalty on a sale", <span className="tabular-nums">{royaltyPercent(collection.royaltyBps)}</span>],
            ["Name", collection.name === "" ? <Absent>None written</Absent> : collection.name],
          ]}
        />
      </Card>
      <Card>
        <p className="max-w-[75ch] text-sm text-muted">
          A collection's assets sit in Merkle trees that its creator makes, and the chain does not list the trees or the assets of a collection. Open an
          asset to see its tree, or an owner's page to see what they hold.
        </p>
      </Card>
    </>
  );
}

function CollectionLoader({ id }: { id: number }) {
  const { state, refetch } = useQuery((s) => s.assets.getCollection(id), [id]);
  useDocumentTitle(state.status === "ready" && state.data && state.data.name !== "" ? state.data.name : `Collection ${id}`);
  return (
    <Query state={state} subject={`collection ${id}`} onRetry={refetch} loading={<Skeleton className="h-48 w-full" />}>
      {(collection) =>
        collection === null ? (
          <NotFoundBox title={NOT_FOUND_TITLE} hint={`x/cnft has no collection ${id}. Collections are numbered from 1 in the order they were made.`} />
        ) : (
          <CollectionView collection={collection} />
        )
      }
    </Query>
  );
}

/** `/explorer/collection/:id` */
export function CollectionPage() {
  const id = parseId(useParams().id ?? "");
  useDocumentTitle(id === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {id === null ? (
        <NotFoundBox title={NOT_FOUND_TITLE} hint="A collection is numbered from 1: /collection/1." />
      ) : (
        <CollectionLoader key={id} id={id} />
      )}
    </Page>
  );
}
