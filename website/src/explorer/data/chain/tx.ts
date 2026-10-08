import { bech32Rehrp } from "../../model/bech32";
import type {
  BalanceChange,
  Fee,
  Norama,
  StorageVisibility,
  TxDetail,
  TxEvent,
  TxMessage,
  TxStatus,
  TxSummary,
  ValidatorRef,
  WalletRef,
} from "../../model/types";
import { ChainReadError } from "./client";
import { clean } from "./text";
import { account, arr, digits, hash64, iso, optArr, optStr, optUint, rec, str, uint } from "./wire";
import type { Rec } from "./wire";

const NORAMA = "norama";
const ACCOUNT_HRP = "orama";
const MAX_REASON = 200;
const MAX_MEMO = 256;
const MAX_TYPE_URL = 200;
const MAX_EVENT_TYPE = 64;
const MAX_ATTRIBUTE = 512;
const MAX_EVENTS = 200;
const MAX_ATTRIBUTES = 64;

const TYPE_SEND = "/cosmos.bank.v1beta1.MsgSend";
const TYPE_DELEGATE = "/cosmos.staking.v1beta1.MsgDelegate";
const TYPE_UNDELEGATE = "/cosmos.staking.v1beta1.MsgUndelegate";
const TYPE_CREATE_DEAL = "/orama.storage.v1.MsgCreateDeal";

const DEAL_CLASS_PRIVATE = "DEAL_CLASS_PRIVATE";
const DEAL_CLASS_PUBLIC_PIN = "DEAL_CLASS_PUBLIC_PIN";

/** A transaction as the indexer serves it (chain/indexer Tx), checked. */
export interface IndexTx {
  hash: string;
  height: number;
  index: number;
  code: number;
  log: string;
  gasWanted: number;
  gasUsed: number;
  time: string;
  signer: string | null;
  memo: string;
  messages: string[];
  body: Rec[];
  events: TxEvent[];
}

function readEvents(raw: unknown): TxEvent[] {
  return optArr(raw, "transaction events")
    .slice(0, MAX_EVENTS)
    .map((item) => {
      const e = rec(item, "transaction event");
      const attributes: Record<string, string> = {};
      for (const a of optArr(e.attributes, "event attributes").slice(0, MAX_ATTRIBUTES)) {
        const attr = rec(a, "event attribute");
        attributes[clean(str(attr.key, "event attribute key", MAX_ATTRIBUTE), MAX_ATTRIBUTE)] = clean(optStr(attr.value, "event attribute value", MAX_ATTRIBUTE), MAX_ATTRIBUTE);
      }
      return { type: clean(str(e.type, "event type", MAX_EVENT_TYPE), MAX_EVENT_TYPE), attributes };
    });
}

export function readIndexTx(raw: unknown): IndexTx {
  const t = rec(raw, "transaction");
  const signer = optStr(t.signer, "transaction signer", 120);
  return {
    hash: hash64(t.hash, "transaction hash"),
    height: uint(t.height, "transaction height"),
    index: uint(t.index, "transaction index"),
    code: optUint(t.code, "transaction code"),
    log: optStr(t.log, "transaction log", 4096),
    gasWanted: optUint(t.gas_wanted, "gas wanted"),
    gasUsed: optUint(t.gas_used, "gas used"),
    time: iso(t.time, "transaction time"),
    signer: signer === "" ? null : account(signer, "transaction signer"),
    memo: clean(optStr(t.memo, "transaction memo", 1024), MAX_MEMO),
    messages: arr(t.messages, "transaction messages").map((m) => str(m, "message type", MAX_TYPE_URL)),
    body: arr(t.body, "transaction body").map((m) => rec(m, "message")),
    events: readEvents(t.events),
  };
}

/** Maps a validator operator address (oramavaloper...) to the validator's reference, or to a bare one. */
export type ValidatorLookup = (operator: string) => ValidatorRef;
/** The label a wallet carries, if the chain's validator directory names it. */
export type LabelLookup = (address: string) => string | undefined;

