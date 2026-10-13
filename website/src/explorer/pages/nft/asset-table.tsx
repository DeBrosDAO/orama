import { Link } from "react-router";
import type { NftRecord } from "../../model/nfts";
import { shortHash } from "../../model/units";
import { useExplorerPaths } from "../../network/active";
import { DataTable } from "../../ui/data-table";
import { StateBadge } from "./state-badge";

/** Assets, one row each, each opening its own page. */
export function AssetTable({ rows }: { rows: readonly NftRecord[] }) {
  const paths = useExplorerPaths();
  return (
    <DataTable
      caption="Assets"
      rows={rows}
      rowKey={(r) => `${r.id}/${r.treeId}/${r.leafIndex}`}
      columns={[
        {
          header: "Asset",
          cell: (r) => (
            <Link to={paths.assets.nft(r.id)} title={r.id} className="font-mono underline decoration-dotted underline-offset-4">
              {shortHash(r.id)}
            </Link>
          ),
        },
        { header: "State", cell: (r) => <StateBadge state={r.state} /> },
        {
          header: "Collection",
          cell: (r) =>
            r.collectionId === 0 ? (
              "None"
            ) : (
              <Link to={paths.assets.collection(r.collectionId)} className="underline decoration-dotted underline-offset-4">
                {r.collectionId}
              </Link>
            ),
        },
        { header: "Tree and leaf", cell: (r) => <span className="font-mono tabular-nums">{r.treeId} / {r.leafIndex}</span>, quiet: true },
      ]}
    />
  );
}
