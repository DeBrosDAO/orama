import { tagOf } from "../../model/describe";
import type { Privacy, ShieldedBundle } from "../../model/shielded";
import { bundleOf, isUnshield, privacyOf, privacyOfMessage } from "../../model/shielded";
import type { MessageField, TxDetail } from "../../model/types";

/** What the chain shows of a shielded transaction, and what it keeps out of view. */
export interface PrivacyNotes {
  privacy: Exclude<Privacy, "public">;
  title: string;
  /** Facts the chain makes public about this transaction. */
  shown: MessageField[];
  /** What a reader might expect to find and cannot, because the chain does not carry it. */
  hidden: string[];
}

const HIDDEN_TRANSFER = [
  "Who sent it",
  "Who received it",
  "How much was sent",
  "Which earlier notes it spent, and so which earlier shield or transfer funded it",
];
const HIDDEN_SHIELD = ["Who will own the shielded value, and what it will be spent on", "Anything about the value once it is inside the pool"];
const HIDDEN_UNSHIELD = ["Which shielded notes paid for it", "Who put that value into the pool, and when"];

const field = (label: string, value: string, kind: MessageField["kind"] = "text"): MessageField => ({ label, value, kind });

function shownFor(tx: TxDetail, bundle: ShieldedBundle | null, privacy: PrivacyNotes["privacy"], unshield: boolean): MessageField[] {
  const shown: MessageField[] = [field("Result", tx.status.ok ? "Accepted by the chain" : "Refused by the chain: nothing moved")];
  if (bundle !== null) shown.push(field("Actions in the bundle", String(bundle.actions)));
  if (privacy === "private") {
    if (bundle !== null) shown.push(field("Fee, paid from the pool", bundle.amount, "norama"));
    return shown;
  }
  if (tx.signer !== null) shown.push(field("Signed by", tx.signer.address, "address"));
  if (bundle !== null) shown.push(field(unshield ? "Amount leaving the pool" : "Amount entering the pool", bundle.amount, "norama"));
  return shown;
}

/**
 * The notes under a shielded transaction: what is public and what is not. Null for an ordinary
 * transaction. The amount comes from the event x/shielded emits for an accepted bundle (its value
 * balance), so a transaction the chain refused shows no amount, and none is guessed.
 */
export function privacyNotes(tx: TxDetail): PrivacyNotes | null {
  const privacy = privacyOf(tx.messages);
  if (privacy === "public") return null;
  const bundle = tx.status.ok ? bundleOf(tx.events) : null;
  const first = tx.messages.find((m) => privacyOfMessage(m) === privacy);
  const unshield = tx.messages.some(isUnshield);
  const hidden = privacy === "private" ? HIDDEN_TRANSFER : unshield ? HIDDEN_UNSHIELD : HIDDEN_SHIELD;
  return {
    privacy,
    title: privacy === "private" ? "A private transfer" : `${first ? tagOf(first) : "Shielding"}: the public edge of the pool`,
    shown: shownFor(tx, bundle, privacy, unshield),
    hidden,
  };
}
