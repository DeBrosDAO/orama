import { MESSAGE_TAGS } from "../../model/describe";
import type { TxMessage } from "../../model/types";

/** The transaction's headline message: the sentence, flow and amount describe this one. */
export function firstMessage(messages: readonly TxMessage[]): TxMessage | null {
  return messages[0] ?? null;
}

/** How many messages the headline does not cover. */
export function extraActionCount(messages: readonly TxMessage[]): number {
  return Math.max(0, messages.length - 1);
}

/** "+1 more action", "+3 more actions"; empty when the headline covers everything. */
export function moreActionsText(messages: readonly TxMessage[]): string {
  const extra = extraActionCount(messages);
  if (extra === 0) return "";
  return `+${extra} more ${extra === 1 ? "action" : "actions"}`;
}

/** The message types in order, each named once: "Transfer, Stake". Empty for no messages. */
export function messageTypesText(messages: readonly TxMessage[]): string {
  return [...new Set(messages.map((m) => MESSAGE_TAGS[m.type]))].join(", ");
}
