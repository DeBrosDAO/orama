import { Link } from "react-router";
import type { NftRecord } from "../../model/nfts";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { CopyChip } from "../../ui/copy";
import { Absent, Facts } from "../../ui/facts";
import { BlockLink, WalletLink } from "../../ui/links";
import { StateBadge } from "./state-badge";

const OWNER_LABEL: Record<NftRecord["state"], string> = {
  compressed: "Owner",
  decompressed: "Owner of the on-chain handle",
  burned: "Last owner",
};

/** One record of an asset: who holds it and where it sits in its tree. */
export function RecordCard({ record, title }: { record: NftRecord; title: string }) {
  const paths = useExplorerPaths();
  return (
    <Card title={title} action={<StateBadge state={record.state} />}>
      <Facts
        rows={[
          [OWNER_LABEL[record.state], <WalletLink wallet={{ address: record.owner }} />],
          ["Delegate", record.delegate === null ? <Absent>None</Absent> : <WalletLink wallet={{ address: record.delegate }} />],
          [
            "Collection",
            record.collectionId === 0 ? (
              <Absent>None</Absent>
            ) : (
              <Link to={paths.assets.collection(record.collectionId)} className="underline decoration-dotted underline-offset-4">
                Collection {record.collectionId}
              </Link>
            ),
          ],
          ["Tree and leaf", <span className="font-mono">tree {record.treeId}, leaf {record.leafIndex}</span>],
          [
            "Metadata content id",
            record.metadataCid === "" ? (
              <Absent>None</Absent>
            ) : (
              <span className="inline-flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs">{record.metadataCid}</span>
                <CopyChip value={record.metadataCid} label="Copy the metadata content id" />
              </span>
            ),
          ],
          ["Changes made", <span className="font-mono tabular-nums">{record.nonce}</span>],
          [
            "Last changed",
            <>
              in block <BlockLink height={record.updatedHeight} />,{" "}
              <Link to={paths.tx(record.updatedTx)} className="font-mono text-xs underline decoration-dotted underline-offset-4">
                transaction
              </Link>
            </>,
          ],
        ]}
      />
    </Card>
  );
}
