import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import { parseAssetId } from "../model/lookup";
import type { NftRecord } from "../model/nfts";
import { shortHash } from "../model/units";
import { Card } from "../ui/card";
import { CopyChip } from "../ui/copy";
import { IndexQuery } from "../ui/index-query";
import { Page } from "../ui/page";
import { NotFoundBox, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { RecordCard } from "./nft/record-card";
import { TreeCard } from "./nft/tree-card";

const NOT_FOUND_TITLE = "NFT not found";

function Records({ id, records }: { id: string; records: readonly NftRecord[] }) {
  const trees = [...new Set(records.map((r) => r.treeId))];
  return (
    <>
      <header>
        <div className="text-[13px] text-muted">NFT</div>
        <h1 className="mt-1.5 flex flex-wrap items-center gap-2 font-display text-2xl font-semibold tracking-tight">
          <span className="font-mono text-lg [overflow-wrap:anywhere]">{shortHash(id)}</span>
          <CopyChip value={id} label="Copy the asset id" />
        </h1>
        <p className="mt-1 break-all font-mono text-xs text-muted">{id}</p>
      </header>
      {records.length > 1 && (
        <Card>
          <p className="text-sm text-muted">
            The chain does not make asset ids unique across mints, so this id has {records.length} records, each at its own place in a tree.
          </p>
        </Card>
      )}
      {records.map((r, i) => (
        <RecordCard key={`${r.treeId}/${r.leafIndex}`} record={r} title={records.length > 1 ? `Record ${i + 1}` : "Asset"} />
      ))}
      {trees.map((tree) => (
        <TreeCard key={tree} treeId={tree} />
      ))}
    </>
  );
}

function AssetLoader({ id }: { id: string }) {
  const { state, refetch } = useQuery((s) => s.assets.getNftRecords(id), [id]);
  useDocumentTitle(`NFT ${shortHash(id)}`);
  return (
    <IndexQuery state={state} subject={`NFT ${shortHash(id)}`} onRetry={refetch} loading={<Skeleton className="h-64 w-full" />}>
      {(records) =>
        records === null ? (
          <NotFoundBox
            title={NOT_FOUND_TITLE}
            hint={`The chain indexer has no asset ${shortHash(id)}. It may be a typo, an id that was never minted, or one minted before this node's index began.`}
          />
        ) : (
          <Records id={id} records={records} />
        )
      }
    </IndexQuery>
  );
}

/** `/explorer/nft/:id`, where the id is the 64 hexadecimal characters of the asset id. */
export function NftPage() {
  const id = parseAssetId(useParams().id ?? "");
  useDocumentTitle(id === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {id === null ? (
        <NotFoundBox title={NOT_FOUND_TITLE} hint="An asset id is 64 hexadecimal characters: the digits 0-9 and the letters a-f." />
      ) : (
        <AssetLoader key={id} id={id} />
      )}
    </Page>
  );
}
