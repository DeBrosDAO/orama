import { describe, expect, it, vi } from "vitest";
import { validatorSet } from "./queries-chain";
import { counterparties, walletActivity, walletProfile } from "./queries-wallet";
import { World } from "./world";
import type { ActivityQuery } from "../../model/types";
import { NORAMA_PER_ORAMA } from "../../model/units";

/** A 30-day world takes seconds to build, and a busy machine takes longer. */
vi.setConfig({ testTimeout: 30_000 });

const NOW = Date.UTC(2026, 8, 29, 14, 0, 0);
const QUERY: ActivityQuery = { filter: "all", counterparty: null, cursor: null, limit: 10 };

const world = (() => {
  const w = new World({ seed: 7, anchorMs: Date.UTC(2026, 8, 29) });
  w.advanceTo(NOW);
  return w;
})();

/** A wallet with enough history for several pages. */
const busy = world.users[0]!;
const addressOf = busy.ref.address;

describe("walletActivity cursors", () => {
  it("TestWalletActivity_null_cursor_is_the_first_page_and_its_cursor_continues", () => {
    const first = walletActivity(world, addressOf, QUERY);
    expect(first.items).toHaveLength(10);
    expect(first.nextCursor).not.toBeNull();
    const second = walletActivity(world, addressOf, { ...QUERY, cursor: first.nextCursor });
    expect(second.items[0]!.hash).not.toBe(first.items[0]!.hash);
  });

  it("TestWalletActivity_rejects_cursors_this_source_never_issued", () => {
    const length = busy.txIndices.length;
    for (const bad of ["", "-1", "abc", "1.5", "1e2", " 3", "007", String(length), String(length + 1000), "99999999999999999999"]) {
      expect(() => walletActivity(world, addressOf, { ...QUERY, cursor: bad }), JSON.stringify(bad)).toThrow("invalid activity cursor");
    }
  });

  it("TestWalletActivity_accepts_the_first_and_last_valid_positions", () => {
    const length = busy.txIndices.length;
    expect(walletActivity(world, addressOf, { ...QUERY, cursor: "0" }).items).toHaveLength(1);
    expect(walletActivity(world, addressOf, { ...QUERY, cursor: String(length - 1) }).items).toHaveLength(10);
  });

  it("TestWalletActivity_unknown_wallet_is_an_empty_page_and_any_cursor_is_invalid", () => {
    const nobody = "orama1nobody";
    expect(walletActivity(world, nobody, QUERY)).toEqual({ items: [], nextCursor: null });
    expect(() => walletActivity(world, nobody, { ...QUERY, cursor: "0" })).toThrow("invalid activity cursor");
  });
});

describe("staking rows have no counterparty", () => {
  it("TestWalletActivity_staking_items_carry_counterparty_null", () => {
    const staking = walletActivity(world, addressOf, { ...QUERY, filter: "staking", limit: 200 }).items;
    expect(staking.length).toBeGreaterThan(0);
    for (const i of staking) expect(i.counterparty).toBeNull();
  });

  it("TestWalletActivity_transfers_and_storage_keep_their_counterparty", () => {
    const items = walletActivity(world, addressOf, { ...QUERY, limit: 200 }).items;
    for (const i of items) {
      if (i.message.type === "send" || i.message.type === "storage_deal") expect(i.counterparty).not.toBeNull();
    }
  });

  it("TestCounterparties_never_list_a_validator_operator_for_staking", () => {
    const operators = new Set(world.validators.map((v) => v.ref.operator));
    for (const c of counterparties(world, addressOf, 50)) expect(operators.has(c.ref.address)).toBe(false);
  });

  it("TestCounterparties_are_ordered_by_volume_descending_and_limited", () => {
    const rows = counterparties(world, addressOf, 5);
    expect(rows.length).toBeLessThanOrEqual(5);
    const volumes = rows.map((r) => BigInt(r.volume));
    expect(volumes).toEqual([...volumes].sort((a, b) => (b > a ? 1 : b < a ? -1 : 0)));
    expect(counterparties(world, addressOf, 0)).toEqual([]);
  });
});

describe("delegators and top delegate", () => {
  it("TestValidatorSet_delegators_counts_wallets_with_a_positive_stake", () => {
    let expected = 0;
    for (const a of world.accounts.values()) if ([...a.stakes.values()].some((s) => s.amount > 0n)) expected++;
    expect(expected).toBeGreaterThan(0);
    expect(validatorSet(world).delegators).toBe(expected);
  });

  it("TestWalletProfile_top_delegate_is_never_a_validator_with_no_stake", () => {
    let named = 0;
    for (const a of world.accounts.values()) {
      if (a.txIndices.length === 0) continue;
      const top = walletProfile(world, a.ref.address, NOW)!.facts.topDelegate;
      if (top === null) continue;
      named++;
      expect(a.stakes.get(top.moniker)!.amount).toBeGreaterThan(0n);
    }
    expect(named).toBeGreaterThan(0);
  });

  it("TestWalletProfile_top_delegate_ignores_an_entry_that_only_holds_unpaid_rewards", () => {
    const w = new World({ seed: 9, anchorMs: Date.UTC(2026, 8, 29) });
    w.advanceTo(NOW);
    const a = w.users.find((u) => u.stakes.size === 0 && u.txIndices.length > 0)!;
    const before = validatorSet(w).delegators;
    a.stakes.set("val-1", { amount: 0n, sinceMs: NOW, carry: 5n });
    expect(walletProfile(w, a.ref.address, NOW)!.facts.topDelegate).toBeNull();
    expect(validatorSet(w).delegators).toBe(before);
    a.stakes.set("val-2", { amount: 7n * NORAMA_PER_ORAMA, sinceMs: NOW, carry: 0n });
    expect(walletProfile(w, a.ref.address, NOW)!.facts.topDelegate?.moniker).toBe("val-2");
    expect(validatorSet(w).delegators).toBe(before + 1);
  });
});
