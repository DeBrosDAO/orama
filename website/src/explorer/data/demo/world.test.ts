import { beforeAll, describe, expect, it } from "vitest";
import { World, BLOCK_MS, GENESIS_SUPPLY } from "./world";
import { parseNorama } from "../../model/units";
import { MAX_CLAIM_FEE, MIN_CLAIM } from "./plans";
import type { TxRecord } from "./world";

const NOW = Date.UTC(2026, 8, 29, 14, 0, 0);
const ANCHOR = Date.UTC(2026, 8, 29);
/** Building a 30-day world is the slow part; tests that only read share one. */
const HEAVY_TEST_MS = 30_000;
function build(seed = 7): World {
  const w = new World({ seed, anchorMs: ANCHOR });
  w.advanceTo(NOW);
  return w;
}
let shared: World | null = null;
const built = (): World => (shared ??= build());

beforeAll(() => {
  built();
}, HEAVY_TEST_MS);

describe("World", () => {
  it("TestWorld_same_seed_same_history", () => {
    const a = built();
    const b = build();
    expect(a.txs.length).toBe(b.txs.length);
    expect(a.txs.at(-1)?.summary.hash).toBe(b.txs.at(-1)?.summary.hash);
    expect(build(8).txs.at(-1)?.summary.hash).not.toBe(a.txs.at(-1)?.summary.hash);
  }, HEAVY_TEST_MS);

  it("TestWorld_has_a_lively_history", () => {
    const w = built();
    expect(w.txs.length).toBeGreaterThan(3000);
    expect(w.users.length).toBeGreaterThan(80);
    expect(w.txs.at(-1)!.timeMs).toBeLessThanOrEqual(NOW);
  });

  it("TestWorld_supply_is_conserved", () => {
    const w = built();
    // Every unit is in a wallet, a stake, unbonding, a storage escrow, or burned.
    let held = 0n;
    for (const a of w.accounts.values()) held += a.total();
    let escrow = 0n;
    for (const r of w.txs) {
      if (!r.summary.status.ok) continue;
      for (const c of r.changes) if (c.party.kind === "system" && c.party.name === "storage_escrow") escrow += parseNorama(c.delta);
    }
    expect(held + escrow + w.burned).toBe(GENESIS_SUPPLY + w.minted);
  });

  it("TestWorld_minted_total_is_what_the_minted_pool_gave_out", () => {
    const w = built();
    let given = 0n;
    for (const r of w.txs) for (const c of r.changes) if (c.party.kind === "system" && c.party.name === "minted") given -= parseNorama(c.delta);
    expect(given).toBeGreaterThan(0n);
    expect(w.minted).toBe(given);
  });

  it("TestWorld_change_rows_match_replayed_balances", () => {
    const w = built();
    for (const r of w.txs.slice(0, 500)) {
      for (const c of r.changes) {
        if (c.party.kind !== "wallet") continue;
        expect(parseNorama(c.after!) - parseNorama(c.before!)).toBe(parseNorama(c.delta));
        expect(parseNorama(c.after!)).toBeGreaterThanOrEqual(0n);
      }
    }
  });

  it("TestWorld_a_failed_transaction_only_charges_the_fee", () => {
    const w = built();
    const failed = w.txs.filter((r) => !r.summary.status.ok);
    expect(failed.length).toBeGreaterThan(50);
    for (const r of failed.slice(0, 50)) {
      const wallets = r.changes.filter((c) => c.party.kind === "wallet");
      expect(wallets).toHaveLength(1);
      expect(parseNorama(wallets[0]!.delta)).toBe(-parseNorama(r.summary.fee.burned));
    }
  });

  it("TestWorld_heights_follow_block_time", () => {
    const w = built();
    for (const r of w.txs.slice(-200)) {
      expect(r.timeMs).toBe(w.timeOfHeight(r.height));
      expect(r.timeMs % BLOCK_MS).toBe(w.genesisMs % BLOCK_MS);
    }
    const times = w.txs.map((r) => r.timeMs);
    expect(times).toEqual([...times].sort((x, y) => x - y));
  });

  it("TestWorld_advanceTo_extends_history_without_rewriting_it", () => {
    const w = build();
    const before = w.txs.length;
    const firstHash = w.txs[0]!.summary.hash;
    w.advanceTo(NOW + 60_000);
    expect(w.txs.length).toBeGreaterThan(before);
    expect(w.txs[0]!.summary.hash).toBe(firstHash);
    w.advanceTo(NOW);
    expect(w.txs.length).toBeGreaterThan(before);
  }, HEAVY_TEST_MS);

  it("TestWorld_last_day_is_busy_enough_to_demo", () => {
    const w = built();
    const day = w.txs.filter((r) => r.timeMs >= NOW - 24 * 3600_000).length;
    expect(day).toBeGreaterThan(300);
  });
});

const byType = (w: World, type: string, ok = true): TxRecord[] =>
  w.txs.filter((r) => r.summary.status.ok === ok && r.summary.messages[0]?.type === type);
const sum = (r: TxRecord): bigint => r.changes.reduce((n, c) => n + parseNorama(c.delta), 0n);
const pool = (r: TxRecord, name: string): bigint | null => {
  const row = r.changes.find((c) => c.party.kind === "system" && c.party.name === name);
  return row ? parseNorama(row.delta) : null;
};

