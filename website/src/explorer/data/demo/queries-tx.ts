import type { ActivityFilter, TxContext, TxDetail, TxMessage, TxSummary } from "../../model/types";
import { categoryOf } from "../../model/describe";
import { TIME } from "../../model/time";
import { firstIndexAtOrAfter } from "./queries-chain";
import type { TxRecord, World } from "./world";

const WEEK_MS = 7 * TIME.DAY;

const TYPE_URLS: Record<TxMessage["type"], string> = {
  send: "/cosmos.bank.v1beta1.MsgSend",
  delegate: "/cosmos.staking.v1beta1.MsgDelegate",
  undelegate: "/cosmos.staking.v1beta1.MsgUndelegate",
  claim_rewards: "/orama.power.v1.MsgClaimRewards",
  storage_deal: "/orama.storage.v1.MsgCreateDeal",
  unknown: "",
};

const coin = (amount: string) => ({ denom: "norama", amount });

function bodyOf(m: TxMessage): Record<string, unknown> {
  const type = { "@type": TYPE_URLS[m.type] };
  switch (m.type) {
    case "send":
      return { ...type, from_address: m.from.address, to_address: m.to.address, amount: [coin(m.amount)] };
    case "delegate":
    case "undelegate":
      return { ...type, delegator_address: m.delegator.address, validator_address: m.validator.operator, amount: coin(m.amount) };
    case "claim_rewards":
      return { ...type, delegator_address: m.delegator.address, validator_address: m.validator.operator };
    case "storage_deal":
      return { ...type, owner: m.owner.address, provider: m.provider.address, replicas: m.replicas, visibility: m.visibility, deposit: coin(m.amount) };
    case "unknown":
      return { "@type": m.typeUrl, signer: m.signer.address };
  }
}

function rawJson(rec: TxRecord): string {
  const fee = rec.summary.fee;
  return JSON.stringify(
    {
      body: { messages: rec.summary.messages.map(bodyOf), memo: rec.memo },
      auth_info: { fee: { amount: [coin(fee.burned)], gas_limit: String(fee.gasWanted) } },
      gas_used: String(fee.gasUsed),
    },
    null,
    2,
  );
}

function priorBetween(world: World, rec: TxRecord): number | null {
  const m = rec.summary.messages[0];
  if (m?.type !== "send" || !rec.summary.status.ok) return null;
  const signer = world.account(m.from.address);
  if (!signer) return null;
  let n = 0;
  for (const i of signer.txIndices) {
    if (i >= rec.index) break;
    const prev = world.txs[i] as TxRecord;
    const pm = prev.summary.messages[0];
    if (prev.summary.status.ok && pm?.type === "send" && pm.from.address === m.from.address && pm.to.address === m.to.address) n++;
  }
  return n;
}

function amountPercentile(world: World, rec: TxRecord): number | null {
  const m = rec.summary.messages[0];
  if (m?.type !== "send" || !rec.summary.status.ok) return null;
  const amount = BigInt(m.amount);
  let smaller = 0;
  let total = 0;
  for (let i = firstIndexAtOrAfter(world.txs, rec.timeMs - WEEK_MS); i < world.txs.length; i++) {
    const other = world.txs[i] as TxRecord;
    if (other.timeMs > rec.timeMs) break;
    const om = other.summary.messages[0];
    if (om?.type !== "send" || !other.summary.status.ok) continue;
    total++;
    if (BigInt(om.amount) < amount) smaller++;
  }
  return total === 0 ? null : Math.round((smaller / total) * 100);
}

function previousFromSigner(world: World, rec: TxRecord): TxSummary | null {
  const signer = world.account(rec.summary.signer.address);
  if (!signer) return null;
  const pos = signer.txIndices.indexOf(rec.index);
  return pos > 0 ? (world.txs[signer.txIndices[pos - 1] as number] as TxRecord).summary : null;
}

function contextOf(world: World, rec: TxRecord): TxContext {
  return {
    amountPercentile: amountPercentile(world, rec),
    priorBetweenParties: priorBetween(world, rec),
    previousFromSigner: previousFromSigner(world, rec),
    otherInBlock: (world.byHeight.get(rec.height)?.length ?? 1) - 1,
  };
}

export function txDetail(world: World, hash: string): TxDetail | null {
  const index = world.byHash.get(hash);
  if (index === undefined) return null;
  const rec = world.txs[index] as TxRecord;
  return {
    ...rec.summary,
    memo: rec.memo,
    balanceChanges: rec.changes,
    events: rec.events,
    rawJson: rawJson(rec),
    context: contextOf(world, rec),
  };
}

function matches(rec: TxRecord, filter: ActivityFilter): boolean {
  const ok = rec.summary.status.ok;
  if (filter === "failed") return !ok;
  if (!ok) return filter === "all";
  const category = categoryOf(rec.summary.messages[0] as TxMessage);
  return filter === "all" || filter === category;
}

/** Newest first. */
export function latestActivity(world: World, filter: ActivityFilter, limit: number): TxSummary[] {
  const out: TxSummary[] = [];
  for (let i = world.txs.length - 1; i >= 0 && out.length < limit; i--) {
    const rec = world.txs[i] as TxRecord;
    if (matches(rec, filter)) out.push(rec.summary);
  }
  return out;
}
