import type { SentencePart } from "../model/describe";
import { formatNorama, shortAddress, shortHash } from "../model/units";

const TEXT_SEPARATOR = " · ";

/** A sentence as plain text, for places that cannot hold links (an accessible name, a title). */
export function messageText(parts: readonly SentencePart[]): string {
  return parts
    .map((part) => {
      switch (part.kind) {
        case "text":
          return part.text;
        case "wallet":
          return part.ref.label ?? shortAddress(part.ref.address);
        case "validator":
          return part.ref.moniker;
        case "amount":
          return `${formatNorama(part.norama)} ORAMA`;
      }
    })
    .join("");
}

/** The accessible name of a row's overlay link: what happened, then which transaction. */
export function txLinkName(parts: readonly SentencePart[], hash: string): string {
  return `${messageText(parts)}${TEXT_SEPARATOR}${shortHash(hash)}`;
}

/** "+2 more" for a transaction that carries more messages than the one described; null for one message. */
export function moreMessagesBadge(messageCount: number): string | null {
  return messageCount > 1 ? `+${messageCount - 1} more` : null;
}

/** The longer note for a detail view. */
export function moreActionsNote(messageCount: number): string | null {
  const extra = messageCount - 1;
  if (extra < 1) return null;
  return `+${extra} more ${extra === 1 ? "action" : "actions"} in this transaction. Open the full transaction to see them.`;
}
