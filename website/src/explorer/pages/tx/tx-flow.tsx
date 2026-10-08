import { Link } from "react-router";
import { amountOf } from "../../model/describe";
import { explorerPaths } from "../../model/routes";
import type { Fee, TxMessage } from "../../model/types";
import { parseNorama } from "../../model/units";
import { cn } from "../../../lib/utils";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Identicon } from "../../ui/identicon";
import { flowOf, walletTitle } from "./tx-parties";
import type { FlowParty } from "./tx-parties";

const NODE_ICON_SIZE = 24;

function feeTip(tip: string): string {
  const base = "The fee is destroyed, not paid to a validator. A tip to the block proposer is optional";
  return parseNorama(tip) > 0n ? `${base}, and this transaction paid one.` : `${base}, and this transaction paid none.`;
}

function FlowNode({ party }: { party: FlowParty }) {
  const { wallet, role } = party;
  return (
    <Link
      to={explorerPaths.wallet(wallet.address)}
      className={cn("block rounded-xl border border-border bg-surface-2 p-3.5 transition-colors hover:border-fg/30")}
    >
      <div className="text-[11px] uppercase tracking-[0.09em] text-muted">{role}</div>
      <div className="mt-1.5 flex items-center gap-2 font-semibold">
        <Identicon seed={wallet.address} size={NODE_ICON_SIZE} />
        <span className="min-w-0 break-words">{walletTitle(wallet)}</span>
      </div>
      <div className="mt-0.5 break-all font-mono text-xs text-muted">{wallet.address}</div>
    </Link>
  );
}

/** A line with an arrowhead: vertical on a phone, horizontal from md up, with the amount on it. */
function Pipe({ amount, failed }: { amount: string; failed: boolean }) {
  return (
    <div className="relative flex h-14 items-center justify-center md:h-[60px]">
      <span aria-hidden="true" className="absolute bottom-2 left-1/2 top-0 w-0.5 -translate-x-1/2 bg-linear-to-b from-border to-fg md:hidden" />
      <span aria-hidden="true" className="absolute bottom-0 left-1/2 -translate-x-1/2 border-x-[7px] border-t-[10px] border-x-transparent border-t-fg md:hidden" />
      <span aria-hidden="true" className="absolute left-0 right-2 top-1/2 hidden h-0.5 -translate-y-1/2 bg-linear-to-r from-border to-fg md:block" />
      <span aria-hidden="true" className="absolute right-0 top-1/2 hidden -translate-y-1/2 border-y-[7px] border-l-[10px] border-y-transparent border-l-fg md:block" />
      <span
        className={cn(
          "relative rounded-full border bg-bg px-3 py-0.5 font-semibold",
          failed ? "border-loss/50 text-loss line-through" : "border-fg/25",
        )}
      >
        <Amount norama={amount} />
      </span>
    </div>
  );
}

function FeeLine({ fee }: { fee: Fee }) {
  const tipped = parseNorama(fee.tip) > 0n;
  return (
    <p className="text-center text-[12.5px] text-muted">
      + network fee <b className="font-medium text-fg"><Amount norama={fee.burned} /></b> · <Badge tone="signal">🔥 burned</Badge>
      {tipped && (
        <>
          {" "}+ tip <b className="font-medium text-fg"><Amount norama={fee.tip} /></b> to the block proposer
        </>
      )}
      <Help tip={feeTip(fee.tip)} />
    </p>
  );
}

const NO_MESSAGE_FLOW_TEXT = "This transaction carries no messages, so nothing moved except the fee.";

/** "Where the money went": from, the amount travelling, to; then the fee. Only the first message is drawn. */
export function FlowCard({ message, fee, failed }: { message: TxMessage | null; fee: Fee; failed: boolean }) {
  if (message === null) {
    return (
      <Card title="Where the money went">
        <p className="my-3 text-sm text-muted">{NO_MESSAGE_FLOW_TEXT}</p>
        <FeeLine fee={fee} />
      </Card>
    );
  }
  const { from, to } = flowOf(message);
  const amount = amountOf(message);
  return (
    <Card title="Where the money went">
      <div className={cn("my-3 grid items-center", to && "md:grid-cols-[1fr_1.1fr_1fr]")}>
        {from && <FlowNode party={from} />}
        {to && amount !== null && <Pipe amount={amount} failed={failed} />}
        {to && <FlowNode party={to} />}
      </div>
      <FeeLine fee={fee} />
    </Card>
  );
}
