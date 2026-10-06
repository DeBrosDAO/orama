import type { TxEvent, TxMessage } from "../../model/types";
import { NORAMA_PER_ORAMA } from "../../model/units";
import { Account, halfOramaToNorama } from "./ledger";
import type { World } from "./world";

/** Norama paid per unit of gas, burned. Matches the chain's default base fee. */
export const BASE_FEE = 1000n;

export type SystemName = "burned" | "minted" | "staked" | "unbonding" | "storage_escrow";

/**
 * A pool that gives value shows a negative delta and a pool that receives it
 * a positive one, so every transaction's rows (wallets and pools, with the
 * fee as a positive "burned" row) sum to zero.
 */
export interface SystemDelta {
  name: SystemName;
  delta: bigint;
}

/** One transaction the world has decided to make, before any balance moves. */
export interface Plan {
  signer: Account;
  /** Other wallets whose available balance this transaction changes. */
  others: Account[];
  message: TxMessage;
  gasUsed: number;
  gasWanted: number;
  failure: string | null;
  events: TxEvent[];
  /** Moves the balances; returns the pool changes (fee excluded). Not called for failed transactions. */
  apply: () => SystemDelta[];
}

export type Kind = "send" | "grant" | "delegate" | "undelegate" | "claim" | "storage" | "failed";

const GAS: Record<Kind, number> = {
  send: 71_204,
  grant: 71_204,
  delegate: 146_310,
  undelegate: 154_022,
  claim: 118_450,
  storage: 209_880,
  failed: 60_000,
};

/** Each transaction's gas differs from its kind's figure by up to this much either way. */
const GAS_JITTER = 3_000;
/** A claim must pay out this many times the largest claim fee, so claiming never loses money. */
const CLAIM_FEE_MULTIPLE = 2n;

const feeOf = (gas: number): bigint => BigInt(gas) * BASE_FEE;
const whole = (orama: number): bigint => BigInt(orama) * NORAMA_PER_ORAMA;

export const MAX_CLAIM_FEE = feeOf(GAS.claim + GAS_JITTER);
export const MIN_CLAIM = MAX_CLAIM_FEE * CLAIM_FEE_MULTIPLE;

function gasFor(world: World, kind: Kind): number {
  return GAS[kind] + world.rng.int(-GAS_JITTER, GAS_JITTER);
}

function base(signer: Account, kind: Kind, world: World): Pick<Plan, "signer" | "gasUsed" | "gasWanted"> {
  const gasUsed = gasFor(world, kind);
  return { signer, gasUsed, gasWanted: Math.ceil((gasUsed * 1.3) / 1000) * 1000 };
}

function transferEvents(from: Account, to: Account, amount: bigint): TxEvent[] {
  return [
    { type: "message", attributes: { action: "/cosmos.bank.v1beta1.MsgSend", sender: from.ref.address } },
    { type: "transfer", attributes: { sender: from.ref.address, recipient: to.ref.address, amount: `${amount}norama` } },
  ];
}

function planSend(world: World, signer: Account, kind: "send" | "grant"): Plan | null {
  const gas = base(signer, kind, world);
  const spendable = signer.available - feeOf(gas.gasUsed);
  const wanted = kind === "grant" ? world.rng.int(20, 200) : Math.exp(world.rng.next() * 4.6) * 0.5;
  const amount = halfOramaToNorama(wanted);
  if (amount < NORAMA_PER_ORAMA / 2n || amount > spendable) return null;
  const to = world.pickReceiver(signer);
  if (!to || to === signer) return null;
  return {
    ...gas,
    others: [to],
    failure: null,
    message: { type: "send", from: signer.ref, to: to.ref, amount: amount.toString() },
    events: transferEvents(signer, to, amount),
    apply: () => {
      signer.available -= amount;
      to.available += amount;
      if (!signer.contacts.includes(to)) signer.contacts.push(to);
      return [];
    },
  };
}

function planDelegate(world: World, signer: Account): Plan | null {
  const gas = base(signer, "delegate", world);
  const maxWhole = Number((signer.available - feeOf(gas.gasUsed)) / NORAMA_PER_ORAMA);
  const amountWhole = Math.floor(maxWhole * (0.15 + world.rng.next() * 0.45));
  if (amountWhole < 10) return null;
  const validator = world.pickValidator(signer);
  const amount = whole(amountWhole);
  return {
    ...gas,
    others: [],
    failure: null,
    message: { type: "delegate", delegator: signer.ref, validator: validator.ref, amount: amount.toString() },
    events: [{ type: "delegate", attributes: { validator: validator.ref.moniker, amount: `${amount}norama` } }],
    apply: () => {
      signer.stake(validator.ref.moniker, amount, world.clockMs);
      return [{ name: "staked", delta: amount }];
    },
  };
}

