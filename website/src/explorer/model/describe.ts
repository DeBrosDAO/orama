import type { TxMessage, TxMessageType, ValidatorRef, WalletRef } from "./types";

/** The short tag shown beside a transaction. */
export const MESSAGE_TAGS: Record<TxMessageType, string> = {
  send: "Transfer",
  delegate: "Stake",
  undelegate: "Unstake",
  claim_rewards: "Rewards",
  storage_deal: "Storage",
  unknown: "Other",
};

export type Category = "transfers" | "staking" | "storage" | "other";

export function categoryOf(message: TxMessage): Category {
  switch (message.type) {
    case "send":
      return "transfers";
    case "delegate":
    case "undelegate":
    case "claim_rewards":
      return "staking";
    case "storage_deal":
      return "storage";
    case "unknown":
      return "other";
  }
}

/** The amount a message moves, or null when it moves none the explorer can read. */
export function amountOf(message: TxMessage): string | null {
  return message.type === "unknown" ? null : message.amount;
}

/** Everything a sentence renderer needs: the pieces, in reading order. */
export type SentencePart =
  | { kind: "text"; text: string }
  | { kind: "wallet"; ref: WalletRef }
  | { kind: "validator"; ref: ValidatorRef }
  | { kind: "amount"; norama: string };

const t = (text: string): SentencePart => ({ kind: "text", text });
const w = (ref: WalletRef): SentencePart => ({ kind: "wallet", ref });
const v = (ref: ValidatorRef): SentencePart => ({ kind: "validator", ref });
const a = (norama: string): SentencePart => ({ kind: "amount", norama });

/** "Alice sent 12.5 ORAMA to Bob" as parts, so the UI can link the names. */
export function describeMessage(m: TxMessage, failed = false): SentencePart[] {
  switch (m.type) {
    case "send":
      return [w(m.from), t(failed ? " tried to send " : " sent "), a(m.amount), t(" to "), w(m.to)];
    case "delegate":
      return [w(m.delegator), t(failed ? " tried to stake " : " staked "), a(m.amount), t(" with "), v(m.validator)];
    case "undelegate":
      return [w(m.delegator), t(failed ? " tried to unstake " : " started unstaking "), a(m.amount), t(" from "), v(m.validator)];
    case "claim_rewards":
      return [w(m.delegator), t(failed ? " tried to claim " : " claimed "), a(m.amount), t(" in rewards from "), v(m.validator)];
    case "storage_deal":
      return [
        w(m.owner),
        t(failed ? " tried to open a " : " opened a "),
        t(`${m.visibility} storage deal`),
        t(` with ${m.replicas} ${m.replicas === 1 ? "copy" : "copies"} for `),
        a(m.amount),
      ];
    case "unknown":
      return [...(m.signer ? [w(m.signer)] : [t("A signer-less transaction")]), t(` sent a ${m.typeUrl.replace(/^\//, "")} message`)];
  }
}
