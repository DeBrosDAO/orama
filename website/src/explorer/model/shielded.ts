import type { KeyPage } from "./paging";
import type { Norama, TxEvent, TxMessage, WalletRef } from "./types";

/**
 * The shielded pool's module account: it holds the pool's coins and the coins behind unshields that
 * wait in the queue. It is x/shielded's module address, and the protocol's signer-less address
 * (chain/x/shielded/types SignerlessAddress) is the same one: no key signs for it.
 */
export const SHIELDED_POOL_ADDRESS = "orama12hms2jejlyt5tdnalles8vzp97srs3q7kg7yrq";
/** The name the explorer gives that account wherever it appears. */
export const SHIELDED_POOL_LABEL = "Shielded pool";

/** The event x/shielded emits for every bundle it accepts (chain/x/shielded/keeper/msg_server.go). */
const BUNDLE_EVENT = "shielded_bundle";

export type BundleKind = "transfer" | "shield" | "shield_earnings" | "unshield";

/** What a successful shielded message makes public in its events. */
export interface ShieldedBundle {
  kind: BundleKind;
  /** The actions the bundle holds: its inputs and outputs, in pairs. */
  actions: number;
  /** What crosses between the pool and the public side: the fee of a private transfer, the amount shielded, or the amount unshielded. */
  amount: Norama;
}

const KINDS: Record<string, BundleKind> = {
  transfer: "transfer",
  shield: "shield",
  shield_earnings: "shield_earnings",
  unshield: "unshield",
};
const UINT = /^(0|[1-9][0-9]*)$/;

/** The bundle a transaction's events record, or null when it carries none (a failed transaction, or one that is not shielded). */
export function bundleOf(events: readonly TxEvent[]): ShieldedBundle | null {
  const event = events.find((e) => e.type === BUNDLE_EVENT);
  if (event === undefined) return null;
  const kind = KINDS[event.attributes.kind ?? ""];
  const actions = event.attributes.actions ?? "";
  const amount = event.attributes.amount ?? "";
  if (kind === undefined || !UINT.test(actions) || !UINT.test(amount)) return null;
  return { kind, actions: Number(actions), amount };
}

/** How much of a transaction the chain makes public. */
export type Privacy =
  /** Nothing but the type, the proof's size and the fee. */
  | "private"
  /** The signer and the amount are public; where the value sits inside the pool is not. */
  | "boundary"
  /** An ordinary transaction: parties and amounts are on chain. */
  | "public";

const PRIVATE_TYPE = "/orama.shielded.v1.MsgShieldedTransfer";
const UNSHIELD_TYPE = "/orama.shielded.v1.MsgUnshield";
const BOUNDARY_TYPES: ReadonlySet<string> = new Set([
  "/orama.shielded.v1.MsgShield",
  "/orama.shielded.v1.MsgShieldEarnings",
  UNSHIELD_TYPE,
]);

/** Whether the message moves value out of the pool. */
export function isUnshield(message: TxMessage): boolean {
  return message.type === "action" && message.typeUrl === UNSHIELD_TYPE;
}

/** The privacy of one message. */
export function privacyOfMessage(message: TxMessage): Privacy {
  if (message.type !== "action") return "public";
  if (message.typeUrl === PRIVATE_TYPE) return "private";
  return BOUNDARY_TYPES.has(message.typeUrl) ? "boundary" : "public";
}

/** The privacy of a transaction: the most private of its messages, since a message that hides something keeps it hidden. */
export function privacyOf(messages: readonly TxMessage[]): Privacy {
  const all = messages.map(privacyOfMessage);
  if (all.includes("private")) return "private";
  return all.includes("boundary") ? "boundary" : "public";
}

/** One transaction that moved value across the pool's boundary or inside it, from the account index of the pool. */
export interface ShieldedTx {
  hash: string;
  height: number;
  time: string;
  ok: boolean;
  /** Null for a failed transaction, which accepted no bundle. */
  bundle: ShieldedBundle | null;
  /** Null for a private transfer, which has no signer. */
  signer: WalletRef | null;
}

/** The parameters of x/shielded the explorer states. */
export interface ShieldedParams {
  /** Burned for each action of a bundle. */
  nullifierFee: Norama;
  maxActionsPerBundle: number;
  /** The least the 24-hour outflow cap allows. */
  unshieldFloor: Norama;
  /** The most one unshield to the signer's earnings, for fees, may move. */
  maxFeeTopup: Norama;
}

/** What queries expose of the pool as a whole. */
export interface ShieldedPool {
  /** The norama held by the pool's module account: the pool plus any unshields waiting in the queue. */
  held: Norama;
  /** Note commitments in the tree: one for each action of every bundle accepted. An action may be padding, so this is not a count of notes or payments. */
  noteCommitments: string;
  /** Nullifiers recorded: one for each action of every bundle accepted. */
  nullifiers: string;
  params: ShieldedParams;
}

/** A page of the transactions that touched the pool, newest first. `nextKey` is the indexer's cursor for the page after it. */
export type ShieldedActivity = KeyPage<ShieldedTx>;