function planUndelegate(world: World, signer: Account): Plan | null {
  const entries = [...signer.stakes.entries()].filter(([, s]) => s.amount >= NORAMA_PER_ORAMA);
  if (entries.length === 0) return null;
  const [moniker, stake] = world.rng.pick(entries);
  const validator = world.validatorByMoniker(moniker);
  const amount = world.rng.chance(0.5) ? stake.amount : (stake.amount / (2n * NORAMA_PER_ORAMA)) * NORAMA_PER_ORAMA;
  if (amount <= 0n) return null;
  return {
    ...base(signer, "undelegate", world),
    others: [],
    failure: null,
    message: { type: "undelegate", delegator: signer.ref, validator: validator.ref, amount: amount.toString() },
    events: [{ type: "unbond", attributes: { validator: moniker, amount: `${amount}norama` } }],
    apply: () => {
      signer.unstake(moniker, amount, world.clockMs);
      return [
        { name: "staked", delta: -amount },
        { name: "unbonding", delta: amount },
      ];
    },
  };
}

function planClaim(world: World, signer: Account): Plan | null {
  const due = [...signer.stakes.keys()].filter((m) => signer.accrued(m, world.clockMs) >= MIN_CLAIM);
  if (due.length === 0) return null;
  const moniker = world.rng.pick(due);
  const validator = world.validatorByMoniker(moniker);
  const paid = signer.accrued(moniker, world.clockMs);
  return {
    ...base(signer, "claim", world),
    others: [],
    failure: null,
    message: { type: "claim_rewards", delegator: signer.ref, validator: validator.ref, amount: paid.toString() },
    events: [{ type: "claim", attributes: { validator: moniker, amount: `${paid}norama` } }],
    apply: () => {
      signer.claim(moniker, world.clockMs);
      return [{ name: "minted", delta: -paid }];
    },
  };
}

function planStorage(world: World, signer: Account): Plan | null {
  const gas = base(signer, "storage", world);
  const amount = whole(world.rng.int(5, 60));
  if (amount > signer.available - feeOf(gas.gasUsed)) return null;
  const provider = world.rng.pick(world.providers);
  return {
    ...gas,
    others: [],
    failure: null,
    message: {
      type: "storage_deal",
      owner: signer.ref,
      provider: provider.ref,
      amount: amount.toString(),
      replicas: world.rng.int(1, 3),
      visibility: world.rng.chance(0.75) ? "private" : "public",
    },
    events: [{ type: "storage_deal", attributes: { provider: provider.ref.address, amount: `${amount}norama` } }],
    apply: () => {
      signer.available -= amount;
      return [{ name: "storage_escrow", delta: amount }];
    },
  };
}

/** A transfer the chain rejects. The fee is still charged; nothing else moves. */
function planFailed(world: World, signer: Account): Plan | null {
  const outOfGas = world.rng.chance(0.6);
  const gasUsed = outOfGas ? GAS.failed : 40_000;
  if (signer.available < feeOf(gasUsed)) return null;
  const to = world.pickReceiver(signer);
  if (!to) return null;
  const attempted = outOfGas
    ? halfOramaToNorama(world.rng.int(2, 60))
    : signer.available + halfOramaToNorama(world.rng.int(5, 200));
  return {
    signer,
    others: [],
    gasUsed,
    gasWanted: outOfGas ? GAS.failed : 60_000,
    failure: outOfGas ? "out of gas" : "insufficient funds",
    message: { type: "send", from: signer.ref, to: to.ref, amount: attempted.toString() },
    events: [],
    apply: () => [],
  };
}

export function plan(world: World, kind: Kind, signer: Account): Plan | null {
  switch (kind) {
    case "send":
    case "grant":
      return planSend(world, kind === "grant" ? world.foundation : signer, kind);
    case "delegate":
      return planDelegate(world, signer);
    case "undelegate":
      return planUndelegate(world, signer);
    case "claim":
      return planClaim(world, signer);
    case "storage":
      return planStorage(world, signer);
    case "failed":
      return planFailed(world, signer);
  }
}

export { feeOf };
