import { Badge } from "../../ui/badge";
import { BlockLink } from "../../ui/links";
import { CopyChip } from "../../ui/copy";
import { Help } from "../../ui/help";
import { RelTime } from "../../ui/rel-time";
import { Sentence } from "../../ui/sentence";
import { formatUtc } from "../../model/time";
import { shortHash } from "../../model/units";
import type { TxDetail } from "../../model/types";
import { firstMessage, moreActionsText } from "./tx-messages";

const FINALITY_TIP =
  "Orama has instant finality: once a block is added, it is permanent. There is nothing to wait for.";
const NO_MESSAGE_TEXT = "This transaction carries no messages";
const MORE_ACTIONS_TIP = "This transaction does more than the sentence below says. The sentence describes the first action; every action is listed under Details.";

function StatusLine({ tx }: { tx: TxDetail }) {
  if (tx.status.ok) {
    return (
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Badge tone="ok">✓ Completed</Badge>
        <span className="text-[13px] text-muted">
          Final. This can&rsquo;t be reversed
          <Help tip={FINALITY_TIP} />
        </span>
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <Badge tone="bad">✕ Failed</Badge>
      <span className="text-[13px] text-muted">Failed: {tx.status.reason}. Nothing moved, but the fee was charged.</span>
    </div>
  );
}

/** The one-sentence verdict: what happened, whether it stuck, and where it lives on the chain. */
export function TxVerdict({ tx }: { tx: TxDetail }) {
  const message = firstMessage(tx.messages);
  const more = moreActionsText(tx.messages);
  return (
    <header className="flex flex-wrap items-start gap-4">
      <div className="min-w-0 flex-1 basis-64">
        <div className="flex flex-wrap items-center gap-2 text-[13px] text-muted">
          <span>Transaction</span>
          <span className="font-mono" title={tx.hash}>{shortHash(tx.hash)}</span>
          <CopyChip value={tx.hash} label="Copy transaction hash" withText />
        </div>
        <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1">
          <StatusLine tx={tx} />
          {more && (
            <span>
              <Badge tone="signal">{more}</Badge>
              <Help tip={MORE_ACTIONS_TIP} />
            </span>
          )}
        </div>
        <h1 className="mt-2.5 font-display text-[clamp(19px,3vw,26px)] font-semibold leading-tight tracking-tight">
          {message ? <Sentence message={message} failed={!tx.status.ok} /> : NO_MESSAGE_TEXT}
        </h1>
        <p className="mt-1 text-[13px] text-muted">
          In block <BlockLink height={tx.height} /> · <RelTime iso={tx.time} />{" "}
          <span className="tabular-nums">({formatUtc(tx.time)})</span>
        </p>
      </div>
      <div className="flex items-center gap-2 text-[13px] text-muted">
        <span>Share this page</span>
        <CopyChip value={window.location.href} label="Copy a link to this transaction" withText />
      </div>
    </header>
  );
}
