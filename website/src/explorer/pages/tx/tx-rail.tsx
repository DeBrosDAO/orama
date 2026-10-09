import { Link } from "react-router";
import { ArrowLeft, ArrowRight, LayoutGrid } from "lucide-react";
import { explorerPaths } from "../../model/routes";
import type { TxDetail, TxMessage, WalletRef } from "../../model/types";
import { shortAddress } from "../../model/units";
import { cn } from "../../../lib/utils";
import { Card } from "../../ui/card";
import { Identicon } from "../../ui/identicon";
import { Sentence } from "../../ui/sentence";
import { describeContext } from "./tx-context";
import { firstMessage } from "./tx-messages";

const ROW_IDENTICON_SIZE = 20;
const SMALL_ICON_SIZE = 12;
const ARROW_SIZE = 14;
const ROW = "flex items-center gap-2.5 border-t border-border py-2.5 first:border-t-0 hover:text-fg";
const ICON_BOX = "grid h-5 w-5 shrink-0 place-items-center rounded-md bg-surface-3 text-muted";
const ARROW = <ArrowRight size={ARROW_SIZE} className="ml-auto shrink-0 text-muted" aria-hidden="true" />;

function otherInBlockText(n: number): string {
  return n === 1 ? "1 other transaction in this block" : `${n} other transactions in this block`;
}

function WalletRow({ wallet }: { wallet: WalletRef }) {
  const name = wallet.label ?? shortAddress(wallet.address);
  return (
    <Link to={explorerPaths.wallet(wallet.address)} className={cn(ROW, "text-sm")}>
      <Identicon seed={wallet.address} size={ROW_IDENTICON_SIZE} />
      <span className="min-w-0 break-words">Everything {name} has done</span>
      {ARROW}
    </Link>
  );
}

/** A row that opens a transaction; the names inside its subtitle stay separate links. */
function PreviousTxRow({ previous }: { previous: NonNullable<TxDetail["context"]["previousFromSigner"]> }) {
  const message = firstMessage(previous.messages);
  return (
    <div className={cn(ROW, "relative text-sm")}>
      <Link
        to={explorerPaths.tx(previous.hash)}
        aria-label="Open the previous transaction from this sender"
        className={cn("absolute inset-0")}
      />
      <span className={ICON_BOX} aria-hidden="true"><ArrowLeft size={SMALL_ICON_SIZE} /></span>
      <span className="pointer-events-none min-w-0">
        Previous transaction from this sender
        {message && (
          <span className="block text-xs text-muted">
            <Sentence message={message} failed={!previous.status.ok} />
          </span>
        )}
      </span>
      {ARROW}
    </div>
  );
}

function receiverOf(message: TxMessage | null): WalletRef | null {
  return message?.type === "send" ? message.to : null;
}

/** "Keep investigating": the next places a reader is likely to want to go. */
export function InvestigateCard({ tx }: { tx: TxDetail }) {
  const receiver = receiverOf(firstMessage(tx.messages));
  const { previousFromSigner, otherInBlock } = tx.context;
  return (
    <Card title="Keep investigating">
      {tx.signer && <WalletRow wallet={tx.signer} />}
      {receiver && receiver.address !== tx.signer?.address && <WalletRow wallet={receiver} />}
      {previousFromSigner && <PreviousTxRow previous={previousFromSigner} />}
      {otherInBlock > 0 && (
        <Link to={explorerPaths.block(tx.height)} className={cn(ROW, "text-sm")}>
          <span className={ICON_BOX} aria-hidden="true"><LayoutGrid size={SMALL_ICON_SIZE} /></span>
          <span>{otherInBlockText(otherInBlock)}</span>
          {ARROW}
        </Link>
      )}
    </Card>
  );
}

/** "Is this normal?": only shown when the transaction has facts to judge it by. */
export function NormalCard({ tx }: { tx: TxDetail }) {
  const message = firstMessage(tx.messages);
  const sentences = message ? describeContext(tx.context, message) : [];
  if (sentences.length === 0) return null;
  return (
    <Card title="Is this normal?">
      <div className="space-y-2 text-[13.5px] text-muted">
        {sentences.map((s) => (
          <p key={s}>{s}</p>
        ))}
      </div>
    </Card>
  );
}
