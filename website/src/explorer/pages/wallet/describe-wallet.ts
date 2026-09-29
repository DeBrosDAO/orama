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

function relations(facts: WalletProfile["facts"]): SentencePart[] {
  const { topSender, topDelegate } = facts;
  if (!topSender && !topDelegate) return [];
  const parts: SentencePart[] = [text("Mostly ")];
  if (topSender) parts.push(text("receives ORAMA from "), { kind: "wallet", ref: topSender });
  if (topSender && topDelegate) parts.push(text(" and "));
  if (topDelegate) parts.push(text("delegates to "), { kind: "validator", ref: topDelegate });
  parts.push(text(". "));
  return parts;
}

function failures(count: number): string {
  if (count === 0) return "no failed payments this week";
  return `${formatInt(count)} failed ${count === 1 ? "payment" : "payments"} this week`;
}

/**
 * The plain-words summary of a wallet, from facts the explorer can compute:
 * what kind of wallet it is, who it deals with most, how active it is.
 */
export function describeWallet(profile: WalletProfile, nowMs: number): SentencePart[] {
  const { facts } = profile;
  const txs = `${formatInt(facts.txCount)} ${facts.txCount === 1 ? "transaction" : "transactions"}`;
  return [
    ...opening(profile.roles),
    ...relations(facts),
    text(
      `Last active ${formatRelative(facts.lastActive, nowMs)}, first seen ${formatRelative(facts.firstSeen, nowMs)}, ` +
        `with ${txs} and ${failures(facts.failedLast7d)}.`,
    ),
  ];
}
