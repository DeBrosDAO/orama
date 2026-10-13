import { Link } from "react-router";
import type { BundleKind, ShieldedTx } from "../../model/shielded";
import { shortHash } from "../../model/units";
import { useExplorerPaths } from "../../network/active";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { DataTable } from "../../ui/data-table";
import { BlockLink, WalletLink } from "../../ui/links";
import { RelTime } from "../../ui/rel-time";
import { FEE_FRACTION } from "./constants";

const KIND_TEXT: Record<BundleKind, string> = {
  transfer: "Private transfer",
  shield: "Shield",
  shield_earnings: "Shield earnings",
  unshield: "Unshield",
};
const AMOUNT_LABEL: Record<BundleKind, string> = {
  transfer: "fee",
  shield: "into the pool",
  shield_earnings: "into the pool",
  unshield: "out of the pool",
};

/** What a row's amount is: the fee of a private transfer, or the value that crossed the boundary. */
function AmountCell({ tx }: { tx: ShieldedTx }) {
  if (tx.bundle === null) return <span className="text-muted">nothing moved</span>;
  return (
    <span>
      <Amount norama={tx.bundle.amount} maxFraction={FEE_FRACTION} /> <span className="text-xs text-muted">{AMOUNT_LABEL[tx.bundle.kind]}</span>
    </span>
  );
}

/** The transactions that touched the pool. A private transfer has no signer and no amount but its fee; the others show who signed and what crossed. */
export function ActivityTable({ rows }: { rows: readonly ShieldedTx[] }) {
  const paths = useExplorerPaths();
  return (
    <DataTable
      caption="Transactions that touched the shielded pool, newest first"
      rows={rows}
      rowKey={(tx) => tx.hash}
      minWidth={640}
      columns={[
        {
          header: "Transaction",
          cell: (tx) => (
            <Link to={paths.tx(tx.hash)} title={tx.hash} className="font-mono underline decoration-dotted underline-offset-4">
              {shortHash(tx.hash)}
            </Link>
          ),
        },
        {
          header: "Kind",
          cell: (tx) =>
            tx.bundle === null ? <Badge tone="bad">Failed</Badge> : <Badge tone={tx.bundle.kind === "transfer" ? "signal" : "mute"}>{KIND_TEXT[tx.bundle.kind]}</Badge>,
        },
        { header: "Amount", cell: (tx) => <AmountCell tx={tx} /> },
        {
          header: "Signer",
          cell: (tx) => (tx.signer === null ? <span className="text-muted">none: private</span> : <WalletLink wallet={tx.signer} />),
        },
        { header: "Block", cell: (tx) => <BlockLink height={tx.height} />, quiet: true },
        { header: "When", cell: (tx) => <RelTime iso={tx.time} />, quiet: true },
      ]}
    />
  );
}
