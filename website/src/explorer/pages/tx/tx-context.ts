import type { TxContext, TxMessage } from "../../model/types";

const PERCENT_MIN = 0;
const PERCENT_MAX = 100;

function percentSentence(percentile: number): string {
  const p = Math.round(Math.min(PERCENT_MAX, Math.max(PERCENT_MIN, percentile)));
  if (p === PERCENT_MIN) return "A transfer this size is among the smallest transfers this week.";
  return `A transfer this size is larger than ${p}% of transfers this week.`;
}

function priorSentence(count: number): string {
  if (count === 0) return "This is the first time the sender has paid this receiver.";
  return `The sender has paid this receiver before (${count === 1 ? "1 time" : `${count} times`}).`;
}

/**
 * Plain-language sentences that say whether a transaction is unusual. Only
 * transfers have the facts to judge by; everything else, and a transfer with
 * no facts, gives an empty list so the page can hide the card.
 */
export function describeContext(ctx: TxContext, message: TxMessage): string[] {
  if (message.type !== "send") return [];
  const out: string[] = [];
  if (ctx.amountPercentile !== null && Number.isFinite(ctx.amountPercentile)) {
    out.push(percentSentence(ctx.amountPercentile));
  }
  if (ctx.priorBetweenParties !== null && Number.isInteger(ctx.priorBetweenParties) && ctx.priorBetweenParties >= 0) {
    out.push(priorSentence(ctx.priorBetweenParties));
  }
  return out;
}
