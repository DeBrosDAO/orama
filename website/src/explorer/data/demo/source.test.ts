import { describe, expect, it } from "vitest";
import { createDemoSource } from "./source";
import type { ActivityItem, ActivityQuery } from "../../model/types";
import { parseNorama } from "../../model/units";

const NOW = Date.UTC(2026, 8, 29, 14, 0, 0);
const source = () => createDemoSource({ seed: 11, now: () => NOW });
const QUERY: ActivityQuery = { filter: "all", counterparty: null, cursor: null, limit: 25 };

async function busyWallet(s: ReturnType<typeof source>): Promise<string> {
  const { busyWalletAddress } = await s.getExamples();
  if (busyWalletAddress === null) throw new Error("the demo world has no busy wallet");
  return busyWalletAddress;
}

async function allActivity(s: ReturnType<typeof source>, address: string, q: Partial<ActivityQuery> = {}): Promise<ActivityItem[]> {
  const out: ActivityItem[] = [];
  let cursor: string | null = null;
  do {
    const page = await s.getWalletActivity(address, { ...QUERY, ...q, cursor });
    out.push(...page.items);
    cursor = page.nextCursor;
  } while (cursor !== null);
  return out;
}

describe("demo data source: not found is null, never invented", () => {
  it("TestDemoSource_unknown_tx_wallet_and_block_resolve_to_null", async () => {
    const s = source();
    expect(await s.getTx("0".repeat(64))).toBeNull();
    expect(await s.getWallet("orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq")).toBeNull();
    expect(await s.getBlock(0)).toBeNull();
    expect(await s.getBlock(-5)).toBeNull();
    expect(await s.getBlock(1.5)).toBeNull();
    const head = await s.getHead();
    expect(await s.getBlock(head.height + 1)).toBeNull();
    expect(await s.getBlock(head.height)).not.toBeNull();
  });
});

describe("demo data source: transactions", () => {
  it("TestDemoSource_tx_detail_is_consistent_with_its_summary_and_balances", async () => {
    const s = source();
    const [latest] = await s.getLatestActivity("transfers", 1);
    const tx = await s.getTx(latest!.hash);
    expect(tx).not.toBeNull();
    expect(tx!.hash).toBe(latest!.hash);
    expect(tx!.status.ok).toBe(true);
    const wallets = tx!.balanceChanges.filter((c) => c.party.kind === "wallet");
    expect(wallets.length).toBeGreaterThanOrEqual(2);
    for (const c of wallets) expect(parseNorama(c.after!) - parseNorama(c.before!)).toBe(parseNorama(c.delta));
    expect(JSON.parse(tx!.rawJson).body.messages[0]["@type"]).toBe("/cosmos.bank.v1beta1.MsgSend");
  });

  it("TestDemoSource_tx_lookup_is_case_insensitive", async () => {
    const s = source();
    const [latest] = await s.getLatestActivity("all", 1);
    expect(await s.getTx(latest!.hash.toLowerCase())).not.toBeNull();
  });

  it("TestDemoSource_latest_activity_is_newest_first_and_filters_apply", async () => {
    const s = source();
    const all = await s.getLatestActivity("all", 50);
    const times = all.map((t) => Date.parse(t.time));
    expect(times).toEqual([...times].sort((a, b) => b - a));
    const failed = await s.getLatestActivity("failed", 20);
    expect(failed.length).toBeGreaterThan(0);
    expect(failed.every((t) => !t.status.ok)).toBe(true);
    const staking = await s.getLatestActivity("staking", 20);
    expect(staking.every((t) => t.status.ok && ["delegate", "undelegate", "claim_rewards"].includes(t.messages[0]!.type))).toBe(true);
    expect(await s.getLatestActivity("all", 0)).toEqual([]);
  });
});

describe("demo data source: blocks", () => {
  it("TestDemoSource_recent_blocks_are_consecutive_and_match_block_detail", async () => {
    const s = source();
    const head = await s.getHead();
    const blocks = await s.getRecentBlocks(10);
    expect(blocks.map((b) => b.height)).toEqual(Array.from({ length: 10 }, (_, i) => head.height - i));
    for (const b of blocks.slice(0, 3)) {
      const detail = await s.getBlock(b.height);
      expect(detail!.txCount).toBe(b.txCount);
      expect(detail!.txs).toHaveLength(b.txCount);
      expect(detail!.proposer).toEqual(b.proposer);
    }
  });

  it("TestDemoSource_a_tx_appears_in_the_block_it_claims", async () => {
    const s = source();
    const [latest] = await s.getLatestActivity("all", 1);
    const block = await s.getBlock(latest!.height);
    expect(block!.txs.map((t) => t.hash)).toContain(latest!.hash);
  });
});

