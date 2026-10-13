import { Link } from "react-router";
import { amountOf, describeMessage } from "../model/describe";
import { useExplorerPaths } from "../network/active";
import type { TxSummary } from "../model/types";
import { cn } from "../../lib/utils";
import { privacyOf } from "../model/shielded";
import { Amount } from "./amount";
import { moreMessagesBadge, txLinkName } from "./message-text";
import { PrivacyBadge } from "./privacy-badge";
import { RelTime } from "./rel-time";
import { Sentence } from "./sentence";
import { TxIcon } from "./tx-icon";

/**
 * One transaction in a chain-wide list (home feed, block page). The whole row
 * opens the transaction; the names inside it are their own links, so the row
 * uses a stretched overlay link instead of nesting anchors. A transaction
 * with several messages is described by its first; the badge says there are more.
 */
export function TxRow({ tx }: { tx: TxSummary }) {
  const paths = useExplorerPaths();
  const message = tx.messages[0];
  if (!message) return null;
  const failed = !tx.status.ok;
  const amount = amountOf(message);
  const more = moreMessagesBadge(tx.messages.length);
  return (
    <div className="group relative -mx-2 flex items-center gap-3 rounded-lg px-2 py-2.5 hover:bg-surface-2 transition-colors">
      <Link to={paths.tx(tx.hash)} aria-label={txLinkName(describeMessage(message, failed), tx.hash)} className="absolute inset-0 rounded-lg" />
      <TxIcon message={message} failed={failed} />
      <div className="pointer-events-none min-w-0 flex-1 text-sm">
        <Sentence message={message} failed={failed} />
        <div className="text-xs text-muted">
          {failed ? <span className="text-loss">Failed: {tx.status.ok ? "" : tx.status.reason}. Nothing moved.</span> : "Completed"}
          {more && <span className="ml-2 rounded bg-surface-3 px-1.5 py-0.5 text-[11px]">{more}</span>}
          <span className="ml-2 align-middle">
            <PrivacyBadge privacy={privacyOf(tx.messages)} />
          </span>
        </div>
      </div>
      <div className="pointer-events-none shrink-0 text-right">
        {amount !== null && (
          <div className={cn("text-[13px]", failed && "text-loss line-through")}>
            <Amount norama={amount} bare />
          </div>
        )}
        <RelTime iso={tx.time} className="text-xs text-muted" />
      </div>
    </div>
  );
}
