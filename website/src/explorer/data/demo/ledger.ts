import type { WalletRef } from "../../model/types";
import { TIME } from "../../model/time";
import { NORAMA_PER_ORAMA } from "../../model/units";

/** Demo staking yield: about 12% a year, accrued per second on the staked amount. */
const REWARD_RATE_PER_SECOND = 0.12 / (365 * 24 * 3600);
const UNBONDING_DAYS = 21;
/** Unstaked funds wait this long before they are spendable again. */
export const UNBONDING_MS = UNBONDING_DAYS * TIME.DAY;

interface Stake {
  amount: bigint;
  sinceMs: number;
  /** Rewards already accrued before the last change to `amount`. */
  carry: bigint;
}

interface Unbonding {
  amount: bigint;
  completesMs: number;
}

export interface LedgerPoint {
  timeMs: number;
  total: bigint;
}

/** One wallet's balances and history in the demo world. */
export class Account {
  available = 0n;
  readonly stakes = new Map<string, Stake>();
  unbonding: Unbonding[] = [];
  /** Total (available + staked + unbonding) after every transaction that changed it. */
  readonly points: LedgerPoint[] = [];
  /** Indices into the world's transaction list, oldest first. */
  readonly txIndices: number[] = [];
  firstSeenMs: number | null = null;
  /** Wallets this one has sent to, oldest first; drives repeat payments in the demo. */
  readonly contacts: Account[] = [];

  constructor(readonly ref: WalletRef) {}

  staked(): bigint {
    let sum = 0n;
    for (const s of this.stakes.values()) sum += s.amount;
    return sum;
  }

  unbondingTotal(): bigint {
    let sum = 0n;
    for (const u of this.unbonding) sum += u.amount;
    return sum;
  }

  total(): bigint {
    return this.available + this.staked() + this.unbondingTotal();
  }

  /** Move unstaked funds whose waiting period is over into `available`. */
  mature(nowMs: number): void {
    const still: Unbonding[] = [];
    for (const u of this.unbonding) {
      if (u.completesMs <= nowMs) this.available += u.amount;
      else still.push(u);
    }
    this.unbonding = still;
  }

  accrued(moniker: string, nowMs: number): bigint {
    const s = this.stakes.get(moniker);
    if (!s) return 0n;
    const seconds = Math.max(0, (nowMs - s.sinceMs) / 1000);
    return s.carry + BigInt(Math.floor(Number(s.amount) * REWARD_RATE_PER_SECOND * seconds));
  }

  claimable(nowMs: number): bigint {
    let sum = 0n;
    for (const moniker of this.stakes.keys()) sum += this.accrued(moniker, nowMs);
    return sum;
  }

  stake(moniker: string, amount: bigint, nowMs: number): void {
    const carry = this.accrued(moniker, nowMs);
    const prior = this.stakes.get(moniker)?.amount ?? 0n;
    this.stakes.set(moniker, { amount: prior + amount, sinceMs: nowMs, carry });
    this.available -= amount;
  }

  /**
   * Start unstaking; the amount becomes spendable after UNBONDING_MS. An entry
   * with nothing staked and nothing accrued is removed; one that still holds
   * unpaid rewards stays until they are claimed.
   */
  unstake(moniker: string, amount: bigint, nowMs: number): void {
    const s = this.stakes.get(moniker);
    if (!s || amount > s.amount) throw new Error(`cannot unstake ${amount} from ${moniker}`);
    const carry = this.accrued(moniker, nowMs);
    if (s.amount === amount && carry === 0n) this.stakes.delete(moniker);
    else this.stakes.set(moniker, { amount: s.amount - amount, sinceMs: nowMs, carry });
    this.unbonding.push({ amount, completesMs: nowMs + UNBONDING_MS });
  }

  /** Pay out everything accrued for one validator; returns the amount. An emptied entry is removed. */
  claim(moniker: string, nowMs: number): bigint {
    const s = this.stakes.get(moniker);
    if (!s) return 0n;
    const paid = this.accrued(moniker, nowMs);
    if (s.amount === 0n) this.stakes.delete(moniker);
    else this.stakes.set(moniker, { amount: s.amount, sinceMs: nowMs, carry: 0n });
    this.available += paid;
    return paid;
  }

  snapshot(timeMs: number): void {
    this.points.push({ timeMs, total: this.total() });
  }
}

/** Round to the nearest half ORAMA and return it in norama. */
export const halfOramaToNorama = (orama: number): bigint => BigInt(Math.round(orama * 2)) * (NORAMA_PER_ORAMA / 2n);
