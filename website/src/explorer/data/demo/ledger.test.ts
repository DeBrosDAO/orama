import { describe, expect, it } from "vitest";
import { NORAMA_PER_ORAMA } from "../../model/units";
import { TIME } from "../../model/time";
import { Account, UNBONDING_MS } from "./ledger";

const T0 = Date.UTC(2026, 8, 1);
const STAKE = 1_000n * NORAMA_PER_ORAMA;
const account = (): Account => {
  const a = new Account({ address: "orama1test" });
  a.available = 5_000n * NORAMA_PER_ORAMA;
  return a;
};

describe("Account staking", () => {
  it("TestAccount_unstake_everything_with_nothing_accrued_removes_the_entry", () => {
    const a = account();
    a.stake("val-1", STAKE, T0);
    a.unstake("val-1", STAKE, T0);
    expect(a.stakes.has("val-1")).toBe(false);
    expect(a.staked()).toBe(0n);
    expect(a.unbondingTotal()).toBe(STAKE);
  });

  it("TestAccount_unstake_everything_keeps_the_entry_until_the_rewards_are_claimed", () => {
    const a = account();
    a.stake("val-1", STAKE, T0);
    const later = T0 + TIME.DAY;
    a.unstake("val-1", STAKE, later);
    const owed = a.claimable(later);
    expect(owed).toBeGreaterThan(0n);
    expect(a.stakes.get("val-1")?.amount).toBe(0n);
    expect(a.claim("val-1", later)).toBe(owed);
    expect(a.stakes.has("val-1")).toBe(false);
    expect(a.claimable(later)).toBe(0n);
  });

  it("TestAccount_claim_on_a_live_stake_keeps_it_and_resets_the_accrual", () => {
    const a = account();
    a.stake("val-1", STAKE, T0);
    const later = T0 + TIME.DAY;
    const paid = a.claim("val-1", later);
    expect(paid).toBeGreaterThan(0n);
    expect(a.stakes.get("val-1")?.amount).toBe(STAKE);
    expect(a.claimable(later)).toBe(0n);
  });

  it("TestAccount_partial_unstake_leaves_the_rest_staked", () => {
    const a = account();
    a.stake("val-1", STAKE, T0);
    a.unstake("val-1", STAKE / 2n, T0);
    expect(a.staked()).toBe(STAKE / 2n);
  });

  it("TestAccount_claim_for_an_unknown_validator_pays_nothing", () => {
    expect(account().claim("nobody", T0)).toBe(0n);
  });

  it("TestAccount_unstake_more_than_staked_throws", () => {
    const a = account();
    a.stake("val-1", STAKE, T0);
    expect(() => a.unstake("val-1", STAKE + 1n, T0)).toThrow(/cannot unstake/);
    expect(() => a.unstake("nobody", 1n, T0)).toThrow(/cannot unstake/);
  });

  it("TestAccount_unstaked_funds_become_available_after_the_unbonding_period", () => {
    const a = account();
    a.stake("val-1", STAKE, T0);
    a.unstake("val-1", STAKE, T0);
    const before = a.available;
    a.mature(T0 + UNBONDING_MS - 1);
    expect(a.available).toBe(before);
    a.mature(T0 + UNBONDING_MS);
    expect(a.available).toBe(before + STAKE);
  });
});
