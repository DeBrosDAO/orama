import type { MessageCategory, SentencePart, TxMessage, ValidatorRef, WalletRef } from "./types";

export type { SentencePart } from "./types";

const MESSAGE_TAGS = {
  send: "Transfer",
  delegate: "Stake",
  undelegate: "Unstake",
  claim_rewards: "Rewards",
  storage_deal: "Storage",
  unknown: "Other",
} as const;

/** The short tag shown beside a transaction. */
export function tagOf(message: TxMessage): string {
  return message.type === "action" ? message.tag : MESSAGE_TAGS[message.type];
}

export type Category = MessageCategory;

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
    case "action":
      return message.category;
    case "unknown":
      return "other";
  }
}

/** True when the wallet signed an action or is one of the accounts its sentence names. */
export function actionInvolves(m: Extract<TxMessage, { type: "action" }>, address: string): boolean {
  return m.actor?.address === address || m.done.some((p) => p.kind === "wallet" && p.ref.address === address);
}

/** The amount a message moves, or null when it moves none the explorer can read. */
export function amountOf(message: TxMessage): string | null {
  return message.type === "unknown" ? null : message.amount;
}

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
    case "action":
      return failed ? m.tried : m.done;
    case "unknown":
      return [...(m.signer ? [w(m.signer)] : [t("A signer-less transaction")]), t(` sent a ${m.typeUrl.replace(/^\//, "")} message`)];
  }
}
