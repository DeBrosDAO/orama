import type { BalanceChange, TxEvent, TxSummary, ValidatorRef } from "../../model/types";
import { TIME } from "../../model/time";
import { NORAMA_PER_ORAMA } from "../../model/units";
import { changeRows } from "./change-rows";
import { hashFor } from "./ids";
import { Account } from "./ledger";
import { feeOf, plan } from "./plans";
import type { Kind, Plan } from "./plans";
import { Rng } from "./rng";
import {
  PROVIDER_LABELS,
  VALIDATOR_SEEDS,
  foundationRef,
  operatorRef,
  providerRef,
  userRef,
  validatorRef,
} from "./roster";
import type { ValidatorSeed } from "./roster";

export const BLOCK_MS = 2000;
export const GENESIS_SUPPLY = 100_000_000n * NORAMA_PER_ORAMA;
export const CHAIN_ID = "orama-demo-1";
export const HISTORY_DAYS = 30;

/** Transactions per second after the anchor: enough for a live feed that visibly moves. */
const LIVE_RATE = 0.05;
const HISTORY_RATE_START = 0.0004;
const HISTORY_RATE_END = 0.03;
const ONBOARD_START = 12;
const ONBOARD_END = 130;
const ZIPF_EXPONENT = 0.8;
const OPERATOR_FUNDING = 10_000;
const PROVIDER_FUNDING = 500;

const KIND_WEIGHTS: ReadonlyArray<readonly [Kind, number]> = [
  ["send", 0.5],
  ["grant", 0.05],
  ["delegate", 0.12],
  ["undelegate", 0.04],
  ["claim", 0.12],
  ["storage", 0.08],
  ["failed", 0.03],
];

export interface TxRecord {
  index: number;
  timeMs: number;
  height: number;
  summary: TxSummary;
  changes: BalanceChange[];
  events: TxEvent[];
  memo: string;
}

export interface ValidatorSim extends ValidatorSeed {
  ref: ValidatorRef;
}

export interface WorldOptions {
  seed: number;
  /**
   * Where history ends and live activity begins. Everything before it is a
   * pure function of the seed and this value, so pass a stable instant (the
   * start of the UTC day) and links to transactions keep working on reload.
   */
  anchorMs: number;
}

/**
 * A small, deterministic simulation of an Orama-like chain: wallets that
 * fund, pay, stake, claim and open storage deals, with every balance change
 * replayed so each transaction's before/after is exact. The same seed and
 * time always produce the same history.
 */
export class World {
  readonly rng: Rng;
  readonly genesisMs: number;
  readonly accounts = new Map<string, Account>();
  readonly txs: TxRecord[] = [];
  readonly byHeight = new Map<number, number[]>();
  readonly byHash = new Map<string, number>();
  readonly validators: ValidatorSim[] = VALIDATOR_SEEDS.map((s) => ({ ...s, ref: validatorRef(s.moniker) }));
  readonly providers: Account[] = [];
  readonly foundation: Account;
  /** Users in the order they joined; earlier users are busier. */
  readonly users: Account[] = [];
  minted = 0n;
  burned = 0n;
  /** The timestamp of the transaction being built; plans read it. */
  clockMs = 0;
  private readonly seed: number;
  private readonly historyEndMs: number;
  private userWeights: number[] = [];
  private nextEventMs: number;
  private cursorMs: number;

  constructor(opts: WorldOptions) {
    this.seed = opts.seed;
    this.rng = new Rng(opts.seed);
    this.genesisMs = Math.floor((opts.anchorMs - HISTORY_DAYS * TIME.DAY) / BLOCK_MS) * BLOCK_MS;
    this.historyEndMs = opts.anchorMs;
    this.cursorMs = this.genesisMs;
    this.foundation = this.addAccount(foundationRef());
    this.foundation.available = GENESIS_SUPPLY;
    this.foundation.snapshot(this.genesisMs);
    for (const label of PROVIDER_LABELS) this.providers.push(this.addAccount(providerRef(label)));
    this.genesisFunding();
    this.nextEventMs = this.cursorMs + this.rng.exponential(this.rateAt(this.cursorMs)) * 1000;
    this.advanceTo(opts.anchorMs);
  }

  heightAt(ms: number): number {
    return 1 + Math.floor((ms - this.genesisMs) / BLOCK_MS);
  }

  timeOfHeight(height: number): number {
    return this.genesisMs + (height - 1) * BLOCK_MS;
  }

  account(address: string): Account | undefined {
    return this.accounts.get(address);
  }

  validatorByMoniker(moniker: string): ValidatorSim {
    const v = this.validators.find((x) => x.moniker === moniker);
    if (!v) throw new Error(`unknown validator ${moniker}`);
    return v;
  }

  /** A repeat counterparty most of the time, otherwise a busy wallet. */
  pickReceiver(signer: Account): Account | null {
    if (this.users.length < 2) return null;
    if (signer.contacts.length > 0 && this.rng.chance(0.65)) return this.rng.pick(signer.contacts);
    for (let i = 0; i < 6; i++) {
      const candidate = this.pickUser();
      if (candidate !== signer) return candidate;
    }
    return null;
  }

  pickValidator(signer: Account): ValidatorSim {
    const active = this.validators.filter((v) => !v.jailed);
    const held = active.filter((v) => signer.stakes.has(v.moniker));
    return held.length > 0 && this.rng.chance(0.7) ? this.rng.pick(held) : this.rng.pick(active);
  }

