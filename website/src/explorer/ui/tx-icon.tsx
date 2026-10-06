import { ArrowDownLeft, ArrowUpRight, Gift, HardDrive, Layers, MoreHorizontal, X } from "lucide-react";
import type { ActivityDirection, TxMessage } from "../model/types";
import { cn } from "../../lib/utils";

const ICON_SIZE = 16;

function iconFor(message: TxMessage, direction: ActivityDirection | null) {
  switch (message.type) {
    case "send":
      return direction === "in" ? <ArrowDownLeft size={ICON_SIZE} /> : <ArrowUpRight size={ICON_SIZE} />;
    case "delegate":
    case "undelegate":
      return <Layers size={ICON_SIZE} />;
    case "claim_rewards":
      return <Gift size={ICON_SIZE} />;
    case "storage_deal":
      return <HardDrive size={ICON_SIZE} />;
    case "unknown":
      return <MoreHorizontal size={ICON_SIZE} />;
  }
}

export interface TxIconProps {
  message: TxMessage;
  failed?: boolean;
  /** From a wallet's side. Omit for chain-wide lists. */
  direction?: ActivityDirection;
}

/** The small square beside a transaction: what kind it is, and whether money came in or it failed. */
export function TxIcon({ message, failed = false, direction }: TxIconProps) {
  const tone = failed ? "bg-loss/15 text-loss" : direction === "in" ? "bg-gain/15 text-gain" : "bg-surface-3 text-muted";
  return (
    <span className={cn("grid h-9 w-9 shrink-0 place-items-center rounded-[10px]", tone)} aria-hidden="true">
      {failed ? <X size={ICON_SIZE} /> : iconFor(message, direction ?? null)}
    </span>
  );
}