describe("World balance rows are zero-sum", () => {
  it("TestWorld_every_transaction_sums_to_zero_including_failed_ones", () => {
    const w = built();
    expect(w.txs.some((r) => !r.summary.status.ok)).toBe(true);
    for (const r of w.txs) expect(sum(r)).toBe(0n);
  });

  it("TestWorld_delegate_moves_wallet_value_into_the_staked_pool", () => {
    const rows = byType(built(), "delegate");
    expect(rows.length).toBeGreaterThan(20);
    for (const r of rows) {
      const m = r.summary.messages[0]!;
      const amount = parseNorama("amount" in m ? m.amount : "0");
      expect(pool(r, "staked")).toBe(amount);
    }
  });

  it("TestWorld_undelegate_moves_value_from_staked_to_unbonding", () => {
    const rows = byType(built(), "undelegate");
    expect(rows.length).toBeGreaterThan(5);
    for (const r of rows) {
      const m = r.summary.messages[0]!;
      const amount = parseNorama("amount" in m ? m.amount : "0");
      expect(pool(r, "staked")).toBe(-amount);
      expect(pool(r, "unbonding")).toBe(amount);
    }
  });

  it("TestWorld_claim_takes_the_reward_from_the_minted_pool_and_pays_the_wallet", () => {
    const rows = byType(built(), "claim_rewards");
    expect(rows.length).toBeGreaterThan(20);
    for (const r of rows) {
      const m = r.summary.messages[0]!;
      const paid = parseNorama("amount" in m ? m.amount : "0");
      expect(pool(r, "minted")).toBe(-paid);
      const wallet = r.changes.find((c) => c.party.kind === "wallet")!;
      expect(parseNorama(wallet.delta)).toBe(paid - parseNorama(r.summary.fee.burned));
    }
  });

  it("TestWorld_storage_moves_wallet_value_into_escrow_and_a_failed_tx_touches_no_pool_but_burned", () => {
    const w = built();
    for (const r of byType(w, "storage")) expect(pool(r, "storage_escrow")).toBeGreaterThan(0n);
    for (const r of byType(w, "send", false)) {
      const systems = r.changes.filter((c) => c.party.kind === "system");
      expect(systems).toHaveLength(1);
      expect(pool(r, "burned")).toBe(parseNorama(r.summary.fee.burned));
    }
  });
});

describe("World staking bookkeeping", () => {
  it("TestMinClaim_is_above_the_largest_claim_fee", () => {
    expect(MIN_CLAIM).toBeGreaterThan(MAX_CLAIM_FEE);
  });

  it("TestWorld_a_claim_never_loses_money", () => {
    const rows = byType(built(), "claim_rewards");
    for (const r of rows) {
      const m = r.summary.messages[0]!;
      const paid = parseNorama("amount" in m ? m.amount : "0");
      expect(paid).toBeGreaterThanOrEqual(MIN_CLAIM);
      const wallet = r.changes.find((c) => c.party.kind === "wallet")!;
      expect(parseNorama(wallet.delta)).toBeGreaterThan(0n);
    }
  });

  it("TestWorld_no_stake_entry_is_left_empty_with_nothing_owed", () => {
    const w = built();
    let entries = 0;
    for (const a of w.accounts.values()) {
      for (const [moniker, s] of a.stakes) {
        entries++;
        expect(s.amount > 0n || a.accrued(moniker, NOW) > 0n).toBe(true);
      }
    }
    expect(entries).toBeGreaterThan(0);
  });
});

describe("World stability", () => {
  it("TestWorld_history_does_not_depend_on_when_you_look", () => {
    const early = new World({ seed: 7, anchorMs: ANCHOR });
    early.advanceTo(ANCHOR + 3_600_000);
    const late = new World({ seed: 7, anchorMs: ANCHOR });
    late.advanceTo(ANCHOR + 10 * 3_600_000);
    const shared = early.txs.length;
    expect(shared).toBeGreaterThan(100);
    expect(late.txs.slice(0, shared).map((r) => r.summary.hash)).toEqual(early.txs.map((r) => r.summary.hash));
    expect(late.txs.slice(0, shared).map((r) => r.timeMs)).toEqual(early.txs.map((r) => r.timeMs));
  }, HEAVY_TEST_MS);

  it("TestWorld_advanceThroughHeight_never_generates_into_a_later_block", () => {
    const w = new World({ seed: 7, anchorMs: ANCHOR });
    const height = w.heightAt(ANCHOR + 3_600_000);
    w.advanceThroughHeight(height);
    expect(w.txs.length).toBeGreaterThan(100);
    expect(Math.max(...w.txs.map((r) => r.height))).toBeLessThanOrEqual(height);
    const count = w.txs.length;
    w.advanceThroughHeight(height);
    expect(w.txs).toHaveLength(count);
    w.advanceThroughHeight(height + 1);
    expect(Math.max(...w.txs.map((r) => r.height))).toBeLessThanOrEqual(height + 1);
  }, HEAVY_TEST_MS);

  it("TestWorld_advanceThroughHeight_and_advanceTo_generate_the_same_transactions", () => {
    const byHeight = new World({ seed: 7, anchorMs: ANCHOR });
    const byTime = new World({ seed: 7, anchorMs: ANCHOR });
    const height = byHeight.heightAt(ANCHOR + 3_600_000);
    byHeight.advanceThroughHeight(height);
    byTime.advanceTo(byTime.timeOfHeight(height + 1) - 1);
    expect(byHeight.txs.map((r) => r.summary.hash)).toEqual(byTime.txs.map((r) => r.summary.hash));
  }, HEAVY_TEST_MS);
});
