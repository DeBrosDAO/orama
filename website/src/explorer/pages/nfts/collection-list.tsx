import { Link } from "react-router";
import type { Collection } from "../../model/nfts";
import { royaltyPercent } from "../../model/nfts";
import type { IdPage } from "../../model/paging";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { DataTable } from "../../ui/data-table";
import { IdPager } from "../../ui/id-pager";
import { WalletLink } from "../../ui/links";

/** Collections, newest first. The chain numbers them 1, 2, 3 and has no list of them, so the newest id is found by asking. */
export function CollectionList({ page, atNewest }: { page: IdPage<Collection>; atNewest: boolean }) {
  const paths = useExplorerPaths();
  return (
    <Card title="Collections" action={<span className="text-xs text-muted">{page.newest} in all</span>}>
      {page.items.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted">{atNewest ? "No collection has been created on this chain yet." : "There is no collection at that number."}</p>
      ) : (
        <>
          <DataTable
            caption="Collections, newest first"
            rows={page.items}
            rowKey={(c) => String(c.id)}
            minWidth={480}
            columns={[
              {
                header: "Collection",
                cell: (c) => (
                  <Link to={paths.assets.collection(c.id)} className="font-medium underline decoration-dotted underline-offset-4">
                    {c.name === "" ? `Collection ${c.id}` : c.name}
                  </Link>
                ),
              },
              { header: "Number", cell: (c) => <span className="font-mono tabular-nums">{c.id}</span>, quiet: true },
              { header: "Creator", cell: (c) => <WalletLink wallet={{ address: c.creator }} /> },
              { header: "Royalty", align: "right", cell: (c) => <span className="tabular-nums">{royaltyPercent(c.royaltyBps)}</span> },
            ]}
          />
          <IdPager param="from" atNewest={atNewest} next={page.next} />
        </>
      )}
    </Card>
  );
}