describe("demo data source: wallets", () => {
  it("TestDemoSource_wallet_pages_add_up_and_never_repeat", async () => {
    const s = source();
    const busyWalletAddress = await busyWallet(s);
    const profile = await s.getWallet(busyWalletAddress);
    const items = await allActivity(s, busyWalletAddress);
    expect(items).toHaveLength(profile!.facts.txCount);
    expect(new Set(items.map((i) => i.hash)).size).toBe(items.length);
  });

  it("TestDemoSource_wallet_filters_partition_correctly", async () => {
    const s = source();
    const busyWalletAddress = await busyWallet(s);
    const inn = await allActivity(s, busyWalletAddress, { filter: "in" });
    const out = await allActivity(s, busyWalletAddress, { filter: "out" });
    const failed = await allActivity(s, busyWalletAddress, { filter: "failed" });
    const all = await allActivity(s, busyWalletAddress);
    expect(inn.every((i) => i.direction === "in" && i.status.ok)).toBe(true);
    expect(out.every((i) => i.direction === "out" && i.status.ok)).toBe(true);
    expect(failed.every((i) => !i.status.ok)).toBe(true);
    expect(inn.length + out.length + failed.length).toBe(all.length);
  });

  it("TestDemoSource_counterparty_filter_matches_counterparties_summary", async () => {
    const s = source();
    const busyWalletAddress = await busyWallet(s);
    const [top] = await s.getCounterparties(busyWalletAddress, 5);
    expect(top).toBeDefined();
    const only = await allActivity(s, busyWalletAddress, { counterparty: top!.ref.address });
    const ok = only.filter((i) => i.status.ok);
    expect(ok).toHaveLength(top!.txCount);
    const net = ok.reduce((n, i) => n + parseNorama(i.amount), 0n);
    expect(net).toBe(parseNorama(top!.net));
  });

  it("TestDemoSource_balance_total_is_the_sum_of_its_parts_and_ends_the_history", async () => {
    const s = source();
    const busyWalletAddress = await busyWallet(s);
    const p = (await s.getWallet(busyWalletAddress))!;
    const b = p.balance;
    expect(parseNorama(b.available) + parseNorama(b.staked) + parseNorama(b.unbonding)).toBe(parseNorama(b.total));
    for (const range of ["7d", "30d", "all"] as const) {
      const history = await s.getBalanceHistory(busyWalletAddress, range);
      expect(history.length).toBeGreaterThan(1);
      expect(history.at(-1)!.total).toBe(b.total);
      const times = history.map((h) => Date.parse(h.time));
      expect(times).toEqual([...times].sort((x, y) => x - y));
    }
  });

  it("TestDemoSource_roles_and_labels", async () => {
    const s = source();
    const [found] = await s.searchLabels("foundation", 3);
    expect(found!.label).toBe("Orama Foundation");
    expect(found!.verified).toBe(true);
    expect((await s.getWallet(found!.address))!.roles).toEqual(["Foundation"]);
    const ops = await s.searchLabels("val-3", 3);
    expect((await s.getWallet(ops[0]!.address))!.roles).toEqual(["Validator operator"]);
    expect(await s.searchLabels("", 5)).toEqual([]);
    expect(await s.searchLabels("no such name", 5)).toEqual([]);
  });
});

describe("demo data source: network and validators", () => {
  it("TestDemoSource_network_snapshot_is_sane", async () => {
    const s = source();
    const n = await s.getNetwork();
    const head = await s.getHead();
    expect(n.height).toBe(head.height);
    expect(n.transactions24h).toBeGreaterThan(0);
    expect(n.transactionsSeries).toHaveLength(24);
    expect(n.transactionsSeries.reduce((a, b) => a + b, 0)).toBe(n.transactions24h);
    expect(n.epoch.progress).toBeGreaterThanOrEqual(0);
    expect(n.epoch.progress).toBeLessThan(1);
    expect(n.validatorsSigning).toBeLessThanOrEqual(n.validatorsTotal);
  });

  it("TestDemoSource_validator_power_sums_to_one_and_jailed_hold_none", async () => {
    const set = await source().getValidators();
    expect(set.validators.reduce((n, v) => n + v.power, 0)).toBeCloseTo(1, 6);
    expect(set.validators.filter((v) => v.jailed).every((v) => v.power === 0)).toBe(true);
    expect(set.lambda).toBeGreaterThan(0);
    expect(set.lambda).toBeLessThanOrEqual(0.9);
    expect(set.nakamoto).toBeGreaterThanOrEqual(1);
    for (const v of set.validators) expect(v.uptimeDays).toHaveLength(30);
  });
});

describe("demo data source: live head", () => {
  it("TestDemoSource_subscribeHead_is_inert_unless_live", async () => {
    const stop = source().subscribeHead(() => {
      throw new Error("should not be called");
    });
    stop();
  });

  it("TestDemoSource_head_follows_the_clock_and_history_is_stable", async () => {
    let now = NOW;
    const s = createDemoSource({ seed: 11, now: () => now });
    const before = await s.getHead();
    const [oldest] = (await s.getLatestActivity("all", 500)).slice(-1);
    now += 60_000;
    const after = await s.getHead();
    expect(after.height).toBe(before.height + 30);
    expect(await s.getTx(oldest!.hash)).not.toBeNull();
  });
});