export interface Context {
  label: LabelLookup;
  validator: ValidatorLookup;
}

export function walletRef(address: string, ctx: Context): WalletRef {
  const label = ctx.label(address);
  return label === undefined ? { address } : { address, label };
}

function noramaAmountOf(coin: unknown, what: string): Norama | null {
  const c = rec(coin, what);
  return c.denom === NORAMA ? digits(c.amount, what) : null;
}

function sumNorama(coins: unknown, what: string): Norama | null {
  let total = 0n;
  let any = false;
  for (const coin of arr(coins, what)) {
    const amount = noramaAmountOf(coin, what);
    if (amount === null) continue;
    total += BigInt(amount);
    any = true;
  }
  return any ? total.toString() : null;
}

function operatorAccount(valoper: string): string {
  const converted = bech32Rehrp(str(valoper, "validator address", 120), ACCOUNT_HRP);
  if (converted === null) throw new ChainReadError("The chain sent a malformed validator address.");
  return converted;
}

function mapSend(m: Rec, ctx: Context): TxMessage | null {
  const amount = sumNorama(m.amount, "send amount");
  if (amount === null) return null;
  return {
    type: "send",
    from: walletRef(account(m.from_address, "sender"), ctx),
    to: walletRef(account(m.to_address, "receiver"), ctx),
    amount,
  };
}

function mapStake(kind: "delegate" | "undelegate", m: Rec, ctx: Context): TxMessage | null {
  const amount = noramaAmountOf(m.amount, "stake amount");
  if (amount === null) return null;
  return {
    type: kind,
    delegator: walletRef(account(m.delegator_address, "delegator"), ctx),
    validator: ctx.validator(operatorAccount(str(m.validator_address, "validator address", 120))),
    amount,
  };
}

function visibilityOf(deal: string): StorageVisibility | null {
  if (deal.includes(DEAL_CLASS_PRIVATE)) return "private";
  if (deal.includes(DEAL_CLASS_PUBLIC_PIN)) return "public";
  return null;
}

function mapDeal(m: Rec, ctx: Context): TxMessage | null {
  const visibility = visibilityOf(str(m.class, "deal class", 64));
  if (visibility === null) return null;
  const payer = optStr(m.granter, "deal granter", 120) || str(m.signer, "deal signer", 120);
  const replicas = uint(m.replicas, "deal replicas");
  const price = BigInt(digits(m.price_per_epoch, "deal price"));
  const epochs = BigInt(digits(m.duration_epochs, "deal duration"));
  return {
    type: "storage_deal",
    owner: walletRef(account(payer, "deal payer"), ctx),
    provider: null,
    amount: (price * BigInt(replicas) * epochs).toString(),
    replicas,
    visibility,
  };
}

/**
 * One message of a transaction body as the explorer's vocabulary. A type the
 * explorer has no sentence for (or a transfer of a token other than ORAMA) is
 * an "unknown" message carrying its type URL, never a blank row.
 */
export function mapMessage(m: Rec, signer: WalletRef | null, ctx: Context): TxMessage {
  const typeUrl = clean(str(m["@type"], "message type", MAX_TYPE_URL), MAX_TYPE_URL);
  let mapped: TxMessage | null = null;
  switch (m["@type"]) {
    case TYPE_SEND:
      mapped = mapSend(m, ctx);
      break;
    case TYPE_DELEGATE:
      mapped = mapStake("delegate", m, ctx);
      break;
    case TYPE_UNDELEGATE:
      mapped = mapStake("undelegate", m, ctx);
      break;
    case TYPE_CREATE_DEAL:
      mapped = mapDeal(m, ctx);
      break;
  }
  return mapped ?? { type: "unknown", typeUrl, signer };
}

function eventAttribute(events: readonly TxEvent[], type: string, key: string): string | undefined {
  return events.find((e) => e.type === type && key in e.attributes)?.attributes[key];
}

function noramaOf(attribute: string | undefined): Norama {
  if (attribute === undefined) return "0";
  return /^(0|[1-9][0-9]*)$/.test(attribute) ? attribute : "0";
}

