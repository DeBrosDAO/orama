import { usePeek } from "../../shell/peek";
import type { ActivityItem } from "../../model/types";
import { Amount } from "../../ui/amount";
import { RelTime } from "../../ui/rel-time";
import { SentenceParts } from "../../ui/sentence";
import { TxIcon } from "../../ui/tx-icon";
import { cn } from "../../../lib/utils";
import { activityLabel, partsToText } from "./activity-model";
import { ROW_AMOUNT_FRACTION } from "./constants";

const ZERO = "0";

/**
 * One row of a wallet's history. The whole row is a button that opens the
 * transaction preview; names inside it are their own links, so the button is
 * a stretched overlay rather than a wrapper around them.
 */
export function ActivityRow({ item }: { item: ActivityItem }) {
  const { openTx } = usePeek();
  const failed = !item.status.ok;
  const parts = activityLabel(item);
  return (
    <li className="group relative -mx-2 flex items-center gap-3 rounded-lg px-2 py-2.5 transition-colors hover:bg-surface-2">
      <button
        type="button"
        aria-label={`Preview transaction: ${partsToText(parts)}`}
        onClick={() => openTx(item.hash)}
        className={cn("absolute inset-0 cursor-pointer rounded-lg")}
      />
      <TxIcon message={item.message} failed={failed} direction={item.direction} />
      <div className="pointer-events-none min-w-0 flex-1 text-sm leading-relaxed">
        <SentenceParts parts={parts} />
      </div>
      <div className="pointer-events-none shrink-0 text-right">
        {item.amount !== ZERO && (
          <div className={cn("text-[13px]", failed && "text-loss line-through")}>
            <Amount norama={item.amount} signed bare maxFraction={ROW_AMOUNT_FRACTION} />
          </div>
        )}
        <RelTime iso={item.time} className="text-xs text-muted/80" />
      </div>
    </li>
  );
}
