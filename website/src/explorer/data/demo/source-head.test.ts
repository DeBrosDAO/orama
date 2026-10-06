import { afterEach, describe, expect, it, vi } from "vitest";
import { MAX_LIST_LIMIT, clampLimit } from "../source";
import { createDemoSource } from "./source";
import { examples } from "./source-reads";
import { BLOCK_MS } from "./world";
import type { World } from "./world";

/** Exactly on a block boundary, so "less than a block later" is unambiguous. */
const BLOCK_START = Date.UTC(2026, 8, 29, 14, 0, 0);

/** A 30-day world takes seconds to build, and a busy machine takes longer. */
vi.setConfig({ testTimeout: 30_000 });

/** For tests that only read at a fixed instant: one world is enough. */
const fixed = createDemoSource({ seed: 11, now: () => BLOCK_START });

afterEach(() => {
  vi.useRealTimers();
});

describe("the head is a finished block", () => {
  it("TestDemoSource_head_is_the_block_before_the_one_in_progress", async () => {
    const s = createDemoSource({ seed: 11, now: () => BLOCK_START + 500 });
    const head = await s.getHead();
    const network = await s.getNetwork();
    expect(network.height).toBe(head.height);
    const [newest] = await s.getRecentBlocks(1);
    expect(newest!.height).toBe(head.height);
    expect(await s.getBlock(head.height)).not.toBeNull();
    expect(await s.getBlock(head.height + 1)).toBeNull();
    const t0 = new Date(BLOCK_START).toISOString();
    expect(head.time < t0).toBe(true);
  });

  it("TestDemoSource_a_block_reads_identically_while_time_advances_within_a_block", async () => {
    let now = BLOCK_START + 100;
    const s = createDemoSource({ seed: 11, now: () => now });
    const head = await s.getHead();
    const first = await s.getBlock(head.height);
    const firstRecent = await s.getRecentBlocks(5);
    now = BLOCK_START + BLOCK_MS - 1;
    expect((await s.getHead()).height).toBe(head.height);
    expect(await s.getBlock(head.height)).toEqual(first);
    expect(await s.getRecentBlocks(5)).toEqual(firstRecent);
  });

  it("TestDemoSource_a_block_stays_identical_after_it_stops_being_the_head", async () => {
    let now = BLOCK_START + 100;
    const s = createDemoSource({ seed: 11, now: () => now });
    const head = await s.getHead();
    const before = await s.getBlock(head.height);
    now += 20 * BLOCK_MS;
    expect((await s.getHead()).height).toBe(head.height + 20);
    expect(await s.getBlock(head.height)).toEqual(before);
  });

  it("TestDemoSource_no_transaction_is_visible_above_the_head", async () => {
    let now = BLOCK_START;
    const s = createDemoSource({ seed: 11, now: () => now });
    for (let i = 0; i < 40; i++) {
      now += 700;
      const head = await s.getHead();
      const latest = await s.getLatestActivity("all", 20);
      expect(latest.every((t) => t.height <= head.height)).toBe(true);
      const blocks = await s.getRecentBlocks(20);
      expect(Math.max(...blocks.map((b) => b.height))).toBe(head.height);
      const wallet = (await s.getExamples()).busyWalletAddress!;
      const activity = await s.getWalletActivity(wallet, { filter: "all", counterparty: null, cursor: null, limit: 5 });
      expect(activity.items.every((it) => Date.parse(it.time) <= Date.parse(head.time))).toBe(true);
    }
  });
});

describe("subscribeHead in live mode", () => {
  it("TestDemoSource_subscribeHead_fires_when_the_head_advances_and_stops_after_unsubscribe", () => {
    vi.useFakeTimers();
    let now = BLOCK_START;
    const s = createDemoSource({ seed: 11, now: () => now, live: true });
    const heights: number[] = [];
    const stop = s.subscribeHead((h) => heights.push(h.height));

    vi.advanceTimersByTime(BLOCK_MS);
    expect(heights).toEqual([]);

    now += BLOCK_MS;
    vi.advanceTimersByTime(BLOCK_MS);
    expect(heights).toHaveLength(1);

    now += 3 * BLOCK_MS;
    vi.advanceTimersByTime(BLOCK_MS);
    expect(heights).toHaveLength(2);
    expect(heights[1]! - heights[0]!).toBe(3);

    stop();
    now += 5 * BLOCK_MS;
    vi.advanceTimersByTime(10 * BLOCK_MS);
    expect(heights).toHaveLength(2);
  });

  it("TestDemoSource_subscribeHead_does_not_announce_the_head_it_started_at", () => {
    vi.useFakeTimers();
    const s = createDemoSource({ seed: 11, now: () => BLOCK_START, live: true });
    const listener = vi.fn();
    const stop = s.subscribeHead(listener);
    vi.advanceTimersByTime(20 * BLOCK_MS);
    stop();
    expect(listener).not.toHaveBeenCalled();
  });
});

