import type { Block } from "../../model/types";
import { Card } from "../../ui/card";
import { TxRow } from "../../ui/tx-row";

const EMPTY_TEXT =
  "No transactions in this block. Empty blocks are normal: the chain keeps producing blocks even when nobody is transacting.";

/** Every transaction in the block, or a calm explanation that there are none. */
export function BlockTxs({ txs }: { txs: Block["txs"] }) {
  return (
    <Card title="Transactions in this block">
      {txs.length === 0 ? (
        <p className="py-4 text-sm text-muted">{EMPTY_TEXT}</p>
      ) : (
        <div>
          {txs.map((tx) => (
            <TxRow key={tx.hash} tx={tx} />
          ))}
        </div>
      )}
    </Card>
  );
}
