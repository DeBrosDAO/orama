import { useQuery } from "../../data/use-query";
import { formatInt } from "../../model/units";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Query, Skeleton } from "../../ui/query-states";

const TIP_BASE_FEE =
  "Every transaction pays a base fee per unit of gas. The chain sets it from how full recent blocks are (x/fees), and the base-fee part of every fee is destroyed.";

/**
 * The base fee now (x/fees BaseFee). The gateway reads it for the latest blocks only (a query older than
 * 100 blocks is refused) and the indexer stores no per-block fee, so there is no history to draw and none is invented.
 */
export function BaseFeeCard() {
  const { state, refetch } = useQuery((s) => s.getBaseFee(), []);
  return (
    <Card title="Base fee">
      <Query state={state} onRetry={refetch} loading={<Skeleton className="h-20 w-full" />}>
        {(fee) => (
          <div className="space-y-2">
            <div className="text-2xl font-semibold tracking-tight tabular-nums">
              {formatInt(fee)} <span className="text-sm font-normal text-muted">norama per gas</span>
              <Help tip={TIP_BASE_FEE} />
            </div>
            <p className="text-[13px] text-muted">
              This is the value now. The public chain query serves it only for the latest blocks and the index does not record it, so there is no past series to show.
              The index records what the burn came to, epoch by epoch, instead.
            </p>
          </div>
        )}
      </Query>
    </Card>
  );
}