  pickUser(): Account {
    return this.users[this.rng.weighted(this.userWeights)] as Account;
  }

  /** Generate every transaction up to `nowMs`. Calling it again with a later time continues. */
  advanceTo(nowMs: number): void {
    while (this.nextEventMs <= nowMs) this.step();
  }

  /**
   * Generate every transaction in blocks up to and including `height`, and
   * nothing later. The result does not depend on how often this is called.
   */
  advanceThroughHeight(height: number): void {
    const end = this.timeOfHeight(height + 1);
    while (this.nextEventMs < end) this.step();
  }

  private step(): void {
    this.cursorMs = this.nextEventMs;
    this.emit(this.blockTimeAt(this.cursorMs));
    this.nextEventMs = this.cursorMs + this.rng.exponential(this.rateAt(this.cursorMs)) * 1000;
  }

  private blockTimeAt(ms: number): number {
    return this.timeOfHeight(this.heightAt(ms));
  }

  private rateAt(ms: number): number {
    if (ms >= this.historyEndMs) return LIVE_RATE;
    const progress = (ms - this.genesisMs) / (this.historyEndMs - this.genesisMs);
    return HISTORY_RATE_START * Math.pow(HISTORY_RATE_END / HISTORY_RATE_START, progress);
  }

  private targetUsers(ms: number): number {
    const progress = Math.min(1, (ms - this.genesisMs) / (this.historyEndMs - this.genesisMs));
    return ONBOARD_START + (ONBOARD_END - ONBOARD_START) * progress;
  }

  private addAccount(ref: Account["ref"]): Account {
    const account = new Account(ref);
    this.accounts.set(ref.address, account);
    return account;
  }

  private genesisFunding(): void {
    let t = this.genesisMs + 10_000;
    const fund = (to: Account, orama: number) => {
      this.clockMs = this.blockTimeAt(t);
      this.commit(this.fundingPlan(to, orama), this.clockMs);
      t += 6_000;
    };
    for (const v of this.validators) fund(this.addAccount(operatorRef(v.moniker)), OPERATOR_FUNDING);
    for (const p of this.providers) fund(p, PROVIDER_FUNDING);
    this.cursorMs = t;
  }

  private fundingPlan(to: Account, orama: number): Plan {
    const amount = BigInt(orama) * NORAMA_PER_ORAMA;
    const from = this.foundation;
    return {
      signer: from,
      others: [to],
      message: { type: "send", from: from.ref, to: to.ref, amount: amount.toString() },
      gasUsed: 71_000,
      gasWanted: 93_000,
      failure: null,
      events: [{ type: "transfer", attributes: { sender: from.ref.address, recipient: to.ref.address, amount: `${amount}norama` } }],
      apply: () => {
        from.available -= amount;
        to.available += amount;
        return [];
      },
    };
  }

  private onboard(timeMs: number): boolean {
    const user = this.addAccount(userRef(this.users.length));
    const orama = this.rng.int(60, 3000);
    this.users.push(user);
    this.userWeights.push(1 / Math.pow(this.users.length, ZIPF_EXPONENT));
    return this.commit(this.fundingPlan(user, orama), timeMs) !== null;
  }

  private emit(timeMs: number): void {
    this.clockMs = timeMs;
    if (this.users.length < this.targetUsers(timeMs) * 0.9) {
      this.onboard(timeMs);
      return;
    }
    for (let attempt = 0; attempt < 6; attempt++) {
      const kind = KIND_WEIGHTS[this.rng.weighted(KIND_WEIGHTS.map((k) => k[1]))]?.[0] as Kind;
      const signer = this.pickUser();
      signer.mature(timeMs);
      const p = plan(this, kind, signer);
      if (p && this.commit(p, timeMs)) return;
    }
  }

  private commit(p: Plan, timeMs: number): TxRecord | null {
    const fee = feeOf(p.gasUsed);
    if (p.signer.available < fee) return null;
    const touched = [p.signer, ...p.others];
    const before = new Map(touched.map((a) => [a, a.available]));
    const system = p.failure ? [] : p.apply();
    p.signer.available -= fee;
    this.burned += fee;
    for (const s of system) if (s.name === "minted") this.minted -= s.delta;
    const changes = changeRows(touched, before, fee, system);
    return this.record(p, timeMs, changes, fee);
  }

  private record(p: Plan, timeMs: number, changes: BalanceChange[], fee: bigint): TxRecord {
    const index = this.txs.length;
    const height = this.heightAt(timeMs);
    const involved = new Set([p.signer, ...p.others]);
    const summary: TxSummary = {
      hash: hashFor(`tx:${this.seed}:${index}`),
      height,
      time: new Date(timeMs).toISOString(),
      status: p.failure ? { ok: false, reason: p.failure } : { ok: true },
      signer: p.signer.ref,
      messages: [p.message],
      fee: { burned: fee.toString(), tip: "0", gasUsed: p.gasUsed, gasWanted: p.gasWanted },
    };
    const rec: TxRecord = { index, timeMs, height, summary, changes, events: p.events, memo: "" };
    this.txs.push(rec);
    this.byHash.set(summary.hash, index);
    const bucket = this.byHeight.get(height);
    if (bucket) bucket.push(index);
    else this.byHeight.set(height, [index]);
    for (const a of involved) {
      a.txIndices.push(index);
      if (a.firstSeenMs === null) a.firstSeenMs = timeMs;
      a.snapshot(timeMs);
    }
    return rec;
  }
}

