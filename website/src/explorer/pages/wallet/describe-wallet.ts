import type { SentencePart } from "../../model/describe";
import { formatInt } from "../../model/units";
import { formatRelative } from "../../model/time";
import type { WalletProfile } from "../../model/types";

const text = (t: string): SentencePart => ({ kind: "text", text: t });
const VOWELS = /^[aeiou]/i;

function opening(roles: string[]): SentencePart[] {
  if (roles.length === 0) return [];
  const noun = roles.map((r) => r.toLowerCase()).join(" and ");
  return [text(`${VOWELS.test(noun) ? "An" : "A"} ${noun} wallet. `)];
}

/**
 * The plain-words summary of a wallet, from facts the chain gives: what kind
 * of wallet it is, when it was first and last seen, and how many transactions
 * name it. A wallet that holds funds and has no transaction says so.
 */
export function describeWallet(profile: WalletProfile, nowMs: number): SentencePart[] {
  const { facts } = profile;
  if (facts.firstSeen === null || facts.lastActive === null) {
    return [...opening(profile.roles), text("No transaction has named this wallet yet.")];
  }
  const txs = `${formatInt(facts.txCount)} ${facts.txCount === 1 ? "transaction" : "transactions"}`;
  return [
    ...opening(profile.roles),
    text(`Last active ${formatRelative(facts.lastActive, nowMs)}, first seen ${formatRelative(facts.firstSeen, nowMs)}, with ${txs}.`),
  ];
}