describe("limits are clamped by the implementation", () => {
  it("TestClampLimit_bounds_negative_nan_and_huge_values", () => {
    expect(clampLimit(-5)).toBe(0);
    expect(clampLimit(Number.NaN)).toBe(0);
    expect(clampLimit(0)).toBe(0);
    expect(clampLimit(7.9)).toBe(7);
    expect(clampLimit(MAX_LIST_LIMIT)).toBe(MAX_LIST_LIMIT);
    expect(clampLimit(MAX_LIST_LIMIT + 1)).toBe(MAX_LIST_LIMIT);
    expect(clampLimit(Number.POSITIVE_INFINITY)).toBe(MAX_LIST_LIMIT);
  });

  it("TestDemoSource_every_list_read_honours_the_clamp", async () => {
    const s = fixed;
    expect(await s.getLatestActivity("all", -1)).toEqual([]);
    expect(await s.getLatestActivity("all", Number.NaN)).toEqual([]);
    expect(await s.getLatestActivity("all", 100_000)).toHaveLength(MAX_LIST_LIMIT);
    expect(await s.getRecentBlocks(-3)).toEqual([]);
    expect(await s.getRecentBlocks(100_000)).toHaveLength(MAX_LIST_LIMIT);
    const wallet = (await s.getExamples()).busyWalletAddress!;
    const page = await s.getWalletActivity(wallet, { filter: "all", counterparty: null, cursor: null, limit: 100_000 });
    expect(page.items.length).toBeLessThanOrEqual(MAX_LIST_LIMIT);
    expect((await s.getWalletActivity(wallet, { filter: "all", counterparty: null, cursor: null, limit: -1 })).items).toEqual([]);
    expect(await s.getCounterparties(wallet, -1)).toEqual([]);
    expect(await s.searchLabels("val", -1)).toEqual([]);
    expect((await s.searchLabels("val", 100_000)).length).toBeLessThanOrEqual(MAX_LIST_LIMIT);
  });
});

describe("activity cursors through the source", () => {
  it("TestDemoSource_an_invalid_cursor_rejects_with_an_error", async () => {
    const s = fixed;
    const wallet = (await s.getExamples()).busyWalletAddress!;
    await expect(s.getWalletActivity(wallet, { filter: "all", counterparty: null, cursor: "not-a-cursor", limit: 5 })).rejects.toThrow(
      "invalid activity cursor",
    );
  });
});

describe("examples", () => {
  it("TestExamples_are_null_when_the_world_has_no_transfers_and_no_users", () => {
    const empty = { txs: [], users: [] } as unknown as Pick<World, "txs" | "users">;
    expect(examples(empty)).toEqual({ latestTxHash: null, busyWalletAddress: null });
  });

  it("TestExamples_a_world_with_users_but_no_transfers_still_names_a_wallet", () => {
    const user = { ref: { address: "orama1a" }, txIndices: [1, 2] };
    const idle = { ref: { address: "orama1b" }, txIndices: [] };
    const w = { txs: [], users: [idle, user] } as unknown as Pick<World, "txs" | "users">;
    expect(examples(w)).toEqual({ latestTxHash: null, busyWalletAddress: "orama1a" });
  });

  it("TestDemoSource_examples_name_a_real_transfer_and_wallet", async () => {
    const s = fixed;
    const { latestTxHash, busyWalletAddress } = await s.getExamples();
    expect(latestTxHash).not.toBeNull();
    expect((await s.getTx(latestTxHash!))?.messages[0]?.type).toBe("send");
    expect(await s.getWallet(busyWalletAddress!)).not.toBeNull();
  });
});