/** What the fee ante handler settled: it emits a "tx" event with the base fee it burned and the tip it paid. */
export function feeOf(tx: IndexTx): Fee {
  return {
    burned: noramaOf(eventAttribute(tx.events, "tx", "base_fee")),
    tip: noramaOf(eventAttribute(tx.events, "tx", "tip")),
    gasUsed: tx.gasUsed,
    gasWanted: tx.gasWanted,
  };
}

function statusOf(tx: IndexTx): TxStatus {
  if (tx.code === 0) return { ok: true };
  const reason = clean(tx.log, MAX_REASON);
  return { ok: false, reason: reason === "" ? `the chain refused it (code ${tx.code})` : reason };
}

export function summaryOf(tx: IndexTx, ctx: Context): TxSummary {
  const signer = tx.signer === null ? null : walletRef(tx.signer, ctx);
  return {
    hash: tx.hash,
    height: tx.height,
    time: tx.time,
    status: statusOf(tx),
    signer,
    messages: tx.body.map((m) => mapMessage(m, signer, ctx)),
    fee: feeOf(tx),
  };
}

const COIN = /^([0-9]+)([a-zA-Z][a-zA-Z0-9/:._-]{2,127})$/;

/** The norama in an SDK coins string such as "5norama,3factory/x/y". */
function noramaInCoins(text: string): bigint {
  let total = 0n;
  for (const part of text.split(",")) {
    const m = COIN.exec(part.trim());
    if (m && m[2] === NORAMA) total += BigInt(m[1] ?? "0");
  }
  return total;
}

/**
 * The effect of a transaction on every account, from the bank events it
 * emitted: what each address received less what it spent, and what was burned.
 * Before and after are unknown to the events, so they are null; a payment made
 * from an earnings account moves no bank event and so does not appear here.
 */
export function balanceChangesOf(tx: IndexTx, ctx: Context): BalanceChange[] {
  const deltas = new Map<string, bigint>();
  let burned = 0n;
  const add = (address: string | undefined, amount: bigint) => {
    if (address === undefined || amount === 0n) return;
    const who = address.toLowerCase();
    if (!/^orama1[02-9ac-hj-np-z]+$/.test(who)) return;
    deltas.set(who, (deltas.get(who) ?? 0n) + amount);
  };
  for (const e of tx.events) {
    const amount = noramaInCoins(e.attributes.amount ?? "");
    if (e.type === "coin_spent") add(e.attributes.spender, -amount);
    else if (e.type === "coin_received") add(e.attributes.receiver, amount);
    else if (e.type === "burn") burned += amount;
  }
  const wallets: BalanceChange[] = [...deltas.entries()]
    .filter(([, delta]) => delta !== 0n)
    .sort(([a, x], [b, y]) => {
      const mx = x < 0n ? -x : x;
      const my = y < 0n ? -y : y;
      return mx === my ? a.localeCompare(b) : mx > my ? -1 : 1;
    })
    .map(([address, delta]) => ({
      party: { kind: "wallet" as const, ref: walletRef(address, ctx) },
      before: null,
      after: null,
      delta: delta.toString(),
    }));
  if (burned > 0n) {
    wallets.push({ party: { kind: "system", name: "burned" }, before: null, after: null, delta: burned.toString() });
  }
  return wallets;
}

/** The transaction body as the explorer's "raw transaction" text. */
export function rawJsonOf(tx: IndexTx): string {
  return JSON.stringify({ body: { messages: tx.body, memo: tx.memo }, signer: tx.signer }, null, 2);
}

export function detailOf(tx: IndexTx, ctx: Context, otherInBlock: number, previousFromSigner: TxSummary | null): TxDetail {
  return {
    ...summaryOf(tx, ctx),
    memo: tx.memo,
    balanceChanges: balanceChangesOf(tx, ctx),
    events: tx.events,
    rawJson: rawJsonOf(tx),
    context: { amountPercentile: null, priorBetweenParties: null, previousFromSigner, otherInBlock },
  };
}
